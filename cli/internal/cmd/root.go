// Package cmd is the frontseat command line: one command to a file, as
// cobra-cli lays an application out. A command talks to a daemon over
// the API and holds no build state; what it prints is plain lines, in a
// terminal or not.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
	"github.com/frontseat-dev/frontseat/sdk/go"
	"github.com/frontseat-dev/frontseat/server/telemetry"
)

// instrumented is how every command reaches a server: through the
// command's own telemetry, so that each call is part of its trace and
// measured with it. The SDK takes it as an option and imports none of
// it.
var instrumented = frontseat.WithHTTPClient(&http.Client{Transport: telemetry.Transport{}})

// inside says whether the command line runs inside a workspace's
// sandbox (machine.Inside), and here is where the daemon there is asked;
// a test stands in for both.
var (
	inside = machine.Inside
	here   = machine.Daemon
)

// fixed is what a command that would change or describe a workspace is
// refused with inside a workspace's sandbox: there its source is fixed,
// and its daemon is asked to build, run and release alone.
var fixed = errors.New("inside a workspace's sandbox its source is fixed: workspaces are opened, filled, described, listed and ended from outside, by their names, and here the command line asks this workspace's daemon to build, run and release alone")

// server is the client of the control plane every command asks: the
// one frontseat/local/server names, or the local stack's, presented the
// token laid out beside it, and that token presented to it alone.
// Inside a workspace's sandbox none is asked.
func server() (*frontseat.Client, error) {
	if inside() {
		return nil, fixed
	}
	at, err := machine.Server()
	if err != nil {
		return nil, err
	}
	token, err := machine.Token()
	if err != nil {
		return nil, err
	}
	options := []frontseat.Option{instrumented}
	if token != "" {
		options = append(options, frontseat.WithToken(token))
	}
	return frontseat.New(at, options...)
}

// asked is the workspace a command asks: inside a workspace's sandbox,
// that workspace, whose daemon is on the loopback, and no other; else
// the one --workspace names, of the control plane; else the one last
// opened, where it was opened of the control plane asked now.
func asked() (*frontseat.Workspace, error) {
	if inside() {
		if workspaceFlag != "" {
			return nil, errors.New("inside a workspace's sandbox the command line asks this workspace's daemon, and no other workspace: --workspace is given outside")
		}
		daemon, err := frontseat.New(here, instrumented)
		if err != nil {
			return nil, err
		}
		return daemon.Here(), nil
	}
	fs, err := server()
	if err != nil {
		return nil, err
	}
	if workspaceFlag != "" {
		return fs.Workspace(workspaceFlag), nil
	}
	held, found, err := machine.ReadContext()
	if err != nil {
		return nil, err
	}
	if found && held.Workspace != "" && held.Server != fs.Server() {
		return nil, fmt.Errorf("the workspace last opened, %s, is of the control plane at %s, and the command line asks the one at %s (frontseat --help): open one there, or name one with --workspace", held.Workspace, held.Server, fs.Server())
	}
	if !found || held.Workspace == "" {
		return nil, errors.New("there is no workspace to talk to: open one with `frontseat workspace open`, or name one with --workspace")
	}
	return fs.Workspace(held.Workspace), nil
}

// workspaceFlag is the workspace a command asks, where a command is told it.
var workspaceFlag string

// out is where a command prints what it says.
var out io.Writer = os.Stdout

// errOut is where a command says what went wrong of what it was asked
// to do, and warns: never where what was asked for is printed, which
// another program may read.
var errOut io.Writer = os.Stderr

// exitCode is what the command ends with where it ran and ended: a
// build's exit code, a release that failed, a command over SSH.
var exitCode int

// rootCmd is frontseat itself: every command is beneath it.
var rootCmd = &cobra.Command{
	Use:   "frontseat",
	Short: "Build, test and release what a workspace holds",
	Long: `frontseat asks a workspace, which runs in a sandbox, to build, test,
run and release what it holds. Every action runs on a build grid.

The server: every command asks one control plane, the one
~/.config/frontseat/local/server names (beneath $XDG_CONFIG_HOME where
it is set), or else the local stack's, http://127.0.0.1:7700. The token
beside it, ~/.config/frontseat/local/token, is presented to that control
plane and to no other address. A control plane on another machine is
named with https, and is asked over nothing else.

The workspace: a command that asks a workspace takes --workspace NAME.
Without it, it asks the workspace last opened.

Inside a workspace's sandbox, a command asks that workspace's daemon,
on the loopback, to build, run and release, and no control plane: there
the workspace's source is fixed, and the workspace commands are refused.

` + jsonHelp + `

` + exitCodes,
	// A mistake in how a command is written is said once, by main, and
	// the usage is asked for with --help.
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the command the arguments name, and returns the exit code
// it ends with, and why it failed where it did (Failed says it).
func Execute(ctx context.Context, args []string) (int, error) {
	exitCode = 0
	// --version says what frontseat version says.
	rootCmd.Version = version
	rootCmd.SetVersionTemplate(released() + "\n")
	fresh(rootCmd, ctx)
	rootCmd.SetArgs(args)
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		code, _ := Failed(ctx, err)
		return code, err
	}
	return exitCode, nil
}

// unanswered says, of a server that does not answer, which it is and
// what starts it, as far as the command line knows: the local stack is
// started with the stack's own program, which a checkout of Frontseat
// runs with mise.
func unanswered() string {
	if inside() {
		return "this workspace's daemon does not answer"
	}
	if at, err := machine.Server(); err == nil && at != machine.Stack {
		return "the control plane frontseat/local/server names does not answer"
	}
	return "the local stack does not answer: `frontseat-local up` starts it (in a checkout of Frontseat, `mise start`)"
}

// Name is the command the arguments name, as it is said of in a trace:
// "build", "build events", "workspace ssh". It never holds an argument, a
// source location or shell text. What names no command is "unknown", and
// nothing at all is "help".
func Name(args []string) string {
	found, _, err := rootCmd.Find(args)
	switch {
	case err != nil:
		return "unknown"
	case found != rootCmd:
		return strings.TrimPrefix(found.CommandPath(), rootCmd.Name()+" ")
	}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return "unknown"
		}
	}
	return "help"
}

func init() {
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
	// The commands cobra adds, made when the others are, so that each is
	// found by its name before the command line is run.
	rootCmd.InitDefaultHelpCmd()
	rootCmd.InitDefaultCompletionCmd()
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		// frontseat alone says what there is, and is no mistake.
		if len(args) == 0 {
			return cmd.Help()
		}
		return named("a command")(cmd, args)
	}
	rootCmd.Args = cobra.ArbitraryArgs
	rootCmd.SuggestionsMinimumDistance = 2
	rootCmd.PersistentFlags().BoolVar(&asJSON, "json", false, "write what is printed as JSON, one message of the API to a line, for a program to read")
}

// named refuses a command that is a group of others and was asked for
// by itself, or with a name none of them has: it says what there is, and
// the command line fails, so that a name written wrongly in a pipeline
// does not pass for a command that ran.
func named(what string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		var names []string
		for _, sub := range cmd.Commands() {
			if sub.IsAvailableCommand() {
				names = append(names, sub.Name())
			}
		}
		if len(args) > 0 {
			meant := ""
			if near := cmd.SuggestionsFor(args[0]); len(near) > 0 {
				meant = "; did you mean " + strings.Join(near, " or ") + "?"
			}
			return fmt.Errorf("%q is not %s; there are %s%s", args[0], what, strings.Join(names, ", "), meant)
		}
		return fmt.Errorf("name %s: %s", what, strings.Join(names, ", "))
	}
}

// asksAWorkspace gives a command the flag that says which workspace it
// asks.
func asksAWorkspace(cmd *cobra.Command) {
	cmd.Flags().StringVar(&workspaceFlag, "workspace", "", "the workspace to ask, by its name; without it, the one last opened (inside a workspace's sandbox, the one it runs in, and no other)")
}

// fresh makes every command as it was made: its flags as they are by
// default, and its context the one given. A process runs one command
// line, and a test many; each begins alike.
func fresh(cmd *cobra.Command, ctx context.Context) {
	cmd.SetContext(ctx)
	reset := func(f *pflag.Flag) {
		if list, ok := f.Value.(pflag.SliceValue); ok {
			_ = list.Replace(nil)
		} else {
			_ = f.Value.Set(f.DefValue)
		}
		f.Changed = false
	}
	cmd.Flags().VisitAll(reset)
	cmd.PersistentFlags().VisitAll(reset)
	// Where -- was is forgotten too: Init keeps the flags, and forgets it.
	cmd.Flags().Init(cmd.Flags().Name(), pflag.ContinueOnError)
	for _, sub := range cmd.Commands() {
		fresh(sub, ctx)
	}
}

// said prints a line a command says.
func said(format string, a ...any) { fmt.Fprintf(out, format+"\n", a...) }

// complained prints a line of what went wrong, or a warning, apart from
// what the command says.
func complained(format string, a ...any) { fmt.Fprintf(errOut, format+"\n", a...) }

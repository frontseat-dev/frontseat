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
	"net/netip"
	"net/url"
	"os"
	"strings"

	"connectrpc.com/connect/v2"
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
	if in, err := inside(); err != nil || in {
		return nil, errors.Join(err, refusedInside(in))
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
	return frontseat.New(at, options...), nil
}

// refusedInside is the refusal of a command inside a workspace's
// sandbox, where it is refused; nil outside.
func refusedInside(in bool) error {
	if in {
		return fixed
	}
	return nil
}

// asked is the workspace a command asks: inside a workspace's sandbox,
// that workspace, whose daemon is on the loopback, and no other; else
// the one --workspace names, of the control plane; else the one last
// opened, where it was opened of the control plane asked now.
func asked() (*frontseat.Workspace, error) {
	in, err := inside()
	if err != nil {
		return nil, err
	}
	if in {
		if workspaceFlag != "" {
			return nil, errors.New("inside a workspace's sandbox the command line asks this workspace's daemon, and no other workspace: --workspace is given outside")
		}
		return frontseat.New(here, instrumented).Here(), nil
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

// local is whether an address is of this machine: a loopback address,
// or localhost.
func local(at string) bool {
	parsed, err := url.Parse(at)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return true
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.IsLoopback()
}

// workspaceFlag is the workspace a command asks, where a command is told it.
var workspaceFlag string

// out is where a command prints what it says.
var out io.Writer = os.Stdout

// exitCode is what the command ends with where it ran and ended: a
// build's exit code, a release that failed, a command over SSH.
var exitCode int

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "frontseat",
	Short: "Build, test and release what a workspace holds",
	Long: `frontseat asks a workspace, which runs in a sandbox, to build, test,
run and release what it holds. Every action runs on a build grid.

The server: every command asks one control plane, the one
~/.config/frontseat/local/server names (beneath $XDG_CONFIG_HOME where
it is set), or else the local stack's, http://127.0.0.1:7700. The token
beside it, ~/.config/frontseat/local/token, is presented to that control
plane and to no other address.

The workspace: a command that asks a workspace takes --workspace NAME.
Without it, it asks the workspace last opened.

Inside a workspace's sandbox, a command asks that workspace's daemon,
on the loopback, to build, run and release, and no control plane: there
the workspace's source is fixed, and the workspace commands are refused.`,
	// A mistake in how a command is written is said once, by main, and
	// the usage is asked for with --help.
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the command the arguments name, and returns the exit code
// it ends with, or why it could not run.
func Execute(ctx context.Context, args []string) (int, error) {
	exitCode = 0
	fresh(rootCmd, ctx)
	rootCmd.SetArgs(args)
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		if connect.CodeOf(err) == connect.CodeUnavailable {
			return 0, fmt.Errorf("%w\nthe server does not answer: where it is the local stack's, `mise start` starts it", err)
		}
		return 0, err
	}
	return exitCode, nil
}

// Name is the command the arguments name, as it is said of in a trace:
// "build", "ship plan", "workspace ssh". It never holds an argument, a
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
	rootCmd.RunE = named("a command")
	rootCmd.Args = cobra.ArbitraryArgs
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
			return fmt.Errorf("%q is not %s; there are %s", args[0], what, strings.Join(names, ", "))
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
	for _, sub := range cmd.Commands() {
		fresh(sub, ctx)
	}
}

// said prints a line a command says.
func said(format string, a ...any) { fmt.Fprintf(out, format+"\n", a...) }

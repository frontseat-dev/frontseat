// Package cmd is the frontseat command line: one command to a file, as
// cobra-cli lays an application out. A workspace command asks the
// control plane; a command that builds asks the build daemon of where
// it is run, which is this program again (daemon.go), started by the
// command line for itself and holding the build's state. What a command
// prints is plain lines, in a terminal or not.
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
	"github.com/frontseat-dev/frontseat/core/telemetry"
	"github.com/frontseat-dev/frontseat/sdk/go"
)

// instrumented is how every command reaches a server: through the
// command's own telemetry, so that each call is part of its trace and
// measured with it. The SDK takes it as an option and imports none of
// it.
var instrumented = frontseat.WithHTTPClient(&http.Client{Transport: telemetry.Transport{}})

// inside says whether the command line runs inside a workspace's
// sandbox (machine.Inside); a test stands in for it.
var inside = machine.Inside

// fixed is what a command that would change or describe a workspace is
// refused with inside a workspace's sandbox: there its source is fixed,
// and the command line builds, runs and releases what it holds alone.
var fixed = errors.New("inside a workspace's sandbox its source is fixed: workspaces are opened, filled, described, listed, closed and ended from outside, by their names, and here the command line builds, runs and releases what this workspace holds alone")

// unasked is what a command that asks the control plane anything else
// is refused with inside a workspace's sandbox: none is asked there.
var unasked = errors.New("inside a workspace's sandbox no control plane is asked: what is asked of one is asked from outside")

// server is the client of the control plane a command asks: the one
// frontseat/local/server names, or the local stack's, presented the
// token laid out beside it, and that token presented to it alone. Only
// a command that asks the control plane calls it: the token is read by
// nothing else. Inside a workspace's sandbox none is asked.
func server() (*frontseat.Client, error) {
	if inside() {
		return nil, unasked
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

// selection is the workspace selected on this machine, the one last
// opened: its name, empty where none is; the control plane that keeps
// it; the one the command line asks now; and whether they are the same
// (machine.SamePlane). One of another control plane is none of this
// one's: a workspace command that takes it is told so, and to what
// builds it counts as none.
type selection struct {
	name, plane, asked string
	here               bool
}

// selected reads the selection. It asks no control plane and reads no
// token; with none selected it reads nothing of the control plane's at
// all, so that a command that builds this machine's directory fails on
// none of its files. With one, the address the command line asks is
// read to compare.
func selected() (selection, error) {
	held, found, err := machine.ReadContext()
	if err != nil || !found {
		return selection{}, err
	}
	at, err := machine.Server()
	if err != nil {
		return selection{}, err
	}
	return selection{name: held.Workspace, plane: held.Server, asked: at, here: machine.SamePlane(held.Server, at)}, nil
}

// errNoWorkspace is a workspace command told no name where no workspace
// is selected: it says what opens one, and that the command takes a
// name too (subject).
var errNoWorkspace = errors.New("there is no workspace to talk to: open one with `frontseat workspace open`")

// directory says which directory a command run here builds, and builder
// finds or starts the build daemon of one; a test stands in for each.
var (
	directory = machine.Directory
	builder   = machine.BuildDaemon
)

// daemonAsked is the build daemon a command asked: the directory it is
// of, the file that holds all it printed, and whether it is the one of
// a workspace, inside its sandbox.
type daemonAsked struct {
	directory, log string
	within         bool
}

// The one command a process runs: whether it is one that works where it
// is run (worksWhereItIsRun), and the build daemon it asked, nil where
// it asked none: what it asks otherwise is the control plane. What is
// said of a server that did not answer, or refused who asked, is said
// of the one that was asked (Failed).
var (
	marked  bool
	reached *daemonAsked
)

// building is what a command that builds, tests, runs, queries or
// releases asks, where it is not refused (worksWhereItIsRun): the build
// daemon of the directory the command is run in, which the command line
// starts for itself, here as inside a workspace's sandbox. Outside, that
// is where this machine was told its grid (build.yaml); inside, it is
// the workspace's directory, told what the sandbox's supervisor wrote of
// the grid the control plane named. That daemon is asked on the loopback
// with its own token, and no control plane is. A command that was not
// made one that works where it is run is given none: it would build
// this machine's directory while a workspace is selected.
func building() (*frontseat.Workspace, error) {
	if !marked {
		return nil, errors.New("this command asks for the build daemon of where it is run, and was not made one that works where it is run (worksWhereItIsRun): it is a defect of the command line")
	}
	within := inside()
	built, err := directory(within)
	if err != nil {
		return nil, err
	}
	found, err := builder(context.Background(), built, within)
	switch {
	case errors.Is(err, machine.ErrNoSettings) && within:
		return nil, fmt.Errorf("this workspace's sandbox was told no grid to build on: its supervisor wrote no build settings, which the control plane that made the sandbox tells it")
	case errors.Is(err, machine.ErrNoSettings):
		return nil, fmt.Errorf("there is no workspace to talk to, and this machine was told no grid to build %s on: to build it here, say the grid in %s; to work in a workspace, open one (frontseat workspace open) and build in it (frontseat workspace ssh)", built, places().Settings)
	}
	if err != nil {
		return nil, err
	}
	daemon, err := frontseat.New("http://"+found.Address, instrumented, frontseat.WithToken(found.Token))
	if err != nil {
		return nil, err
	}
	reached = &daemonAsked{directory: built, log: found.Log, within: within}
	return daemon.Here(), nil
}

// places is the files that tell the command line, as they are named on
// this machine (machine.Where), for what is said to a person; where
// this machine says no home to find them in, as they are named beneath
// frontseat's configuration directory.
func places() machine.Places {
	if named, err := machine.Where(); err == nil {
		return named
	}
	return machine.Places{Server: "frontseat/local/server", Token: "frontseat/local/token", Settings: "frontseat/build.yaml"}
}

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

The workspace: build, test, run, query and ship work on what is where
they are run, through a build daemon frontseat starts for itself, on
the loopback, and ask no control plane. Inside a workspace's sandbox
that is the workspace, on the grid its control plane named: there the
workspace's source is fixed, and the workspace commands are refused. A
workspace is built from inside it: open a shell
there with frontseat workspace ssh, or run one command there with
frontseat workspace ssh -- frontseat build.

Outside a sandbox, with no workspace open, build, test, run, query and
ship work on the directory they are run in, the repository or workspace
it is part of: on a machine told the grid it builds on, in
~/.config/frontseat/build.yaml. Opening a workspace selects it on this
machine. While one is selected, the one last opened of the control
plane asked, they are refused and say to run them in it. frontseat
workspace close forgets the selection here, and they work on this
machine's directory again; the workspace goes on, and frontseat
workspace delete ends it. A workspace last opened of another control
plane is none selected here.

` + exitCodes,
	// A mistake in how a command is written is said once, by main, and
	// the usage is asked for with --help.
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the command the arguments name, and returns the exit code
// it ends with, and why it failed where it did (Failed says it).
func Execute(ctx context.Context, args []string) (int, error) {
	exitCode, marked, reached = 0, false, nil
	fresh(rootCmd, ctx)
	rootCmd.SetArgs(args)
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		code, _ := Failed(ctx, err)
		return code, err
	}
	return exitCode, nil
}

// unanswered says, of a server that did not answer, which it was and
// what to do, as far as the command line knows: the build daemon the
// command asked, with where all it printed is, or else the control
// plane, by the file that names it on this machine. The local stack is
// started with the stack's own program, which a checkout of Frontseat
// runs with mise.
func unanswered() string {
	switch {
	case reached != nil && reached.within:
		return fmt.Sprintf("it was the build daemon of this workspace that was asked, which the command line started here for %s: all it said is in %s, and one that is gone is started again by the next command", reached.directory, reached.log)
	case reached != nil:
		return fmt.Sprintf("it was the build daemon of %s that was asked, which the command line started on this machine, and no control plane: all it said is in %s, and one that is gone is started again by the next command", reached.directory, reached.log)
	case inside():
		return "the build daemon of this workspace does not answer"
	}
	if at, err := machine.Server(); err == nil && at != machine.Stack {
		return fmt.Sprintf("the control plane %s names does not answer", places().Server)
	}
	return "the local stack does not answer: `frontseat-stack up` starts it (in a checkout of Frontseat, `mise start`)"
}

// unserving is the server that does not serve what the command asked
// of it: the build daemon it asked, or else the control plane.
func unserving() string {
	if reached != nil {
		return "the build daemon of " + reached.directory
	}
	return "this control plane"
}

// unauthorized says, of a server that refused who asked, whose token it
// refused and where that token is: a build daemon's own, which it says
// in its state directory and the command line read there, or the one
// laid out for the control plane, by its file on this machine. It never
// says a token.
func unauthorized() string {
	if reached != nil {
		return fmt.Sprintf("it was the build daemon of %s that refused, of the token it wrote itself beside %s: it was replaced since the command found it, and the command run again asks the one there is", reached.directory, reached.log)
	}
	return fmt.Sprintf("the control plane refused your token: it is %s, beside the server it is for", places().Token)
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
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		// frontseat alone says what there is, and is no mistake.
		if len(args) == 0 {
			return cmd.Help()
		}
		return named("a command")(cmd, args)
	}
	rootCmd.Args = cobra.ArbitraryArgs
	rootCmd.SuggestionsMinimumDistance = 2
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

// whereItIsRun marks a command that builds, tests, runs, queries or
// releases what is where it is run (worksWhereItIsRun).
const whereItIsRun = "frontseat/where-it-is-run"

// worksWhereItIsRun makes a command one that builds, tests, runs,
// queries or releases what is where it is run: inside a workspace's
// sandbox, that workspace, and outside, the directory (building).
// Outside, while a workspace is selected, the one last opened of the
// control plane asked now, it is refused before anything of it is run:
// a workspace is built from inside it, and the refusal says how, by the
// command's own name and nothing it was given, and what forgets the
// selection. One of another control plane is none selected here. It is
// told of a command once the command is whole.
func worksWhereItIsRun(cmd *cobra.Command) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[whereItIsRun] = "true"
	run := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		marked = true
		if inside() {
			return run(cmd, args)
		}
		chosen, err := selected()
		if err != nil {
			return err
		}
		if chosen.name != "" && chosen.here {
			return fmt.Errorf("the workspace %s is open, and what builds, tests, runs, queries or releases it is run in it: in a shell there (frontseat workspace ssh), or as one command (frontseat workspace ssh -- %s); or forget it here, to work on this machine's directory again: frontseat workspace close", chosen.name, cmd.CommandPath())
		}
		return run(cmd, args)
	}
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

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
)

// counted is a control plane that keeps the workspace "named", and how
// often it was asked anything.
func counted(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	asked := &atomic.Int32{}
	return counting(t, asked, func(api *connect.Server) {
		workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, keeping{})
	}), asked
}

// noDaemon has a command that asks for the build daemon of its
// directory fail, saying so, and counts how often one did.
func noDaemon(t *testing.T) *int {
	t.Helper()
	// A command with no workspace selected builds the repository it is
	// run in, the nearest above it: the test stands in one of its own,
	// and finds none by where it happens to be run.
	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "frontseat.yaml"), []byte("plugins: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repository)
	asked := new(int)
	before := builder
	t.Cleanup(func() { builder = before })
	builder = func(context.Context, string, bool) (machine.Builder, error) {
		*asked++
		return machine.Builder{}, errors.New("the build daemon of the directory was asked for")
	}
	return asked
}

// noShell has the test fail where a command starts the SSH client: the
// one found here is the test's own, which says that it was started.
func noShell(t *testing.T) (started func() bool) {
	t.Helper()
	client := t.TempDir()
	mark := filepath.Join(client, "started")
	if err := os.WriteFile(filepath.Join(client, "ssh"), []byte("#!/bin/sh\n: > '"+mark+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", client+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() bool {
		_, err := os.Stat(mark)
		return err == nil
	}
}

// worked is every command that builds, tests, runs, queries or releases
// what is where it is run, each as it is written with what it takes: a
// word a refusal must not say again wherever the command takes one.
var worked = []struct{ words, given []string }{
	{[]string{"build"}, []string{"secret-word", "--only", "secret-word"}},
	{[]string{"build", "list"}, nil},
	{[]string{"build", "describe"}, []string{"secret-word"}},
	{[]string{"build", "events"}, []string{"secret-word", "--from", "7"}},
	{[]string{"build", "actions"}, []string{"secret-word"}},
	{[]string{"build", "logs"}, []string{"secret-word", "//secret-word:go.lint"}},
	{[]string{"build", "cancel"}, []string{"secret-word"}},
	{[]string{"test"}, []string{"secret-word", "--only", "secret-word"}},
	{[]string{"run"}, []string{"//secret-word:go.run/greet", "--", "secret-word"}},
	{[]string{"run", "list"}, nil},
	{[]string{"run", "describe"}, []string{"secret-word"}},
	{[]string{"run", "logs"}, []string{"secret-word"}},
	{[]string{"run", "cancel"}, []string{"secret-word"}},
	{[]string{"query"}, []string{"secret-word", "--why", "secret-word", "--json"}},
	{[]string{"ship"}, []string{"secret-word", "--plan=secret-word"}},
	{[]string{"ship", "plan"}, []string{"secret-word"}},
}

// A workspace that is open is built from inside it. Outside a sandbox,
// with a workspace last opened of the control plane asked, every command
// that builds, tests, runs, queries or releases is refused by name and
// runs nothing: it names the workspace and how the command is run in it,
// a shell there or the one command, by the command's own words and
// nothing it was given. The control plane is asked nothing, no build
// daemon of the directory is asked for, and no SSH client is started.
// The refusal says what forgets the selection too (workspace close).
// The controls: with no workspace selected, the directory's daemon is
// asked for, and so it is where the workspace last opened is of another
// control plane, which is none selected here; inside a sandbox, the
// daemon there is asked; and a workspace command asks the control plane
// of the workspace last opened, and is told whose it is where it is of
// another.
func TestAWorkspaceThatIsOpenIsBuiltFromInsideIt(t *testing.T) {
	plane, asked := counted(t)
	serverAt(t, plane)
	daemons := noDaemon(t)
	shelled := noShell(t)
	// Every command that works where it is run is among them.
	tried := map[string]bool{}
	t.Cleanup(func() {
		var each func(*cobra.Command)
		each = func(cmd *cobra.Command) {
			if name := strings.TrimPrefix(cmd.CommandPath(), "frontseat "); cmd.Annotations[whereItIsRun] != "" && !tried[name] {
				t.Errorf("%s works where it is run, and was not tried here", name)
			}
			for _, sub := range cmd.Commands() {
				each(sub)
			}
		}
		each(rootCmd)
	})
	lastOpened(t, plane, "named")
	for _, command := range worked {
		words := strings.Join(command.words, " ")
		tried[words] = true
		printed, code, err := runAt(t, context.Background(), "", slices.Concat(command.words, command.given)...)
		if err == nil || code != ExitUsage || printed != "" {
			t.Errorf("%s, outside a workspace that is open, ended %d (%v), printing %q; want it refused, exit %d, and nothing printed", words, code, err, printed, ExitUsage)
			continue
		}
		for _, said := range []string{"the workspace named is open", "(frontseat workspace ssh)", "(frontseat workspace ssh -- frontseat " + words + ")", "or forget it here, to work on this machine's directory again: frontseat workspace close"} {
			if !strings.Contains(err.Error(), said) {
				t.Errorf("%s is refused with %q, which does not say %q", words, err, said)
			}
		}
		if strings.Contains(err.Error(), "secret-word") {
			t.Errorf("%s is refused with what it was given: %q", words, err)
		}
	}
	if asked.Load() != 0 || *daemons != 0 || shelled() {
		t.Errorf("the control plane was asked %d times, the directory's daemon %d, and an SSH client started: %v; want nothing asked, and none started", asked.Load(), *daemons, shelled())
	}

	// A workspace command asks the control plane of the workspace last
	// opened.
	if printed, _, err := runAt(t, context.Background(), "", "workspace", "describe"); err != nil || printed != "named  ready\n" || asked.Load() != 1 {
		t.Errorf("workspace describe printed %q (%v), the control plane asked %d times", printed, err, asked.Load())
	}
	// Inside a sandbox, the daemon there is asked.
	d := &daemonOf{}
	if _, code, err := run(t, d, "ship", "plan", "greet"); err != nil || code != 0 || len(d.planned) != 1 {
		t.Errorf("inside a sandbox, a plan ended %d (%v), its daemon asked %d times", code, err, len(d.planned))
	}
	// The workspace last opened of another control plane is none of
	// this one's: to a command that builds, none is selected here, and
	// it works on its directory; a workspace command is told whose it
	// is, and that it takes a name.
	lastOpened(t, "http://127.0.0.1:1", "named")
	if _, _, err := runAt(t, context.Background(), "", "build"); err == nil || !strings.Contains(err.Error(), "the build daemon of the directory was asked for") || *daemons != 1 {
		t.Errorf("a build, the workspace last opened being of another control plane: %v, the directory's daemon asked for %d times; want it asked for once", err, *daemons)
	}
	if _, _, err := runAt(t, context.Background(), "", "workspace", "describe"); err == nil || !strings.Contains(err.Error(), "the workspace last opened, named, is of the control plane at http://127.0.0.1:1") || !strings.Contains(err.Error(), "open one there, or name one") {
		t.Errorf("a workspace command, of the workspace last opened of another control plane: %v, want it refused, and told it takes a name", err)
	}
	if *daemons != 1 {
		t.Errorf("the directory's daemon was asked for %d times, want once, by the build", *daemons)
	}
	// With no workspace selected, the directory's daemon is asked for.
	noneOpened(t)
	if _, _, err := runAt(t, context.Background(), "", "build", "list"); err == nil || !strings.Contains(err.Error(), "the build daemon of the directory was asked for") || *daemons != 2 {
		t.Errorf("with no workspace selected: %v, the directory's daemon asked for %d times; want it asked for once more", err, *daemons)
	}
	if asked.Load() != 1 || shelled() {
		t.Errorf("the controls asked the control plane %d times, and an SSH client was started: %v; want once, by workspace describe, and none", asked.Load(), shelled())
	}
}

// No command that builds, tests, runs, queries or releases takes a
// workspace by a flag: it works on what is where it is run, and
// --workspace is a flag there is none of, outside a sandbox and inside
// one, where nothing is asked. A command that asks a workspace with
// none selected says how one is opened, and that it takes a name only
// where it does. The control is the same command without the flag,
// inside, which its daemon is asked.
func TestNoCommandThatBuildsTakesAWorkspace(t *testing.T) {
	plane, asked := counted(t)
	serverAt(t, plane)
	daemons := noDaemon(t)
	lastOpened(t, plane, "named")
	d := &daemonOf{}
	for _, command := range worked {
		line := slices.Concat(command.words, []string{"--workspace", "named"}, command.given)
		for where, ran := range map[string]func() (string, int, error){
			"outside": func() (string, int, error) { return runAt(t, context.Background(), "", line...) },
			"inside":  func() (string, int, error) { return run(t, d, line...) },
		} {
			if _, code, err := ran(); err == nil || code != ExitUsage || !strings.Contains(err.Error(), "unknown flag: --workspace") {
				t.Errorf("%s, %v ended %d (%v); want --workspace a flag there is none of", where, line, code, err)
			}
		}
	}
	// Whatever the call: the daemon inside is asked nothing at all.
	if asked.Load() != 0 || *daemons != 0 || d.asked.Load() != 0 {
		t.Errorf("the control plane was asked %d times, the directory's daemon %d and the sandbox's %d; want nothing asked", asked.Load(), *daemons, d.asked.Load())
	}
	if _, _, err := run(t, d, "ship", "plan", "greet"); err != nil || len(d.planned) != 1 || d.asked.Load() != 1 {
		t.Errorf("inside, without the flag, the daemon there was asked %d times, %d of them for a plan (%v)", d.asked.Load(), len(d.planned), err)
	}
	// With none selected, on a machine told nothing of its grid: it says
	// how this machine is told, in the file as it is named here, and
	// where a workspace is built, and takes no name.
	noneOpened(t)
	builder = func(context.Context, string, bool) (machine.Builder, error) {
		return machine.Builder{}, machine.ErrNoSettings
	}
	settings := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "frontseat", "build.yaml")
	if _, _, err := runAt(t, context.Background(), "", "build"); err == nil || !strings.Contains(err.Error(), "there is no workspace to talk to, and this machine was told no grid to build") || !strings.Contains(err.Error(), "to build it here, say the grid in "+settings+"; ") || !strings.Contains(err.Error(), "to work in a workspace, open one (frontseat workspace open) and build in it (frontseat workspace ssh)") || strings.Contains(err.Error(), "name one") || strings.Contains(err.Error(), "open one with") {
		t.Errorf("a build with no workspace selected, on a machine told no grid: %v; want how it is told, where a workspace is built, and no name taken", err)
	}
	if _, _, err := runAt(t, context.Background(), "", "workspace", "describe"); err == nil || !strings.Contains(err.Error(), "open one with `frontseat workspace open`, or name one, as this command takes a workspace's NAME") {
		t.Errorf("workspace describe with no workspace selected: %v; want how one is opened, and that it takes a name", err)
	}
}

// A workspace is selected where it was last opened of the control plane
// asked now, and that is the same control plane where it is the same
// origin, however it is written: the host in another case, a scheme's
// own port written out. Two names of this machine are two control
// planes, since nothing is resolved: the control, where a command that
// builds works on its directory.
func TestAWorkspaceIsSelectedWhereItsControlPlaneIsTheOriginAsked(t *testing.T) {
	kept := serverAt(t, "http://LocalHost:80")
	daemons := noDaemon(t)
	asks := func(server string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(kept, "server"), []byte(server+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, same := range [][2]string{
		{"http://LocalHost:80", "http://localhost"},
		{"http://localhost", "http://LOCALHOST:80"},
		{"https://frontseat.example", "https://FrontSeat.Example:443"},
		{"http://127.0.0.1:7700", "http://127.0.0.1:7700"},
	} {
		asks(same[0])
		lastOpened(t, same[1], "named")
		if _, code, err := runAt(t, context.Background(), "", "build", "list"); err == nil || code != ExitUsage || !strings.Contains(err.Error(), "the workspace named is open") {
			t.Errorf("asking %s, with the workspace last opened of %s, a build ended %d (%v); want it refused, the workspace being selected", same[0], same[1], code, err)
		}
	}
	if *daemons != 0 {
		t.Fatalf("the directory's daemon was asked for %d times by what was refused", *daemons)
	}
	for i, other := range [][2]string{
		{"http://localhost:7700", "http://127.0.0.1:7700"},
		{"http://127.0.0.1:7700", "http://127.0.0.1:7701"},
		{"https://frontseat.example", "https://frontseat.example:80"},
		{"https://localhost", "http://localhost"},
	} {
		asks(other[0])
		lastOpened(t, other[1], "named")
		if _, _, err := runAt(t, context.Background(), "", "build", "list"); err == nil || !strings.Contains(err.Error(), "the build daemon of the directory was asked for") || *daemons != i+1 {
			t.Errorf("the control: asking %s, with the workspace last opened of %s: %v, after %d daemons asked for; want the directory's asked for", other[0], other[1], err, *daemons)
		}
	}
}

// A command that builds this machine's directory asks no control plane,
// and reads nothing of one's files: with no workspace selected, a token
// another could read and a server file that names no control plane fail
// no build, test, run, query or release. With a workspace last opened,
// the address asked is read, to say whether it is selected, and never
// the token. The controls are a workspace command, which each of those
// files fails, and a server file that names no control plane where a
// workspace was last opened, which fails the build too: whose it is
// cannot be said.
func TestABuildOfThisMachinesDirectoryReadsNothingOfTheControlPlanes(t *testing.T) {
	plane, asked := counted(t)
	token := layOut(t, plane)
	daemons := noDaemon(t)
	if err := os.Chmod(token, 0o644); err != nil {
		t.Fatal(err)
	}
	named := filepath.Join(filepath.Dir(token), "server")
	for i, command := range worked {
		if _, _, err := runAt(t, context.Background(), "", slices.Concat(command.words, command.given)...); err == nil || !strings.Contains(err.Error(), "the build daemon of the directory was asked for") || *daemons != i+1 {
			t.Fatalf("with a token another could read, %v: %v, after %d daemons asked for; want the directory's asked for", command.words, err, *daemons)
		}
	}
	if _, _, err := runAt(t, context.Background(), "", "workspace", "list"); err == nil || !strings.Contains(err.Error(), "can be read by others") {
		t.Errorf("the control: workspace list with a token another could read: %v, want it refused", err)
	}
	// A workspace last opened of this control plane: selected, by the
	// address alone.
	lastOpened(t, plane, "named")
	if _, _, err := runAt(t, context.Background(), "", "build"); err == nil || !strings.Contains(err.Error(), "the workspace named is open") {
		t.Errorf("with a token another could read and a workspace selected, a build: %v; want it refused as built in the workspace", err)
	}
	if _, _, err := runAt(t, context.Background(), "", "workspace", "describe"); err == nil || !strings.Contains(err.Error(), "can be read by others") {
		t.Errorf("the control: workspace describe with a token another could read: %v, want it refused", err)
	}
	// A server file that names no control plane.
	if err := os.WriteFile(named, []byte("no address at all\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := *daemons
	if _, _, err := runAt(t, context.Background(), "", "build"); err == nil || !strings.Contains(err.Error(), "names no control plane") || *daemons != before {
		t.Errorf("the control: with a workspace last opened and a server file that names none, a build: %v, after %d more daemons asked for; want it refused by the file", err, *daemons-before)
	}
	noneOpened(t)
	if _, _, err := runAt(t, context.Background(), "", "build"); err == nil || !strings.Contains(err.Error(), "the build daemon of the directory was asked for") || *daemons != before+1 {
		t.Errorf("with none selected and a server file that names none, a build: %v; want the directory's daemon asked for", err)
	}
	if _, _, err := runAt(t, context.Background(), "", "workspace", "list"); err == nil || !strings.Contains(err.Error(), "names no control plane") {
		t.Errorf("the control: workspace list with a server file that names none: %v, want it refused", err)
	}
	if asked.Load() != 0 {
		t.Errorf("the control plane was asked %d times; want nothing asked", asked.Load())
	}
}

// Only a command made one that works where it is run (worksWhereItIsRun)
// is given the build daemon of where it is run: one that was not, which
// would build this machine's directory while a workspace is selected,
// is given none and says so, whatever is selected. The control is the
// same command made one: with none selected it asks for the daemon, and
// with a workspace selected it is refused as every other.
func TestOnlyACommandThatWorksWhereItIsRunIsGivenTheDirectorysDaemon(t *testing.T) {
	plane, _ := counted(t)
	serverAt(t, plane)
	daemons := noDaemon(t)
	made := &cobra.Command{Use: "made-for-the-test", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		_, err := building()
		return err
	}}
	rootCmd.AddCommand(made)
	t.Cleanup(func() { rootCmd.RemoveCommand(made) })
	for _, open := range []bool{false, true} {
		noneOpened(t)
		if open {
			lastOpened(t, plane, "named")
		}
		// After a command that works where it is run, as a process never is.
		_, _, _ = runAt(t, context.Background(), "", "build", "list")
		before := *daemons
		if _, code, err := runAt(t, context.Background(), "", "made-for-the-test"); err == nil || code != ExitUsage || !strings.Contains(err.Error(), "was not made one that works where it is run") || *daemons != before {
			t.Errorf("with a workspace selected: %t, a command not made one that works where it is run ended %d (%v), after %d daemons asked for; want it given none", open, code, err, *daemons-before)
		}
	}
	worksWhereItIsRun(made)
	if _, _, err := runAt(t, context.Background(), "", "made-for-the-test"); err == nil || !strings.Contains(err.Error(), "the workspace named is open") || !strings.Contains(err.Error(), "frontseat workspace ssh -- frontseat made-for-the-test") {
		t.Errorf("the control: made one, with a workspace selected: %v; want it refused as built in the workspace", err)
	}
	noneOpened(t)
	before := *daemons
	if _, _, err := runAt(t, context.Background(), "", "made-for-the-test"); err == nil || !strings.Contains(err.Error(), "the build daemon of the directory was asked for") || *daemons != before+1 {
		t.Errorf("the control: made one, with none selected: %v, after %d daemons asked for; want the directory's asked for", err, *daemons-before)
	}
}

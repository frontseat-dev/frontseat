package cmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
)

// boxes stands in for the workspaces' shells: of which workspace each
// command was run in the sandbox, what the SSH client was given to run
// there, and how it ended.
type boxes struct {
	workspaces []string
	given      [][]string
	terminals  int
	exit       int
}

// standingIn has the test's commands run in boxes, and in no sandbox.
func standingIn(t *testing.T) *boxes {
	t.Helper()
	b := &boxes{}
	before := inBox
	t.Cleanup(func() { inBox = before })
	inBox = func(_ context.Context, workspace string, terminal bool, arguments []string) error {
		b.workspaces, b.given = append(b.workspaces, workspace), append(b.given, arguments)
		if terminal {
			b.terminals++
		}
		exitCode = b.exit
		return nil
	}
	return b
}

// unboxed is what the tests' commands are run in a sandbox with, unless a
// test stands in (standingIn): no test opens a shell of this machine's.
func unboxed(context.Context, string, bool, []string) error {
	return errors.New("a test ran a command in a workspace's sandbox, and stood in for none")
}

// arriving is what a sandbox makes of what an SSH client was given to
// run there: the client joins what it was given with spaces, and the
// sandbox's shell runs that with sh -c. It returns the arguments the
// program in the box is started with, which a shell here reads out.
func arriving(t *testing.T, given []string) []string {
	t.Helper()
	line, said := strings.CutPrefix(strings.Join(given, " "), machine.Program+" ")
	if !said {
		t.Fatalf("what is run in the sandbox is %q, want the program of the box, %s, and what it is given", given, machine.Program)
	}
	out, err := exec.Command("sh", "-c", "set -- "+line+`; [ $# -eq 0 ] || printf '%s\0' "$@"`).Output()
	if err != nil {
		t.Fatalf("a shell could not read %q: %v", line, err)
	}
	if len(out) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
}

// counted is a control plane that keeps the workspace "named", and how
// often it was asked anything.
func counted(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	api := connect.NewServer()
	workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, keeping{daemon: func() string { return "" }})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, api)
	asked := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, asked
}

// noDaemon has the test fail where a command asks for the build daemon
// of its directory, and counts how often one did.
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
	builder = func(context.Context, string) (machine.Builder, error) {
		*asked++
		return machine.Builder{}, errors.New("the build daemon of the directory was asked for")
	}
	return asked
}

// With a workspace selected, by its name or as the one last opened, a
// command that builds, tests, runs, queries or releases is run in that
// workspace's sandbox, over its shell: the program of the box, given
// what this command was given, less the workspace's name, which is said
// outside alone. How it ends there is how this command ends. No control
// plane is asked to build, and no build daemon of the directory. The
// controls: a workspace command, which asks the control plane and runs
// nothing in a sandbox; the same commands inside a sandbox, which ask the
// daemon there; with no workspace selected, the directory's daemon; and
// a workspace last opened of another control plane, which is refused as
// it was.
func TestWithAWorkspaceSelectedWhatBuildsIsRunInItsSandbox(t *testing.T) {
	plane, asked := counted(t)
	serverAt(t, plane)
	daemons := noDaemon(t)
	b := standingIn(t)
	b.exit = 7
	// Every command that takes a workspace's name is among them.
	tried := map[string]bool{}
	t.Cleanup(func() {
		var each func(*cobra.Command)
		each = func(cmd *cobra.Command) {
			if name := strings.TrimPrefix(cmd.CommandPath(), "frontseat "); cmd.Flags().Lookup("workspace") != nil && !tried[name] {
				t.Errorf("%s takes --workspace, and was not tried here", name)
			}
			for _, sub := range cmd.Commands() {
				each(sub)
			}
		}
		each(rootCmd)
	})
	for _, command := range []struct{ named, want []string }{
		{[]string{"build", "--workspace", "named"}, []string{"build"}},
		{[]string{"build", "list", "--workspace=named"}, []string{"build", "list"}},
		{[]string{"build", "--workspace", "named", "logs", "build_1", "//greet:go.lint"}, []string{"build", "logs", "build_1", "//greet:go.lint"}},
		{[]string{"build", "events", "build_1", "--from", "7", "--workspace", "named"}, []string{"build", "events", "build_1", "--from", "7"}},
		{[]string{"test", "gateway", "--workspace", "named", "--only", "unit"}, []string{"test", "gateway", "--only", "unit"}},
		{[]string{"run", "--workspace", "named", "//greet:go.run/greet", "--", "a", "b"}, []string{"run", "//greet:go.run/greet", "--", "a", "b"}},
		{[]string{"run", "list", "--workspace", "named"}, []string{"run", "list"}},
		{[]string{"build", "describe", "build_1", "--workspace", "named"}, []string{"build", "describe", "build_1"}},
		{[]string{"build", "actions", "--workspace", "named", "build_1"}, []string{"build", "actions", "build_1"}},
		{[]string{"build", "cancel", "build_1", "--workspace=named"}, []string{"build", "cancel", "build_1"}},
		{[]string{"run", "describe", "--workspace", "named", "run_1"}, []string{"run", "describe", "run_1"}},
		{[]string{"run", "logs", "run_1", "--workspace", "named"}, []string{"run", "logs", "run_1"}},
		{[]string{"run", "cancel", "--workspace=named", "run_1"}, []string{"run", "cancel", "run_1"}},
		{[]string{"--workspace", "named", "query"}, []string{"query"}},
		{[]string{"query", "gateway", "--workspace=named", "--why", "money", "--json"}, []string{"query", "gateway", "--why", "money", "--json"}},
		{[]string{"ship", "plan", "x", "--workspace", "named"}, []string{"ship", "plan", "x"}},
		{[]string{"ship", "--workspace", "named", "x", "--plan=seen"}, []string{"ship", "x", "--plan=seen"}},
	} {
		// Named, and then as the one last opened, which is said again
		// to no one: the sandbox is asked for as the one last opened.
		tried[Name(command.want)] = true
		for workspace, line := range map[string][]string{"named": command.named, "": command.want} {
			lastOpened(t, plane, "another")
			b.workspaces, b.given = nil, nil
			_, code, err := runAt(t, context.Background(), "", line...)
			if err != nil || code != 7 {
				t.Errorf("%v ended %d, %v; want as it ended in the sandbox, 7", line, code, err)
			}
			if len(b.given) != 1 || b.workspaces[0] != workspace || b.terminals != 0 {
				t.Errorf("%v was run in the sandboxes of %q, %d times with a terminal; want once in that of %q, with none", line, b.workspaces, b.terminals, workspace)
				continue
			}
			if got := arriving(t, b.given[0]); !slices.Equal(got, command.want) {
				t.Errorf("%v was run in the sandbox as %q, want %q", line, got, command.want)
			}
		}
	}
	if asked.Load() != 0 || *daemons != 0 {
		t.Errorf("the control plane was asked %d times and the directory's daemon %d; want neither asked to build", asked.Load(), *daemons)
	}

	// A workspace command asks the control plane, and runs nothing in a
	// sandbox; neither does what asks no workspace.
	b.workspaces = nil
	lastOpened(t, plane, "named")
	if printed, _, err := runAt(t, context.Background(), "", "workspace", "describe"); err != nil || printed != "named  ready\n" || asked.Load() != 1 {
		t.Errorf("workspace describe printed %q (%v), the control plane asked %d times", printed, err, asked.Load())
	}
	if _, _, err := runAt(t, context.Background(), "", "version"); err != nil {
		t.Errorf("version: %v", err)
	}
	// Inside a sandbox, the daemon there is asked.
	d := &daemonOf{}
	if _, code, err := run(t, d, "ship", "plan", "greet"); err != nil || code != 0 || len(d.planned) != 1 {
		t.Errorf("inside a sandbox, a plan ended %d (%v), its daemon asked %d times", code, err, len(d.planned))
	}
	// With no workspace selected, the directory's daemon is.
	lastOpened(t, plane, "")
	if _, _, err := runAt(t, context.Background(), "", "build"); err == nil || !strings.Contains(err.Error(), "the build daemon of the directory was asked for") || *daemons != 1 {
		t.Errorf("with no workspace selected: %v, the directory's daemon asked for %d times; want it asked for once", err, *daemons)
	}
	// The workspace last opened of another control plane is none of
	// this one's.
	lastOpened(t, "http://127.0.0.1:1", "named")
	if _, _, err := runAt(t, context.Background(), "", "build"); err == nil || !strings.Contains(err.Error(), "is of the control plane at http://127.0.0.1:1") {
		t.Errorf("the workspace last opened of another control plane: %v, want it refused", err)
	}
	if len(b.workspaces) != 0 || asked.Load() != 1 {
		t.Errorf("the controls ran in the sandboxes of %q, and the control plane was asked %d times; want none, and once", b.workspaces, asked.Load())
	}
}

// What a command is given arrives in the sandbox as it was given: the
// sandbox's shell reads the one line the SSH client sends, so each
// argument is written for it to read as that argument and nothing more,
// whatever it holds. Each is read here by a shell, as the sandbox's
// reads it. The control is the same arguments written as they are,
// which a shell reads as something else.
func TestWhatACommandIsGivenArrivesInTheSandboxAsItWas(t *testing.T) {
	plane, _ := counted(t)
	serverAt(t, plane)
	lastOpened(t, plane, "named")
	b := standingIn(t)
	given := []string{"a b", "it's", `"said"`, "$HOME", ";printf injected", "line\nbreak", "*", "-dash", "", `back\slash`, "`id`", "~", "#no", "a&b|c>d"}
	for _, line := range [][]string{
		append([]string{"run", "//greet:go.run/greet"}, given...),
		append([]string{"run", "//greet:go.run/greet", "--"}, given...),
		append([]string{"build", "--only", "x;y", "--"}, given...),
		{"build", "a b", "--only", "x;y", "--only=it's", "*"},
	} {
		b.given = nil
		if _, _, err := runAt(t, context.Background(), "", line...); err != nil || len(b.given) != 1 {
			t.Fatalf("%q: %v, run in a sandbox %d times", line, err, len(b.given))
		}
		if got := arriving(t, b.given[0]); !slices.Equal(got, line) {
			t.Errorf("%q arrived in the sandbox as\n%q", line, got)
		}
		if len(b.given[0]) != 1 {
			t.Errorf("the SSH client was given %d words to join, want the one line: %q", len(b.given[0]), b.given[0])
		}
	}
	plainly := []string{"a b", "$HOME", "*"}
	if written := arriving(t, append([]string{machine.Program}, plainly...)); slices.Equal(written, plainly) {
		t.Fatalf("written as they are, arguments arrive as they were, so this test proves nothing: %q", written)
	}
}

// The workspace a command names is said outside alone: --workspace is
// not said again in the sandbox, where it is refused, however it is
// written and wherever cobra reads it as the flag. What only looks like
// it is the command's own and is said again: what follows --, what a
// program is given, and what another flag takes for its value.
func TestTheWorkspaceNamedIsNotSaidAgainInItsSandbox(t *testing.T) {
	plane, _ := counted(t)
	serverAt(t, plane)
	lastOpened(t, plane, "opened")
	b := standingIn(t)
	for _, command := range []struct {
		line      []string
		workspace string
		want      []string
	}{
		{[]string{"build", "--workspace", "named", "x"}, "named", []string{"build", "x"}},
		{[]string{"build", "x", "--workspace=named"}, "named", []string{"build", "x"}},
		{[]string{"--workspace", "named", "build", "x"}, "named", []string{"build", "x"}},
		{[]string{"--workspace=named", "build", "list"}, "named", []string{"build", "list"}},
		{[]string{"build", "--workspace", "named", "cancel", "build_1"}, "named", []string{"build", "cancel", "build_1"}},
		{[]string{"build", "--workspace", "first", "x", "--workspace=named"}, "named", []string{"build", "x"}},
		{[]string{"build", "--workspace", "--only", "x"}, "--only", []string{"build", "x"}},
		{[]string{"run", "--workspace", "named", "prog", "--", "--workspace", "x"}, "named", []string{"run", "prog", "--", "--workspace", "x"}},
		// The control: what is no flag of this command's.
		{[]string{"run", "prog", "--", "--workspace", "x"}, "", []string{"run", "prog", "--", "--workspace", "x"}},
		{[]string{"run", "prog", "--workspace", "x"}, "", []string{"run", "prog", "--workspace", "x"}},
		{[]string{"run", "--", "prog", "--workspace=x"}, "", []string{"run", "--", "prog", "--workspace=x"}},
		{[]string{"build", "--", "--workspace", "x"}, "", []string{"build", "--", "--workspace", "x"}},
		{[]string{"build", "--only", "--workspace", "x"}, "", []string{"build", "--only", "--workspace", "x"}},
		{[]string{"build", "--only=--workspace", "x"}, "", []string{"build", "--only=--workspace", "x"}},
		{[]string{"test", "-", "--only", "unit"}, "", []string{"test", "-", "--only", "unit"}},
	} {
		b.workspaces, b.given = nil, nil
		if _, _, err := runAt(t, context.Background(), "", command.line...); err != nil || len(b.given) != 1 {
			t.Errorf("%q: %v, run in a sandbox %d times", command.line, err, len(b.given))
			continue
		}
		if got := arriving(t, b.given[0]); !slices.Equal(got, command.want) || b.workspaces[0] != command.workspace {
			t.Errorf("%q was run in the sandbox of %q as %q; want in that of %q, as %q", command.line, b.workspaces[0], got, command.workspace, command.want)
		}
	}
}

// What is run in a sandbox is handed to the SSH client as the last thing
// it is given, after the workspace's host, one line and no terminal
// asked for; and how the client ends is how the command ends. The SSH
// client here is one of the test's own, which keeps what it was given
// and ends as no command of frontseat's does.
func TestWhatIsRunInASandboxIsHandedToTheSSHClientAsOneLine(t *testing.T) {
	plane, asked := counted(t)
	serverAt(t, plane)
	before := inBox
	t.Cleanup(func() { inBox = before })
	inBox = shell
	client := t.TempDir()
	kept := filepath.Join(client, "given")
	if err := os.WriteFile(filepath.Join(client, "ssh"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+kept+"'\nexit 41\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", client+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, code, err := runAt(t, context.Background(), "", "build", "--workspace", "named", "a b", "--only", "x;y"); err != nil || code != 41 {
		t.Fatalf("the build ended %d, %v; want as the SSH client ended, 41", code, err)
	}
	given, err := os.ReadFile(kept)
	if err != nil {
		t.Fatal(err)
	}
	if want := "\nfrontseat-named\n" + machine.Program + " 'build' 'a b' '--only' 'x;y'\n"; !strings.HasSuffix(string(given), want) || strings.Contains(string(given), "\n-t\n") || asked.Load() != 0 {
		t.Errorf("the SSH client was given\n%s\nwant, last, %q, and no terminal; the control plane was asked %d times", given, want, asked.Load())
	}
}

// A command line is said again in a sandbox only where what is left of
// it holds the arguments the command read: one the command line reads
// otherwise than cobra did, as a flag of one dash that takes the next
// word, is refused, and nothing of it is repeated. The control is the
// same flag written with two dashes.
func TestACommandLineReadOtherwiseIsNotSaidAgain(t *testing.T) {
	for line, want := range map[string][]string{
		"made -n secret-word x":             nil,
		"made --name secret-word x":         {"made", "--name", "secret-word", "x"},
		"made --workspace w -- -n x":        {"made", "--", "-n", "x"},
		"made x --name=secret --workspace=": {"made", "x", "--name=secret"},
	} {
		root := &cobra.Command{Use: "frontseat"}
		made := &cobra.Command{Use: "made", RunE: func(*cobra.Command, []string) error { return nil }}
		root.AddCommand(made)
		made.Flags().StringP("name", "n", "", "")
		made.Flags().String("workspace", "", "")
		words := strings.Fields(line)
		root.SetArgs(words)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		got, err := passed(made, words)
		switch {
		case want == nil && (!errors.Is(err, errUnsaid) || strings.Contains(err.Error(), "secret")):
			t.Errorf("%q was said again as %q (%v), want it refused, and not repeated", line, got, err)
		case want != nil && (err != nil || !slices.Equal(got, want)):
			t.Errorf("%q is said again as %q (%v), want %q", line, got, err, want)
		}
	}
}

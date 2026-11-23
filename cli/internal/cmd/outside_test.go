package cmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
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

// counted is a control plane that keeps the workspace "named", and how
// often it was asked anything.
func counted(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	api := connect.NewServer()
	workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, keeping{})
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
// The controls: with no workspace selected, the directory's daemon is
// asked for; inside a sandbox, the daemon there is asked; a workspace
// command asks the control plane of the workspace last opened; and the
// workspace last opened of another control plane is refused as such.
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
		for _, said := range []string{"the workspace named is open", "(frontseat workspace ssh)", "(frontseat workspace ssh -- frontseat " + words + ")"} {
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
	// this one's, and is said to be that: the command takes no name.
	lastOpened(t, "http://127.0.0.1:1", "named")
	if _, _, err := runAt(t, context.Background(), "", "build"); err == nil || !strings.Contains(err.Error(), "the workspace last opened, named, is of the control plane at http://127.0.0.1:1") || !strings.HasSuffix(err.Error(), "open one there") {
		t.Errorf("the workspace last opened of another control plane: %v, want it refused as that, and no more", err)
	}
	if _, _, err := runAt(t, context.Background(), "", "workspace", "describe"); err == nil || !strings.Contains(err.Error(), "is of the control plane at http://127.0.0.1:1") || !strings.Contains(err.Error(), "open one there, or name one") {
		t.Errorf("a workspace command, of the workspace last opened of another control plane: %v, want it refused, and told it takes a name", err)
	}
	if *daemons != 0 {
		t.Errorf("the directory's daemon was asked for %d times by what was refused", *daemons)
	}
	// With no workspace selected, the directory's daemon is asked for.
	lastOpened(t, plane, "")
	if _, _, err := runAt(t, context.Background(), "", "build", "list"); err == nil || !strings.Contains(err.Error(), "the build daemon of the directory was asked for") || *daemons != 1 {
		t.Errorf("with no workspace selected: %v, the directory's daemon asked for %d times; want it asked for once", err, *daemons)
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
	if asked.Load() != 0 || *daemons != 0 || len(d.planned) != 0 {
		t.Errorf("the control plane was asked %d times, the directory's daemon %d and the sandbox's %d; want nothing asked", asked.Load(), *daemons, len(d.planned))
	}
	if _, _, err := run(t, d, "ship", "plan", "greet"); err != nil || len(d.planned) != 1 {
		t.Errorf("inside, without the flag, the daemon there was asked %d times (%v)", len(d.planned), err)
	}
	// With none selected, on a machine told nothing of its grid.
	lastOpened(t, plane, "")
	builder = func(context.Context, string, bool) (machine.Builder, error) {
		return machine.Builder{}, machine.ErrNoSettings
	}
	if _, _, err := runAt(t, context.Background(), "", "build"); err == nil || !strings.Contains(err.Error(), "no workspace to talk to: open one with `frontseat workspace open`; or, to build") || strings.Contains(err.Error(), "name one") {
		t.Errorf("a build with no workspace selected: %v; want how one is opened, and no name taken", err)
	}
	if _, _, err := runAt(t, context.Background(), "", "workspace", "describe"); err == nil || !strings.Contains(err.Error(), "open one with `frontseat workspace open`, or name one, as this command takes a workspace's NAME") {
		t.Errorf("workspace describe with no workspace selected: %v; want how one is opened, and that it takes a name", err)
	}
}

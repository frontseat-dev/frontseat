package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
)

// `workspace close` forgets which workspace is selected on this machine,
// and nothing else: the control plane is asked nothing, and a command
// that builds, refused until then, works on its directory again. It
// says what it closed, and that the workspace goes on; with none
// selected it says so, and is no mistake; one of another control plane
// is forgotten as any. It takes no name. The control is the build
// before it, which is refused.
func TestClosingAWorkspaceForgetsItHereAndAsksNoOne(t *testing.T) {
	plane, asked := counted(t)
	serverAt(t, plane)
	daemons := noDaemon(t)
	lastOpened(t, plane, "named")
	if _, _, err := runAt(t, context.Background(), "", "build", "list"); err == nil || !strings.Contains(err.Error(), "the workspace named is open") || *daemons != 0 {
		t.Fatalf("the control: a build with a workspace selected: %v, after %d daemons asked for; want it refused", err, *daemons)
	}
	if _, code, err := runAt(t, context.Background(), "", "workspace", "close", "named"); err == nil || code != ExitUsage {
		t.Errorf("workspace close with a name ended %d (%v); want it refused as written wrongly", code, err)
	}
	if _, found, err := machine.ReadContext(); err != nil || !found {
		t.Fatalf("what was refused forgot the selection: %v, %v", found, err)
	}
	printed, code, err := runAt(t, context.Background(), "", "workspace", "close")
	if want := "closed named on this machine: the workspace goes on (frontseat workspace open named selects it again, frontseat workspace delete named ends it)\n"; err != nil || code != ExitDone || printed != want {
		t.Errorf("workspace close printed %q and ended %d (%v); want %q", printed, code, err, want)
	}
	if _, found, err := machine.ReadContext(); err != nil || found {
		t.Errorf("closed, a workspace is still selected: %v, %v", found, err)
	}
	if _, _, err := runAt(t, context.Background(), "", "build", "list"); err == nil || !strings.Contains(err.Error(), "the build daemon of the directory was asked for") || *daemons != 1 {
		t.Errorf("after workspace close, a build: %v, after %d daemons asked for; want the directory's asked for", err, *daemons)
	}
	printed, code, err = runAt(t, context.Background(), "", "workspace", "close")
	if want := "no workspace is selected on this machine\n"; err != nil || code != ExitDone || printed != want {
		t.Errorf("workspace close with none selected printed %q and ended %d (%v); want %q", printed, code, err, want)
	}
	// One of another control plane, which answers no one.
	lastOpened(t, "http://127.0.0.1:1", "other")
	printed, _, err = runAt(t, context.Background(), "", "workspace", "close")
	if _, found, _ := machine.ReadContext(); err != nil || found || !strings.HasPrefix(printed, "closed other on this machine") {
		t.Errorf("workspace close of another control plane's printed %q (%v), and it is still selected: %t", printed, err, found)
	}
	if asked.Load() != 0 {
		t.Errorf("the control plane was asked %d times; want nothing asked", asked.Load())
	}
}

// A context this machine cannot read, written by another version of the
// command line or by anything else, selects nothing and is believed by
// no one: a command that builds and a workspace command told no name
// are refused, exit 2, naming the file and `frontseat workspace close`
// as the way out, and nothing the file holds; `workspace close` removes
// it, saying so, and a build works on its directory again. The control
// is a context in the command line's format, which is read.
func TestAContextThatCannotBeReadIsRemovedByClosing(t *testing.T) {
	plane, asked := counted(t)
	serverAt(t, plane)
	daemons := noDaemon(t)
	// No command here starts an SSH client: one that did would find the
	// test's own, never this machine's.
	shelled := noShell(t)
	t.Cleanup(func() {
		if shelled() {
			t.Error("an SSH client was started")
		}
	})
	file := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "frontseat", "context.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, written := range []string{
		`{"server":"` + plane + `","workspace":"named","build":"http://secret-word","sandbox":"secret-word"}`,
		`{"server":"` + plane + `","workspace":"secret-word\u001b[2J"}`,
		`{"server":"secret-word","workspace":"named"}`,
		`secret-word`,
	} {
		if err := os.WriteFile(file, []byte(written), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, command := range [][]string{{"build"}, {"test"}, {"query"}, {"ship", "plan", "greet"}, {"workspace", "describe"}, {"workspace", "add", "--entity", "gateway"}, {"workspace", "forward", "8080", "--listen", "127.0.0.1:0"}, {"workspace", "ssh", "--stdio"}} {
			// One that is taken, and serves, is ended and not waited for.
			briefly, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			printed, code, err := runAt(t, briefly, "", command...)
			cancel()
			switch {
			case err == nil || code != ExitUsage || !strings.Contains(err.Error(), file) || !strings.Contains(err.Error(), "`frontseat workspace close` removes it"):
				t.Errorf("%v, with a context that cannot be read (%s), ended %d (%v); want it refused by the file's name, with the way out", command, written, code, err)
			case strings.Contains(err.Error()+printed+complaints.String(), "secret-word"):
				t.Errorf("%v says what the context it cannot read holds: %v, %q, %q", command, err, printed, complaints.String())
			}
		}
		printed, code, err := runAt(t, context.Background(), "", "workspace", "close")
		if want := "forgot a selection that could not be read: no workspace is selected on this machine\n"; err != nil || code != ExitDone || printed != want {
			t.Errorf("workspace close, of a context that cannot be read (%s), printed %q and ended %d (%v); want %q", written, printed, code, err, want)
		}
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Errorf("workspace close left the context it could not read (%s): %v", written, err)
		}
	}
	if *daemons != 0 || asked.Load() != 0 {
		t.Fatalf("the directory's daemon was asked for %d times and the control plane %d times by what was refused", *daemons, asked.Load())
	}
	if _, _, err := runAt(t, context.Background(), "", "build"); err == nil || !strings.Contains(err.Error(), "the build daemon of the directory was asked for") || *daemons != 1 {
		t.Errorf("after workspace close, a build: %v, after %d daemons asked for; want the directory's asked for", err, *daemons)
	}
	// The control: one in the command line's format is read.
	if err := os.WriteFile(file, []byte(`{"server":"`+plane+`","workspace":"named"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if printed, _, err := runAt(t, context.Background(), "", "workspace", "describe"); err != nil || printed != "named  ready\n" {
		t.Errorf("the control: a context in the command line's format: %q (%v)", printed, err)
	}
}

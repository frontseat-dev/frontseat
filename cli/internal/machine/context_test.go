package machine

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/frontseat-dev/frontseat/local"
)

// The context is whole or not there: a command that reads it while
// another writes it reads the one before or the one after, never a
// part of either, so `frontseat workspace open` breaks no command beside it.
func TestTheContextIsReadWholeWhileItIsWritten(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	long := strings.Repeat("w", 64<<10)
	if err := WriteContext(Context{Server: "http://a", Workspace: long}); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var writing sync.WaitGroup
	writing.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := WriteContext(Context{Server: "http://a", Workspace: long[:len(long)-i%2]}); err != nil {
				t.Error(err)
				return
			}
		}
	})
	for range 2000 {
		c, found, err := ReadContext()
		if err != nil || !found || c.Server != "http://a" || len(c.Workspace) < len(long)-1 {
			close(stop)
			writing.Wait()
			t.Fatalf("read while it was written: %v, found %v, %v; want it whole", err, found, c.Server)
		}
	}
	close(stop)
	writing.Wait()
}

// The context file is the command line's: its keys are written so, and
// what an older one held besides is read past.
func TestTheContextFileIsWrittenInItsFormat(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	at := filepath.Join(config, "frontseat", "context.json")
	if err := WriteContext(Context{Server: "http://s", Workspace: "w"}); err != nil {
		t.Fatal(err)
	}
	// The directory it made holds a key and a token besides: it is the
	// person's alone.
	if kept, err := os.Stat(filepath.Dir(at)); err != nil || kept.Mode().Perm() != 0o700 {
		t.Errorf("the directory the context is kept in is %v (%v), want it its owner's alone", kept.Mode().Perm(), err)
	}
	raw, err := os.ReadFile(at)
	if want := "{\n  \"server\": \"http://s\",\n  \"workspace\": \"w\"\n}\n"; err != nil || string(raw) != want {
		t.Fatalf("wrote %q (%v), want %q", raw, err, want)
	}
	if err := os.WriteFile(at, []byte(`{"server":"http://s","workspace":"w","build":"http://b","sandbox":"sbx"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, found, err := ReadContext(); err != nil || !found || c != (Context{Server: "http://s", Workspace: "w"}) {
		t.Fatalf("read %+v, %v, %v", c, found, err)
	}
}

// What the command line keeps is beneath one directory, on every system:
// frontseat in the configuration directory of the XDG base directory
// specification, the context, this machine's key for its sandboxes and
// the local stack's token alike. The control is a machine with no
// XDG_CONFIG_HOME, whose root is in ~/.config.
func TestWhatTheCommandLineKeepsIsBeneathOneDirectory(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	if err := WriteContext(Context{Server: "http://s", Workspace: "w"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Identity(); err != nil {
		t.Fatal(err)
	}
	for _, kept := range []string{"context.json", "id_ed25519"} {
		if _, err := os.Stat(filepath.Join(config, "frontseat", kept)); err != nil {
			t.Errorf("%s is not beneath the root: %v", kept, err)
		}
	}
	if at, err := local.TokenFile(); err != nil || at != filepath.Join(config, "frontseat", "local", "token") {
		t.Errorf("the token is looked for at %s (%v), want beneath the root", at, err)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/someone")
	if at, err := root(); err != nil || at != "/home/someone/.config/frontseat" {
		t.Errorf("with no XDG_CONFIG_HOME the root is %s (%v), want ~/.config/frontseat", at, err)
	}
}

// The control plane the command line asks, and its token is for, is
// the local stack's, or the one local/server names, written as one
// address; a file that names anything else is refused.
func TestTheServerIsTheLocalStacksOrTheOneNamedBesideTheToken(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	if server, err := Server(); err != nil || server != Stack {
		t.Fatalf("with nothing named the command line asks %s (%v), want the local stack's", server, err)
	}
	kept := filepath.Join(config, "frontseat", "local")
	for written, want := range map[string]string{
		"http://127.0.0.1:41234\n": "http://127.0.0.1:41234",
		"http://127.0.0.1:41234/":  "http://127.0.0.1:41234",
		"ftp://127.0.0.1:1":        "",
		"http://someone@127.0.0.1": "",
		"http://127.0.0.1/a/path":  "",
		"127.0.0.1:1":              "",
		"http://a\nhttp://b":       "",
	} {
		server, err := serverNamed(t, written)
		if want == "" && err == nil {
			t.Errorf("%q was taken for the control plane %s", written, server)
		}
		if want != "" && (err != nil || server != want) {
			t.Errorf("%q: the command line asks %s (%v), want %s", written, server, err, want)
		}
	}
	// One another could write is refused: it says where the token goes.
	if err := os.WriteFile(filepath.Join(kept, "server"), []byte("http://127.0.0.1:41234\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(kept, "server"), 0o622); err != nil {
		t.Fatal(err)
	}
	if server, err := Server(); err == nil {
		t.Errorf("a file others can write was taken for the control plane %s", server)
	}
}

// A control plane on another machine is asked over TLS alone: a file
// that names one over http is refused, without the address, since the
// owner's token would be sent to it in clear. The controls are the same
// machine over https, and this machine over http, by its name and by
// its loopback addresses.
func TestAControlPlaneOnAnotherMachineIsAskedOverTLSAlone(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for written, taken := range map[string]bool{
		"http://frontseat.example:7700\n":      false,
		"http://192.0.2.1:7700\n":              false,
		"http://127.0.0.1.frontseat.example\n": false,
		"https://frontseat.example\n":          true,
		"https://frontseat.example:7700\n":     true,
		"http://localhost:7700\n":              true,
		"http://127.0.0.1:7700\n":              true,
		"http://[::1]:7700\n":                  true,
	} {
		server, err := serverNamed(t, written)
		switch {
		case taken && (err != nil || server != strings.TrimSuffix(written, "\n")):
			t.Errorf("the control: %q: the command line asks %q (%v)", written, server, err)
		case !taken && err == nil:
			t.Errorf("%q was taken for the control plane %s, and is asked in clear", written, server)
		case !taken && strings.Contains(err.Error(), "example") || !taken && strings.Contains(err.Error(), "192.0.2.1"):
			t.Errorf("the refusal of %q repeats it: %v", written, err)
		}
	}
}

// serverNamed is the control plane the command line asks where the
// server file holds what is written, or why it asks none.
func serverNamed(t *testing.T, written string) (string, error) {
	t.Helper()
	path, err := serverFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(written), 0o600); err != nil {
		t.Fatal(err)
	}
	return Server()
}

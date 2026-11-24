package machine

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/frontseat-dev/frontseat/stack/local"
)

// The context is whole or not there: a command that reads it while
// another writes it reads the one before or the one after, never a
// part of either, so `frontseat workspace open` breaks no command beside it.
func TestTheContextIsReadWholeWhileItIsWritten(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// Two contexts of two lengths: a part of either is none.
	long, short := "http://"+strings.Repeat("w", 200)+".example", "w"
	written := []Context{{Server: long, Workspace: strings.Repeat("w", 48)}, {Server: "http://a", Workspace: short}}
	if err := WriteContext(written[0]); err != nil {
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
			if err := WriteContext(written[i%2]); err != nil {
				t.Error(err)
				return
			}
		}
	})
	for range 2000 {
		c, found, err := ReadContext()
		if err != nil || !found || (c != written[0] && c != written[1]) {
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
	if c, found, err := ReadContext(); err != nil || !found || c != (Context{Server: "http://s", Workspace: "w"}) {
		t.Fatalf("the control: read %+v, %v, %v", c, found, err)
	}
	// It is read in its format alone: a key it does not have (an older
	// build's) or a second object is refused, naming the file, never read
	// past. So is a workspace that is no workspace's name, which would be
	// printed to a terminal as it is, and a server that is no server's
	// address. The refusal says what removes the file, and nothing the
	// file holds.
	for _, written := range []string{
		`{"server":"http://s","workspace":"w","build":"http://b","sandbox":"sbx"}`,
		`{"server":"http://s","workspace":"w"}{"workspace":"x"}`,
		`{"server":"http://s","workspace":"w\u001b[2Jsecret-word"}`,
		`{"server":"http://s","workspace":"Secret-Word"}`,
		`{"server":"http://s","workspace":""}`,
		`{"server":"http://s"}`,
		`{"server":"secret-word","workspace":"w"}`,
		`{"server":"http://s/secret-word","workspace":"w"}`,
		`{"server":"http://secret-word@s","workspace":"w"}`,
		`{"workspace":"w"}`,
		`secret-word`,
		``,
	} {
		if err := os.WriteFile(at, []byte(written), 0o600); err != nil {
			t.Fatal(err)
		}
		c, found, err := ReadContext()
		if err == nil || found || c != (Context{}) || !strings.Contains(err.Error(), at) || !strings.Contains(err.Error(), "`frontseat workspace close` removes it") {
			t.Errorf("%s was read as %+v, %v (%v), want it refused by the file's name, with what removes it", written, c, found, err)
		} else if strings.Contains(strings.ToLower(err.Error()), "secret-word") {
			t.Errorf("the refusal of %s repeats what the file holds: %v", written, err)
		}
	}
	// One that is forgotten is none, and no mistake, read or not.
	if err := ForgetContext(); err != nil {
		t.Fatal(err)
	}
	if c, found, err := ReadContext(); err != nil || found {
		t.Errorf("forgotten, it is read as %+v, %v (%v)", c, found, err)
	}
	if err := ForgetContext(); err != nil {
		t.Errorf("forgetting what is not there: %v", err)
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
	path, err := local.ServerFile()
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

// Two addresses name one control plane where they are one origin: the
// scheme and the host in whatever case, a scheme's own port written out
// or not. Nothing is resolved: localhost and a loopback address are two
// names, as they are two origins to the token. The controls are another
// port, another scheme and another host.
func TestTwoAddressesOfOneOriginAreOneControlPlane(t *testing.T) {
	for _, c := range []struct {
		one, other string
		same       bool
	}{
		{"http://127.0.0.1:7700", "http://127.0.0.1:7700", true},
		{"https://frontseat.example", "https://FrontSeat.Example", true},
		{"https://frontseat.example", "HTTPS://frontseat.example", true},
		{"https://frontseat.example", "https://frontseat.example:443", true},
		{"https://frontseat.example:443", "https://frontseat.example", true},
		{"http://localhost", "http://localhost:80", true},
		{"http://[::1]:80", "http://[::1]", true},
		{"http://localhost:7700", "http://LOCALHOST:7700", true},
		// Two names of this machine are two control planes.
		{"http://localhost:7700", "http://127.0.0.1:7700", false},
		{"http://127.0.0.1:7700", "http://[::1]:7700", false},
		// Another port, a port that is another scheme's own, another
		// scheme, another host.
		{"http://127.0.0.1:7700", "http://127.0.0.1:7701", false},
		{"https://frontseat.example", "https://frontseat.example:80", false},
		{"http://frontseat.example:443", "http://frontseat.example", false},
		{"http://frontseat.example", "https://frontseat.example", false},
		{"http://frontseat.example:80", "https://frontseat.example:443", false},
		{"https://frontseat.example", "https://frontseat.example.evil.example", false},
		{"https://frontseat.example:4430", "https://frontseat.example:443", false},
		// What is no address is no control plane, itself neither.
		{"", "", false},
		{"frontseat.example", "frontseat.example", false},
		{"http://%zz", "http://%zz", false},
	} {
		if got := SamePlane(c.one, c.other); got != c.same {
			t.Errorf("%q and %q are one control plane: %t, want %t", c.one, c.other, got, c.same)
		}
		if got := SamePlane(c.other, c.one); got != c.same {
			t.Errorf("%q and %q, the other way: %t, want %t", c.other, c.one, got, c.same)
		}
	}
}

// The files that tell the command line are said as they are named on
// this machine: beneath $XDG_CONFIG_HOME where it is set, and beneath
// ~/.config where it is not, which is the control.
func TestTheFilesThatTellTheCommandLineAreNamedAsTheyAreHere(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	if got, err := Where(); err != nil || got != (Places{
		Server:   filepath.Join(config, "frontseat", "local", "server"),
		Token:    filepath.Join(config, "frontseat", "local", "token"),
		Settings: filepath.Join(config, "frontseat", "build.yaml"),
	}) {
		t.Errorf("with XDG_CONFIG_HOME set, the files are %+v (%v)", got, err)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/someone")
	if got, err := Where(); err != nil || got.Token != "/home/someone/.config/frontseat/local/token" || got.Settings != "/home/someone/.config/frontseat/build.yaml" {
		t.Errorf("the control: with none, the files are %+v (%v)", got, err)
	}
}

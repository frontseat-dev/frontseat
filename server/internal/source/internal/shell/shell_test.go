package shell_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"

	"github.com/frontseat-dev/frontseat/server/internal/source/internal/shell"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrequired"
)

func key(t *testing.T) gossh.Signer {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := gossh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

// served serves a directory to whoever holds a key, and returns the
// address.
func served(t *testing.T, authorized gossh.Signer, dir string) string {
	t.Helper()
	keys, err := shell.Authorized(string(gossh.MarshalAuthorizedKey(authorized.PublicKey())))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() { _ = shell.Serve(listener, shell.Config{Authorized: keys, Directory: dir}) }()
	return listener.Addr().String()
}

func login(address string, with gossh.Signer) (*gossh.Client, error) {
	return gossh.Dial("tcp", address, &gossh.ClientConfig{
		User:            "developer",
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(with)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	})
}

// loggedIn logs in, with a key of its own, to a shell served over a
// workspace of the test's to that key, and logs out when the test ends.
func loggedIn(t *testing.T) *gossh.Client {
	t.Helper()
	mine := key(t)
	client, err := login(served(t, mine, workspace(t)), mine)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

// opened opens a session on a connection, which ends when the test does.
func opened(t *testing.T, client *gossh.Client) *gossh.Session {
	t.Helper()
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func workspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestWhoeverHoldsAnAuthorizedKeyRunsACommandInTheWorkspace(t *testing.T) {
	client := loggedIn(t)

	session := opened(t, client)
	out, err := session.Output("ls && exit 7")

	if got := strings.TrimSpace(string(out)); got != "main.go" {
		t.Errorf("the command printed %q, want the workspace's files", got)
	}
	exit, isExit := err.(*gossh.ExitError)
	if !isExit || exit.ExitStatus() != 7 {
		t.Errorf("err = %v, want the command's exit status, 7", err)
	}
}

func TestAKeyThatIsNotAuthorizedIsRefused(t *testing.T) {
	if _, err := login(served(t, key(t), workspace(t)), key(t)); err == nil {
		t.Error("a key nobody authorized logged in")
	}
}

func TestASessionThatAsksForATerminalHasOne(t *testing.T) {
	// A grid's action may open no terminal: every action of a runner is
	// one user, and a terminal one opened another could write into. It is
	// run where the stack is, as `mise run test` runs it, and anywhere a
	// terminal can be opened.
	if ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0); err == nil {
		_ = ptmx.Close()
	} else if testrequired.Stackless() {
		t.Skip("no terminal can be opened here, and the stack is not: ", err)
	}
	client := loggedIn(t)
	session := opened(t, client)
	if err := session.RequestPty("xterm-256color", 24, 80, gossh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}

	out, err := session.Output(`test -t 0 && echo "a terminal, $TERM"`)

	if err != nil || !strings.Contains(string(out), "a terminal, xterm-256color") {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

// Nothing is uploaded to a sandbox: its shell serves no way to transfer a
// file, the SFTP an editor or `scp` asks for among them. The control is
// the same connection, on which a command runs.
func TestNoFileTransferIsServed(t *testing.T) {
	client := loggedIn(t)

	asking := opened(t, client)
	if err := asking.RequestSubsystem("sftp"); err == nil {
		t.Errorf("the shell serves SFTP")
	}

	running := opened(t, client)
	if out, err := running.Output("cat main.go"); err != nil || string(out) != "package main\n" {
		t.Errorf("on the same connection a command printed %q (%v)", out, err)
	}
}

func TestAConnectionIsForwardedToAPortOfTheSandbox(t *testing.T) {
	inside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "the back end") }))
	defer inside.Close()
	client := loggedIn(t)

	through := &http.Client{Transport: &http.Transport{Dial: client.Dial}}
	response, err := through.Get(inside.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)

	if string(body) != "the back end" {
		t.Errorf("through the forwarded connection: %q", body)
	}
}

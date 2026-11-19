package e2e

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/frontseat-dev/frontseat/sdk/go"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/server/testsupport/testidentity"
	"github.com/frontseat-dev/frontseat/server/testsupport/testowner"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrequired"
)

// piped is a connection over a program's standard input and output, as
// an SSH client's proxy command is.
type piped struct {
	io.Reader
	io.WriteCloser
}

func (piped) LocalAddr() net.Addr              { return &net.UnixAddr{Name: "stdio"} }
func (piped) RemoteAddr() net.Addr             { return &net.UnixAddr{Name: "stdio"} }
func (piped) SetDeadline(time.Time) error      { return nil }
func (piped) SetReadDeadline(time.Time) error  { return nil }
func (piped) SetWriteDeadline(time.Time) error { return nil }
func (p piped) Close() error                   { return p.WriteCloser.Close() }

// A person reaches a workspace's shell on the local stack through the
// command line, as an editor and `frontseat workspace ssh` do: through
// `frontseat workspace ssh --stdio`, which presents the owner's token
// the stack laid out. The control is the same command on a machine with
// no token, whose tunnel the stack refuses.
func TestAShellIsReachedThroughTheCommandLineWithTheOwnersToken(t *testing.T) {
	testrequired.Stack(t)
	testgrid.InSandbox(t)
	image := testbox.Image(t)
	owner := stackOwner(t)
	made, _ := madeWorkspace(t, frontseat.New(stack, owner), frontseat.Spec{Image: image, Repositories: cloned(t, helloModule(t))})

	command := filepath.Join(t.TempDir(), "frontseat")
	if out, err := exec.Command("go", "build", "-o", command, "github.com/frontseat-dev/frontseat/cli/cmd/frontseat").CombinedOutput(); err != nil {
		t.Fatalf("building the command line: %v\n%s", err, out)
	}
	// The command line keeps its context beneath a directory of the
	// test's, beside the owner's token the stack laid out, or beside none:
	// the workspace last opened, of the stack's control plane, which is
	// the one it asks where nothing names another.
	raw, err := json.Marshal(map[string]string{"server": stack, "workspace": made.Name()})
	if err != nil {
		t.Fatal(err)
	}
	opened := func(token string) string {
		config := testowner.LayOut(t, token, "")
		if err := os.WriteFile(filepath.Join(config, "frontseat", "context.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return config
	}
	owned, unowned := opened(testowner.File(t)), opened("")
	// ssh asks the tunnel and speaks SSH over it, logging in with the
	// test's key, and runs a command.
	ssh := func(root string) (string, string, error) {
		cmd := exec.Command(command, "workspace", "ssh", "--stdio")
		cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+root)
		var said strings.Builder
		cmd.Stderr = &said
		in, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = in.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
		config := &ssh.ClientConfig{User: "frontseat", Auth: []ssh.AuthMethod{ssh.PublicKeys(testidentity.Key())}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: time.Minute}
		conn, channels, requests, err := ssh.NewClientConn(piped{Reader: out, WriteCloser: in}, made.Name(), config)
		if err != nil {
			_ = in.Close()
			_ = cmd.Wait()
			return "", said.String(), err
		}
		shell := ssh.NewClient(conn, channels, requests)
		defer shell.Close()
		session, err := shell.NewSession()
		if err != nil {
			return "", said.String(), err
		}
		printed, err := session.Output("uname -s")
		return strings.TrimSpace(string(printed)), said.String(), err
	}
	printed, said, err := ssh(owned)
	if err != nil || printed != "Linux" {
		t.Errorf("a command over `frontseat workspace ssh --stdio`: %q, %v (%s), want Linux", printed, err, said)
	}
	// The control: no token, and the stack refuses the tunnel.
	if _, said, err := ssh(unowned); err == nil || !strings.Contains(said, "401") {
		t.Errorf("without the owner's token the shell was reached, or refused otherwise: %v (%s)", err, said)
	}
}

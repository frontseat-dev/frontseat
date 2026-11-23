package source_test

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/workspace/box/source"
	"golang.org/x/crypto/ssh"
)

// keyedShell serves a shell over a directory of the test's to the one
// key it makes, closes the shell when the test ends, and returns it and
// the key.
func keyedShell(t *testing.T) (*source.Shell, ssh.Signer) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	editor, err := source.NewShell(source.ShellConfig{Address: "127.0.0.1:0", Directory: t.TempDir(), AuthorizedKeys: string(ssh.MarshalAuthorizedKey(signer.PublicKey()))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = editor.Close() })
	return editor, signer
}

// dialled connects to a shell with a key, as the developer, and closes
// the connection when the test ends.
func dialled(t *testing.T, editor *source.Shell, signer ssh.Signer) *ssh.Client {
	t.Helper()
	client, err := ssh.Dial("tcp", editor.Address(), &ssh.ClientConfig{User: "developer", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// printedProcess starts a command in a session of a shell, reached with
// a key, and returns the process whose id is the first line the command
// prints; what names that id in the test's failure.
func printedProcess(t *testing.T, editor *source.Shell, signer ssh.Signer, command, what string) *os.Process {
	t.Helper()
	session, err := dialled(t, editor, signer).NewSession()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	stdout, err := session.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Start(command); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if _, err := fmt.Sscan(strings.TrimSpace(line), &pid); err != nil || pid <= 0 {
		t.Fatalf("bad %s %q: %v", what, line, err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	return process
}

func TestShellCloseOwnsAcceptedHandshake(t *testing.T) {
	editor, _ := keyedShell(t)
	conn, err := net.Dial("tcp", editor.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if banner, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("accepted connection did not begin handshake: %q %v", banner, err)
	}
	// Control: it is accepted and awaiting the peer's handshake, not already closed.
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	_, err = reader.ReadByte()
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("handshake control: want waiting connection, got %v", err)
	}
	if err := editor.Close(); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, err = reader.ReadByte()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Close returned while accepted handshake remains open: want EOF, got %v", err)
	}
}

func TestShellConfigurationAndSessionLifetime(t *testing.T) {
	if s, err := source.NewShell(source.ShellConfig{Address: "invalid", AuthorizedKeys: "# no editor"}); err != nil || s != nil {
		t.Fatalf("disabled: %v %v", s, err)
	}
	var disabled *source.Shell
	if err := disabled.Close(); err != nil {
		t.Fatalf("closing the shell of a sandbox that serves none: %v", err)
	}
	if s, err := source.NewShell(source.ShellConfig{Address: "127.0.0.1:0", AuthorizedKeys: "invalid-key"}); err == nil || s != nil {
		t.Fatalf("invalid key accepted: %v %v", s, err)
	}
	editor, signer := keyedShell(t)
	process := printedProcess(t, editor, signer, "echo $$; exec sleep 60", "process id")
	defer process.Kill()
	if err := process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("live session control: %v", err)
	}
	var joined sync.WaitGroup
	for range 3 {
		joined.Go(func() {
			if err := editor.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	joined.Wait()
	if err := process.Signal(syscall.Signal(0)); err == nil {
		t.Fatal("Close returned before the session process ended")
	}
	if conn, err := net.DialTimeout("tcp", editor.Address(), time.Second); err == nil {
		conn.Close()
		t.Fatal("closed shell still admits connections")
	}
}

func TestShellCloseStopsCommandChildren(t *testing.T) {
	editor, signer := keyedShell(t)
	child := printedProcess(t, editor, signer, "sleep 60 & echo $!; wait", "child pid")
	defer child.Kill()
	done := make(chan error, 1)
	go func() { done <- editor.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		_ = child.Kill()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("Close still stuck after test child cleanup")
		}
		t.Fatal("Close waited for an orphaned command child instead of stopping the session process tree")
	}
}

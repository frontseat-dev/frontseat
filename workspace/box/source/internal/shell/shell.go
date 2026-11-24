// Package shell lets a person into a sandbox the way an editor reaches a
// machine: over SSH. It serves what VS Code's remote development and
// JetBrains Gateway ask of a host, less one thing: a command or a
// terminal, and connections forwarded to and from the sandbox's ports.
// It serves no transfer of files: nothing is uploaded to a sandbox,
// whose source its supervisor clones, and what changes there is changed by
// work done in it. A sandbox image needs no SSH server of its own.
//
// It is for a sandbox that a person works in. A sandbox that holds only
// a supervisor does not serve it.
package shell

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	osexec "os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/frontseat-dev/ssh"
	gossh "golang.org/x/crypto/ssh"

	"github.com/frontseat-dev/frontseat/common/confine"
)

// Config is who a sandbox lets in, and where.
type Config struct {
	// Authorized are the keys that may log in. Whoever holds one is let
	// in, under any user name, as the user the sandbox runs as.
	Authorized []ssh.PublicKey
	// Directory is where a session starts: the workspace.
	Directory string
	// Handshake is how long a connection has to log in; none given is
	// handshakeWithin.
	Handshake time.Duration
}

// handshakeWithin is how long a connection has to log in. Whoever
// reaches the port and shows no key holds a connection no longer.
const handshakeWithin = 30 * time.Second

// Authorized reads keys as an authorized_keys file lists them.
func Authorized(keys string) ([]ssh.PublicKey, error) {
	var out []ssh.PublicKey
	for _, line := range strings.Split(keys, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("an authorized key: %w", err)
		}
		out = append(out, key)
	}
	return out, nil
}

// Serve serves SSH on a listener until the listener is closed. The host
// key is made anew each time: a sandbox is not a host that lasts.
func Serve(listener net.Listener, config Config) error {
	if len(config.Authorized) == 0 {
		return errors.New("shell: no key is authorized, so nobody could log in")
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	host, err := gossh.NewSignerFromKey(private)
	if err != nil {
		return err
	}
	if config.Handshake == 0 {
		config.Handshake = handshakeWithin
	}
	sessions := &sessions{}
	server := &ssh.Server{
		HostSigners:      []ssh.Signer{host},
		HandshakeTimeout: config.Handshake,
		Handler:          sessions.handle(config.session),
		PublicKeyHandler: func(_ ssh.Context, key ssh.PublicKey) bool {
			for _, authorized := range config.Authorized {
				if ssh.KeysEqual(key, authorized) {
					return true
				}
			}
			return false
		},
		// An editor reaches its back end, and what a developer runs,
		// through forwarded connections.
		LocalPortForwardingCallback:   func(ssh.Context, string, uint32) bool { return true },
		ReversePortForwardingCallback: func(ssh.Context, string, uint32) bool { return true },
		ChannelHandlers: map[string]ssh.ChannelHandler{
			"session":      ssh.DefaultSessionHandler,
			"direct-tcpip": ssh.DirectTCPIPHandler,
		},
		RequestHandlers: map[string]ssh.RequestHandler{
			"tcpip-forward":        forward,
			"cancel-tcpip-forward": forward,
		},
		// No subsystem is served: SFTP is how a file is copied in, and
		// none is.
		SubsystemHandlers: map[string]ssh.SubsystemHandler{},
	}
	defer server.Close()
	err = serveConnections(listener, server, sessions)
	if errors.Is(err, ssh.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// session runs a command, or a login shell where none is given, with a
// terminal where one is asked for.
func (c Config) session(s ssh.Session) {
	shell := "/bin/sh"
	if bash, err := osexec.LookPath("bash"); err == nil {
		shell = bash
	}
	cmd := osexec.CommandContext(s.Context(), shell, "-l")
	if command := s.RawCommand(); command != "" {
		cmd = osexec.CommandContext(s.Context(), shell, "-c", command)
	}
	cmd.Dir = c.Directory
	cmd.Env = append(os.Environ(), s.Environ()...)
	// Cancellation belongs to the process group for the whole session,
	// including output draining after the shell exits. CommandContext's
	// default cancellation kills only the shell itself.
	cmd.Cancel = nil

	var err error
	if terminal, resized, asked := s.Pty(); asked {
		cmd.Env = append(cmd.Env, "TERM="+terminal.Term)
		var tty *os.File
		tty, err = pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(terminal.Window.Height), Cols: uint16(terminal.Window.Width)})
		if err == nil {
			// The terminal is resized for as long as the command runs, and
			// no longer: it is closed only once nothing resizes it.
			over, resizing := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(resizing)
				for {
					select {
					case size, more := <-resized:
						if !more {
							return
						}
						_ = pty.Setsize(tty, &pty.Winsize{Rows: uint16(size.Height), Cols: uint16(size.Width)})
					case <-over:
						return
					}
				}
			}()
			go func() { _, _ = io.Copy(tty, s) }()
			var once sync.Once
			closeTerminal := func() { once.Do(func() { close(over); <-resizing; _ = tty.Close() }) }
			stopCancellation := onCancel(s.Context(), func() {
				_ = confine.SignalGroup(cmd.Process.Pid, syscall.SIGKILL)
				closeTerminal()
			})
			_, _ = io.Copy(s, tty)
			err = cmd.Wait()
			stopCancellation()
			closeTerminal()
		}
	} else {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Stdout, cmd.Stderr = s, s.Stderr()
		var stdin io.WriteCloser
		if stdin, err = cmd.StdinPipe(); err == nil {
			go func() {
				_, _ = io.Copy(stdin, s)
				stdin.Close()
			}()
			if err = cmd.Start(); err == nil {
				stopCancellation := onCancel(s.Context(), func() { _ = confine.SignalGroup(cmd.Process.Pid, syscall.SIGKILL) })
				err = cmd.Wait()
				stopCancellation()
			} else {
				_ = stdin.Close()
			}
		}
	}

	var exit *osexec.ExitError
	switch {
	case err == nil:
		_ = s.Exit(0)
	case errors.As(err, &exit):
		_ = s.Exit(exit.ExitCode())
	default:
		fmt.Fprintln(s.Stderr(), "frontseat:", err)
		_ = s.Exit(255)
	}
}

// sessions closes admission before joining commands. The SSH
// library launches these separately from the connection that admitted them.
type sessions struct {
	mu      sync.Mutex
	stopped bool
	active  sync.WaitGroup
}

func (s *sessions) handle(next func(ssh.Session)) func(ssh.Session) {
	return func(session ssh.Session) {
		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			_ = session.Close()
			return
		}
		s.active.Add(1)
		s.mu.Unlock()
		defer s.active.Done()
		next(session)
	}
}

// serveConnections owns each accepted socket before the SSH handshake starts.
// The SSH server's Close only tracks connections after authentication.
func serveConnections(listener net.Listener, server *ssh.Server, sessions *sessions) error {
	defer listener.Close()
	var mu sync.Mutex
	connections := map[net.Conn]struct{}{}
	var active sync.WaitGroup
	defer func() {
		sessions.mu.Lock()
		sessions.stopped = true
		sessions.mu.Unlock()
		mu.Lock()
		for conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		active.Wait()
		sessions.active.Wait()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		mu.Lock()
		connections[conn] = struct{}{}
		mu.Unlock()
		active.Go(func() {
			defer func() { mu.Lock(); delete(connections, conn); mu.Unlock() }()
			server.HandleConn(conn)
		})
	}
}

// onCancel joins its callback before resources it uses can be released.
func onCancel(ctx context.Context, cancel func()) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(done); cancel() })
	return func() {
		if !stop() {
			<-done
		}
	}
}

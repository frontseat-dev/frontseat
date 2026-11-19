package source

import (
	"errors"
	"net"
	"sync"

	"github.com/frontseat-dev/frontseat/server/internal/source/internal/shell"
)

// ShellConfig selects the workspace editor endpoint and the keys allowed in it.
type ShellConfig struct {
	Address, Directory, AuthorizedKeys string
}

// Shell owns an editor endpoint. Close stops admission and joins its server.
type Shell struct {
	listener net.Listener
	done     chan struct{}
	once     sync.Once
	err      error
}

// NewShell starts workspace editor access. With no authorized keys it returns
// nil: a sandbox intended only for a daemon serves no editor endpoint.
func NewShell(config ShellConfig) (*Shell, error) {
	keys, err := shell.Authorized(config.AuthorizedKeys)
	if err != nil || len(keys) == 0 {
		return nil, err
	}
	listener, err := net.Listen("tcp", config.Address)
	if err != nil {
		return nil, err
	}
	s := &Shell{listener: listener, done: make(chan struct{})}
	go func() {
		s.err = shell.Serve(listener, shell.Config{Authorized: keys, Directory: config.Directory})
		close(s.done)
	}()
	return s, nil
}

// Address is where the editor endpoint is listening.
func (s *Shell) Address() string { return s.listener.Addr().String() }

// Wait returns when the editor server ends, with its failure if any.
func (s *Shell) Wait() error {
	<-s.done
	if errors.Is(s.err, net.ErrClosed) {
		return nil
	}
	return s.err
}

// Close stops the endpoint and waits for its serving loop to end.
func (s *Shell) Close() error {
	s.once.Do(func() { _ = s.listener.Close() })
	return s.Wait()
}

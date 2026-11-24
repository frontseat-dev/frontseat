// Package machine is what the command line keeps and reads on this
// machine: the workspace selected here (the context), the key it logs
// in to sandboxes with, the checkout it is run in, and the build daemon
// of a directory, which it starts.
package machine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/frontseat-dev/frontseat/sdk/go"
	"github.com/frontseat-dev/frontseat/stack/local"
)

// Context is the workspace selected on this machine, the one last
// opened, and the control plane that keeps it. A workspace command told
// no name asks that one; and while it is selected, of the control plane
// asked now, what builds, tests, runs, queries or releases is run in it
// and not on this machine's directory. `frontseat workspace close`
// forgets it here. The context file holds it as these keys say, in the
// command line's format.
type Context struct {
	Server    string `json:"server"`
	Workspace string `json:"workspace"`
}

// root is the one directory the command line keeps what it keeps in, on
// every system: frontseat in the configuration directory of the XDG
// base directory specification ($XDG_CONFIG_HOME, else ~/.config). The
// context (frontseat/context.json), this machine's key for its
// sandboxes (frontseat/id_ed25519) and the local stack's owner's token,
// which the stack lays out (frontseat/local/token), are there, and
// nothing of the command line's is anywhere else. The stack and the
// test support find it as the command line does (local.Config).
func root() (string, error) {
	return local.Config()
}

// beneath is the path of a file kept beneath the root.
func beneath(name ...string) (string, error) {
	dir, err := root()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{dir}, name...)...), nil
}

// contextFile is where the context is kept: frontseat/context.json.
func contextFile() (string, error) {
	return beneath("context.json")
}

// ReadContext returns the context: the workspace selected here. It says
// whether there is one. The file is read in its format alone: a key it
// does not have, more than one object, a workspace that is no
// workspace's name or a server that is no server's address is refused,
// naming the file and never what it holds, which is believed and
// printed by no one: `frontseat workspace close` removes such a file,
// and the next workspace opened writes it again.
func ReadContext() (Context, bool, error) {
	path, err := contextFile()
	if err != nil {
		return Context{}, false, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Context{}, false, nil
	}
	if err != nil {
		return Context{}, false, err
	}
	var c Context
	strict := json.NewDecoder(bytes.NewReader(raw))
	strict.DisallowUnknownFields()
	if err := strict.Decode(&c); err != nil || strict.More() || !frontseat.IsWorkspaceName(c.Workspace) {
		return Context{}, false, unread(path)
	}
	// A server is what the SDK makes a client for, as the server file's
	// is (Server); it is said back as that client says it.
	plane, err := frontseat.New(c.Server)
	if err != nil {
		return Context{}, false, unread(path)
	}
	return Context{Server: plane.Server(), Workspace: c.Workspace}, true, nil
}

// unread is a context file that does not say a context, by the file's
// name and with the way out.
func unread(path string) error {
	return fmt.Errorf("%s is not written in the command line's format, and says no workspace selected here: `frontseat workspace close` removes it, and the next workspace opened writes it again", path)
}

// WriteContext keeps the context, replacing the one there was at once.
// The directory it makes is the person's alone, as the key and the token
// kept beside it are (Identity).
func WriteContext(c Context) error {
	path, err := contextFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	// Whole or not there: a command reading it meanwhile reads the one
	// before, never part of this one (keep).
	return keep(path, append(raw, '\n'))
}

// ForgetContext removes the context, read or not: the file is the
// command line's own, on this machine, at the one path it keeps its
// context (contextFile), and nothing of a control plane's. One that is
// not there is forgotten already.
func ForgetContext() error {
	path, err := contextFile()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

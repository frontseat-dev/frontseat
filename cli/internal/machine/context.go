// Package machine is what the command line keeps and reads on this
// machine: the context its commands go to, the key it logs in to
// sandboxes with, and the checkout it is run in.
package machine

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/frontseat-dev/frontseat/local"
)

// Context is the workspace last opened, and the control plane that keeps
// it: what a command asks where it is told no workspace. The context
// file holds it as these keys say, in the command line's format.
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

// ReadContext returns the context: the workspace last opened. It says
// whether there is one.
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
	if err := json.Unmarshal(raw, &c); err != nil {
		return Context{}, false, err
	}
	return c, true, nil
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

// ForgetContext removes the context.
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

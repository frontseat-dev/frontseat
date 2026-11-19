package ports

import (
	"os"
	"path/filepath"
)

// Config returns the one directory the command line keeps what it keeps
// in, on every system: frontseat in the configuration directory of the
// XDG base directory specification ($XDG_CONFIG_HOME, else ~/.config; a
// relative one is ignored, as the specification says). The local stack
// lays its owner's token out beneath it (Token), where the command line
// reads it to reach the stack's control plane: said here once, for the
// command line, the stack and the test support alike.
func Config() (string, error) {
	config := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(config) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		config = filepath.Join(home, ".config")
	}
	return filepath.Join(config, "frontseat"), nil
}

// Token is where, beneath Config, the local stack lays out its owner's
// token, and Server where whoever lays out a token for another control
// plane names it beside.
var (
	Token  = filepath.Join("local", "token")
	Server = filepath.Join("local", "server")
)

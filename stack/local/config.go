package local

import (
	"os"
	"path/filepath"
)

// Config returns the one directory the command line keeps what it keeps
// in, on every system: frontseat in the configuration directory of the
// XDG base directory specification ($XDG_CONFIG_HOME, else ~/.config).
// A directory that is no absolute path is ignored, as the specification
// says. The stack lays its owner's token out beneath it, the command
// line reads it there, and the test support finds it there: each asks
// here, so that none looks where another did not write.
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

// TokenFile returns where the local stack lays out its owner's token
// (frontseat-stack up): frontseat/local/token in the configuration
// directory, beneath Config.
func TokenFile() (string, error) {
	return beneath("token")
}

// ServerFile returns the file that names the control plane the command
// line asks, where it is not the local stack's: frontseat/local/server,
// beside the token. Whoever lays out a token for another control plane writes it
// there, and the local stack removes it when it lays out its own.
func ServerFile() (string, error) {
	return beneath("server")
}

// beneath is the path of a file of the local stack's beneath what the
// command line keeps.
func beneath(name string) (string, error) {
	dir, err := Config()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "local", name), nil
}

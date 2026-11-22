package local

import (
	"os"
	"path/filepath"
)

// ConfigRoot is the one directory the command line keeps what it keeps
// in, on every system: frontseat in the configuration directory of the
// XDG base directory specification ($XDG_CONFIG_HOME, else ~/.config;
// a relative one is ignored, as the specification says). The local
// stack lays its owner's token out beneath it (frontseat/local), where
// the command line reads it.
func ConfigRoot() (string, error) {
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

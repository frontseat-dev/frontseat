package local

import (
	"path/filepath"
	"testing"
)

// The configuration directory is $XDG_CONFIG_HOME's where it is a whole
// path, and ~/.config's where it is unset or relative, which the
// specification says is ignored: a command line and a stack that read it
// otherwise would lay out and look for the owner's token in two places.
func TestTheConfigurationDirectoryIsWorkedOutOneWay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for given, want := range map[string]string{
		"/somewhere/config": "/somewhere/config/frontseat",
		"":                  filepath.Join(home, ".config", "frontseat"),
		"relative/config":   filepath.Join(home, ".config", "frontseat"),
	} {
		t.Setenv("XDG_CONFIG_HOME", given)
		if got, err := ConfigRoot(); err != nil || got != want {
			t.Errorf("XDG_CONFIG_HOME=%q: %q, %v; want %q", given, got, err, want)
		}
	}
}

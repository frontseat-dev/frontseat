package ports

import (
	"path/filepath"
	"testing"
)

// The command line's directory is beneath an absolute XDG_CONFIG_HOME,
// and beneath the home's .config where it is empty or relative, as the
// specification says.
func TestARelativeConfigurationDirectoryIsIgnored(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	absolute := t.TempDir()
	for given, want := range map[string]string{
		absolute:   filepath.Join(absolute, "frontseat"),
		"":         filepath.Join(home, ".config", "frontseat"),
		"relative": filepath.Join(home, ".config", "frontseat"),
	} {
		t.Setenv("XDG_CONFIG_HOME", given)
		if got, err := Config(); err != nil || got != want {
			t.Errorf("with XDG_CONFIG_HOME=%q, Config() = %q, %v; want %q", given, got, err, want)
		}
	}
}

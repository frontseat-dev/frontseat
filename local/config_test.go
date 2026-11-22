package local_test

import (
	"path/filepath"
	"testing"

	"github.com/frontseat-dev/frontseat/local"
)

// What the command line keeps is beneath the person's home where the
// configuration directory named is no absolute path, as the XDG
// specification says, and the owner's token and the server's file are
// beneath it. The control is an absolute one, which is the one read.
func TestARelativeConfigurationDirectoryIsIgnored(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	absolute := t.TempDir()
	for _, c := range []struct{ named, config string }{
		{"relative", filepath.Join(home, ".config")},
		{"", filepath.Join(home, ".config")},
		{absolute, absolute},
	} {
		t.Setenv("XDG_CONFIG_HOME", c.named)
		for want, read := range map[string]func() (string, error){
			filepath.Join(c.config, "frontseat"):                    local.Config,
			filepath.Join(c.config, "frontseat", "local", "token"):  local.TokenFile,
			filepath.Join(c.config, "frontseat", "local", "server"): local.ServerFile,
		} {
			if got, err := read(); err != nil || got != want {
				t.Errorf("with the configuration directory %q: %s (%v), want %s", c.named, got, err, want)
			}
		}
	}
}

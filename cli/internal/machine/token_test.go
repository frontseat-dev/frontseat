package machine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/frontseat-dev/frontseat/stack/local"
)

// A token is the control plane's to make and to know: the command line
// reads whatever one line its file holds and presents it, whatever form
// the control plane gives its tokens. What is no token, nothing or more
// than one word, is refused.
func TestATokenIsReadAsTheControlPlaneWroteIt(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := local.TokenFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	for written, want := range map[string]string{
		"746f6b656e2d6f662d7468652d636f6d6d616e642d6c696e65732d7465737473\n": "746f6b656e2d6f662d7468652d636f6d6d616e642d6c696e65732d7465737473",
		"fs_a.B-1~x\n": "fs_a.B-1~x",
		"fs_a.B-1~x":   "fs_a.B-1~x",
	} {
		if err := os.WriteFile(path, []byte(written), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := Token(); err != nil || got != want {
			t.Errorf("%q is read as %q (%v), want %q", written, got, err, want)
		}
	}
	for _, written := range []string{"", "\n", "two words\n", "a\nb\n", "a\x00b\n"} {
		if err := os.WriteFile(path, []byte(written), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := Token(); err == nil {
			t.Errorf("%q is read as the token %q", written, got)
		}
	}
}

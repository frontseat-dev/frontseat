package pins_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/frontseat-dev/frontseat/server/internal/pins"
)

const forConfig = `[tools]
go = "1.27.3"
"http:rust" = { version = "1.99.1", url = "https://example.com/rust.tar.xz" }
node = "24.1.0"

[tool_alias]
rust = "http:rust"
nodejs = "node"

[env]
GOFLAGS = "-trimpath"
FLAGS = { value = "{% if tools['http:rust'] %}-L {{ tools['http:rust'].path }}{% endif %}", tools = true }

[settings]
experimental = true

[tasks.build]
run = "go build ./..."
`

const forLock = `lockfile_version = 3

[[tools.go]]
version = "1.27.3"
backend = "core:go"

[tools.go."platforms.linux-arm64"]
checksum = "sha256:aa"
url = "https://example.com/go.tar.gz"

[[tools."http:rust"]]
version = "1.99.1"
backend = "http:rust"

[tools."http:rust"."platforms.linux-arm64"]
checksum = "sha256:bb"
conda_deps = ["libzlib"]

[[tools.node]]
version = "24.1.0"
backend = "core:node"

[tools.node."platforms.linux-arm64"]
checksum = "sha256:cc"
conda_deps = ["openssl"]

[conda-packages.linux-arm64.libzlib]
checksum = "sha256:dd"

[conda-packages.linux-arm64.openssl]
checksum = "sha256:ee"
`

// An action is given the pins of the tools it names, with what mise reads
// when it runs them, and nothing of other tools or of the tasks.
func TestAnActionIsGivenThePinsOfItsOwnTools(t *testing.T) {
	gotConfig, gotLock, err := pins.For([]byte(forConfig), []byte(forLock), []string{"go", "rust"})
	if err != nil {
		t.Fatal(err)
	}
	var c, l map[string]any
	if err := toml.Unmarshal(gotConfig, &c); err != nil {
		t.Fatalf("%v:\n%s", err, gotConfig)
	}
	if err := toml.Unmarshal(gotLock, &l); err != nil {
		t.Fatalf("%v:\n%s", err, gotLock)
	}
	tools := c["tools"].(map[string]any)
	if _, there := tools["go"]; !there || tools["http:rust"] == nil || tools["node"] != nil {
		t.Errorf("tools = %v; want go and http:rust alone", tools)
	}
	if aliases := c["tool_alias"].(map[string]any); aliases["rust"] != "http:rust" || aliases["nodejs"] != nil {
		t.Errorf("aliases = %v; want rust's alone", aliases)
	}
	if env := c["env"].(map[string]any); env["GOFLAGS"] != "-trimpath" || env["FLAGS"] == nil {
		t.Errorf("env = %v; want it whole", env)
	}
	if c["settings"] == nil || c["tasks"] != nil {
		t.Errorf("settings %v, tasks %v; want the settings and no task", c["settings"], c["tasks"])
	}
	locked := l["tools"].(map[string]any)
	if locked["go"] == nil || locked["http:rust"] == nil || locked["node"] != nil || l["lockfile_version"] == nil {
		t.Errorf("lock = %v; want go and http:rust alone, with its version", l)
	}
	packages := l["conda-packages"].(map[string]any)["linux-arm64"].(map[string]any)
	if packages["libzlib"] == nil || packages["openssl"] != nil {
		t.Errorf("packages = %v; want rust's alone", packages)
	}
	// What the tools are locked as is as the lock says.
	if err := pins.Locked(gotLock, "rust", "linux-arm64"); err != nil {
		t.Errorf("the narrowed lock does not lock rust: %v", err)
	}
}

// Pins that differ in what an action does not run with give it the same
// bytes; pins that differ in what it does give it others.
func TestAnActionsPinsChangeOnlyWithItsTools(t *testing.T) {
	given := func(config, lock string) string {
		t.Helper()
		c, l, err := pins.For([]byte(config), []byte(lock), []string{"go"})
		if err != nil {
			t.Fatal(err)
		}
		return string(c) + "\x00" + string(l)
	}
	before := given(forConfig, forLock)
	for i := 0; i < 20; i++ {
		if again := given(forConfig, forLock); again != before {
			t.Fatalf("the same pins gave other bytes:\n%s\n%s", before, again)
		}
	}
	for name, changed := range map[string][2]string{
		"another task":       {forConfig + "\n[tasks.test]\nrun = \"go test ./...\"\n", forLock},
		"another tool's pin": {strings.Replace(forConfig, `node = "24.1.0"`, `node = "24.2.0"`, 1), strings.Replace(forLock, `"sha256:cc"`, `"sha256:ff"`, 1)},
		"the comments":       {"# a comment\n" + forConfig, forLock},
	} {
		if got := given(changed[0], changed[1]); got != before {
			t.Errorf("%s changed what an action of go is given", name)
		}
	}
	for name, changed := range map[string][2]string{
		"the go pin":  {strings.Replace(forConfig, `go = "1.27.3"`, `go = "1.27.4"`, 1), forLock},
		"go's lock":   {forConfig, strings.Replace(forLock, `"sha256:aa"`, `"sha256:a0"`, 1)},
		"the env":     {strings.Replace(forConfig, `"-trimpath"`, `"-race"`, 1), forLock},
		"the setting": {strings.Replace(forConfig, "experimental = true", "experimental = false", 1), forLock},
	} {
		if got := given(changed[0], changed[1]); got == before {
			t.Errorf("%s did not change what an action of go is given", name)
		}
	}
	if c, _, _ := pins.For([]byte(forConfig), []byte(forLock), []string{"go"}); bytes.Contains(c, []byte("node")) {
		t.Errorf("node's pins reach an action of go:\n%s", c)
	}
}

// What is kept is as the repository wrote it, in its order: mise reads an
// environment in order, and a variable may be made of one before it.
func TestAnActionsPinsKeepTheRepositorysOrder(t *testing.T) {
	config := `# the repository's tools
[tools]
node = "24.1.0"
go = "1.27.3" # the compiler
tools.jq = "1.7.1"

[env]
_.path = ["./bin"]
ZED = "base"
ALPHA = "{{ env.ZED }}/more"
NOTE = """
# not a comment, but the end of a string"""

[tasks.build]
run = "go build"
`
	got, _, err := pins.For([]byte(config), []byte(forLock), []string{"go"})
	if err != nil {
		t.Fatal(err)
	}
	want := `[tools]
go = "1.27.3"
[env]
_.path = ["./bin"]
ZED = "base"
ALPHA = "{{ env.ZED }}/more"
NOTE = """
# not a comment, but the end of a string"""
`
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	var parsed map[string]any
	if err := toml.Unmarshal(got, &parsed); err != nil {
		t.Errorf("what an action is given is no TOML: %v", err)
	}
}

// A mise.toml that is no TOML is refused, whatever is left out of it.
func TestAMiseTomlThatIsNoTomlIsRefused(t *testing.T) {
	broken := "[tools]\nnode = \"1\"\ngo = \"1.27.3\"\n\n[tools.node.x]\ny = 1\n"
	if _, _, err := pins.For([]byte(broken), []byte(forLock), []string{"go"}); err == nil {
		t.Error("a mise.toml that is no TOML was cut")
	}
	// The control: the same without the conflict.
	sound := "[tools]\nnode = \"1\"\ngo = \"1.27.3\"\n"
	if _, _, err := pins.For([]byte(sound), []byte(forLock), []string{"go"}); err != nil {
		t.Errorf("a sound mise.toml was refused: %v", err)
	}
}

// An [env] that is said once for its repository's actions is given; one
// that reads a file of the repository, or names the directory mise is
// asked in, is refused by name, where mise would lose it without a word.
func TestAnEnvironmentThatCannotBeSaidOnceForItsRepositoryIsRefused(t *testing.T) {
	pinned := "[tools]\ngo = \"1.27.3\"\n\n"
	for name, env := range map[string]string{
		"a value":                    "[env]\nA = \"a\"\n",
		"a directory on the path":    "[env]\n_.path = [\"./bin\"]\n",
		"the pins' directory":        "[env]\nA = \"{{config_root}}/x\"\n",
		"the action's own variable":  "[env]\nA = \"{{env.HOME}}/x\"\n",
		"a word that only reads cwd": "[env]\nA = \"cwd\"\n",
		"a variable unset":           "[env]\nA = false\n",
		"a variable named cwd":       "[env]\nA = \"{{ env.cwd }}\"\n",
		"a variable that begins so":  "[vars]\ncwd_x = \"y\"\n[env]\nA = \"{{ vars.cwd_x }}\"\n",
		"cwd written out raw":        "[env]\nA = \"{% raw %}{{ cwd }}{% endraw %}\"\n",
	} {
		if _, _, err := pins.For([]byte(pinned+env), []byte(forLock), []string{"go"}); err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
	}
	for name, env := range map[string]string{
		"a file read":                      "[env]\n_.file = \".env\"\n",
		"a script sourced":                 "[env]\n_.source = \"env.sh\"\n",
		"the directory asked in":           "[env]\nA = \"{{cwd}}/x\"\n",
		"the directory asked in, spaced":   "[env]\nA = \"{{ cwd }}/x\"\n",
		"a block of the directory":         "[env]\nA = \"{% if cwd %}x{% endif %}\"\n",
		"a file read in a list":            "[[env]]\nA = \"a\"\n[[env]]\n_.file = \".env\"\n",
		"the directory asked in, as a var": "[vars]\nhere = \"{{cwd}}\"\n[env]\nA = \"{{vars.here}}\"\n",
		"a file read by a template":        "[env]\nA = \"{{ read_file(path='VERSION') }}\"\n",
		"a command run by a template":      "[env]\nA = \"{{ exec(command='cat VERSION') }}\"\n",
		"the directory behind a brace":     "[env]\nA = \"{{ '}' ~ cwd }}\"\n",
		"the directory, filtered":          "[env]\nA = \"{{cwd|upper}}\"\n",
		"an env file":                      "env_file = \".env\"\n",
		"a dotenv":                         "dotenv = \".env\"\n",
		"an env file setting":              "[settings]\nenv_file = \".env\"\n",
	} {
		// What it says first, as a key at the top of a file is.
		if _, _, err := pins.For([]byte(env+"\n"+pinned), []byte(forLock), []string{"go"}); err == nil {
			t.Errorf("%s was taken", name)
		}
	}
}

// What a mise.toml sets to false, alone or as a value of its own, it
// unsets; true and text are values; a list of [env] is read in order.
func TestWhatAMiseTomlUnsetsIsKnownByName(t *testing.T) {
	got, err := pins.Unset([]byte("[env]\nA = false\nB = { value = false }\nC = true\nD = \"x\"\n_.path = [\"./bin\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "A B" {
		t.Errorf("unset %v, want A B", got)
	}
	listed, err := pins.Unset([]byte("[[env]]\nE = false\n[[env]]\nF = \"y\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(listed, " ") != "E" {
		t.Errorf("unset %v, want E", listed)
	}
}

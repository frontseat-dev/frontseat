package settings_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/daemon/build"
	"github.com/frontseat-dev/frontseat/build/daemon/settings"
	"github.com/frontseat-dev/frontseat/testsupport/testauthority"
	"github.com/frontseat-dev/frontseat/testsupport/testmodule"
)

// sound is a settings file that says everything one may, of files that
// are there: %s is the directory they are in.
const sound = `version: 1
plugins: plugins
grid:
  address: grid.example:8980
  instance: frontseat/p/b
  assets: assets.example:8983
  authorities: ca.pem
  token: token
  rewrites:
    - from: '^https://static\.crates\.io/crates/([^/]+)/([^/]+)/download$'
      to: 'http://mirror:8080/cargo/$1/$2/download'
    - from: '^https://b c/(.*)$'
      to: 'http://other/$1'
platform:
  OSFamily: linux
  ISA: x86-64
  container-image: docker://frontseat.dev/workers/v1
publish:
  environment: [GH_TOKEN, NPM_TOKEN]
`

// laid lays out a directory holding what the sound settings name, two
// plugins, the authorities and a token, and returns it.
func laid(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	known, err := os.ReadFile(testauthority.New(t).File)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"plugins/go.wasm": "go", "plugins/cargo.wasm": "cargo", "plugins/README": "no plugin", "ca.pem": string(known), "token": "aaa.bbb.ccc\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// read writes a settings file into a directory and reads it.
func read(t *testing.T, dir, content string) (settings.Settings, error) {
	t.Helper()
	file := filepath.Join(dir, "build.yaml")
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return settings.Read(file)
}

// A build daemon is told its plugins, its grid, its workers' platform
// and what a publish may be given in one file, read when it starts. A
// path in it is from the file's own directory, and the plugins are every
// <name>.wasm of the directory it names, by name.
func TestABuildDaemonsSettingsAreReadFromOneFile(t *testing.T) {
	dir := laid(t)
	got, err := read(t, dir, sound)
	if err != nil {
		t.Fatal(err)
	}
	want := settings.Settings{
		Plugins: []string{filepath.Join(dir, "plugins", "cargo.wasm"), filepath.Join(dir, "plugins", "go.wasm")},
		Grid: build.Grid{Address: "grid.example:8980", Instance: "frontseat/p/b", Assets: "assets.example:8983", TLS: true, Authorities: filepath.Join(dir, "ca.pem"), Token: filepath.Join(dir, "token"),
			Rewrites: []build.Rewrite{{From: `^https://static\.crates\.io/crates/([^/]+)/([^/]+)/download$`, To: "http://mirror:8080/cargo/$1/$2/download"}, {From: `^https://b c/(.*)$`, To: "http://other/$1"}}},
		Platform: map[string]string{"OSFamily": "linux", "ISA": "x86-64", "container-image": "docker://frontseat.dev/workers/v1"},
		Given:    []string{"GH_TOKEN", "NPM_TOKEN"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read\n%+v\nwant\n%+v", got, want)
	}
}

// Settings that say only what they must, the plugins and the grid's
// address, ask the grid in the clear for Linux on this machine's
// architecture, and give a publish nothing: as the local stack's grid is
// asked.
func TestSettingsThatSayOnlyWhatTheyMustAskTheGridPlainly(t *testing.T) {
	got, err := read(t, laid(t), "version: 1\nplugins: plugins\ngrid:\n  address: localhost:8980\n")
	if err != nil {
		t.Fatal(err)
	}
	if got.Grid.TLS || got.Grid.Token != "" || got.Grid.Assets != "" || len(got.Given) != 0 {
		t.Errorf("told nothing more, the grid is asked as %+v and a publish given %v", got.Grid, got.Given)
	}
	if got.Platform["OSFamily"] != "linux" || got.Platform["ISA"] == "" || len(got.Platform) != 2 {
		t.Errorf("told no platform: %v; want Linux on this machine's architecture", got.Platform)
	}
}

// The file is read strictly, and what is wrong with it is found when
// the daemon starts, not at the first build: a key it does not know,
// another version, a second document, a rewrite that is no expression or
// is given twice, a platform's property twice, plugins that are not
// there, a grid that could not be asked so. The control is the sound
// file, which TestABuildDaemonsSettingsAreReadFromOneFile reads.
func TestSettingsThatAreWrongAreRefusedWhenTheyAreRead(t *testing.T) {
	for name, change := range map[string][2]string{
		"a key that is none":                  {"version: 1\n", "version: 1\nlisten: 0.0.0.0:7600\n"},
		"a key of the grid's that is none":    {"  instance: frontseat/p/b\n", "  instance: frontseat/p/b\n  proxy: http://proxy\n"},
		"another version":                     {"version: 1\n", "version: 2\n"},
		"no version":                          {"version: 1\n", ""},
		"a second document":                   {"publish:\n", "---\npublish:\n"},
		"no grid":                             {"  address: grid.example:8980\n", ""},
		"no plugins":                          {"plugins: plugins\n", ""},
		"a directory of plugins not there":    {"plugins: plugins\n", "plugins: elsewhere\n"},
		"a rewrite that is no expression":     {`from: '^https://b c/(.*)$'`, `from: '^(https://b c/(.*)$'`},
		"a rewrite given twice":               {`from: '^https://b c/(.*)$'`, `from: '^https://static\.crates\.io/crates/([^/]+)/([^/]+)/download$'`},
		"a rewrite to nowhere":                {"      to: 'http://other/$1'\n", ""},
		"a property given twice":              {"  ISA: x86-64\n", "  ISA: x86-64\n  ISA: aarch64\n"},
		"a token and no authorities":          {"  authorities: ca.pem\n", ""},
		"a token's file that is not there":    {"  token: token\n", "  token: missing\n"},
		"a variable that is no name":          {"[GH_TOKEN, NPM_TOKEN]", "[GH_TOKEN, 'NPM TOKEN']"},
		"a variable given twice":              {"[GH_TOKEN, NPM_TOKEN]", "[GH_TOKEN, GH_TOKEN]"},
		"a platform that is no properties":    {"platform:\n", "platform: linux\nextra:\n"},
		"plugins that are a list, not a path": {"plugins: plugins\n", "plugins: [plugins]\n"},
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(sound, change[0]) {
				t.Fatalf("the settings hold no %q to change", change[0])
			}
			if got, err := read(t, laid(t), strings.Replace(sound, change[0], change[1], 1)); err == nil {
				t.Errorf("it was read: %+v", got)
			}
		})
	}
	dir := laid(t)
	if err := os.Remove(filepath.Join(dir, "plugins", "go.wasm")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "plugins", "cargo.wasm")); err != nil {
		t.Fatal(err)
	}
	if got, err := read(t, dir, sound); err == nil || !strings.Contains(err.Error(), ".wasm") {
		t.Errorf("a directory of plugins that holds none: %+v, %v; want it refused, saying so", got, err)
	}
	if _, err := settings.Read(filepath.Join(dir, "absent.yaml")); err == nil {
		t.Error("settings that are not there were read")
	}
}

// The page that says what a build daemon is told shows one example, and
// it is one the reader takes, beside the files it names: a page that
// showed what is refused would teach a mistake. The control is the same
// example with a key that is none, which is refused.
func TestTheExampleOfTheSettingsPageIsRead(t *testing.T) {
	root, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(filepath.Join(root, "docs", "build-settings.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, after, found := strings.Cut(string(page), "```yaml\n")
	example, _, ended := strings.Cut(after, "```\n")
	if !found || !ended {
		t.Fatal("the page shows no example fenced ```yaml")
	}
	example = strings.NewReplacer("/opt/frontseat/plugins", "plugins", "/etc/fleet-ca/ca.crt", "ca.pem", "/run/secrets/grid-token", "token").Replace(example)
	if _, err := read(t, laid(t), example); err != nil {
		t.Errorf("the page's example is refused: %v", err)
	}
	if _, err := read(t, laid(t), example+"listen: 0.0.0.0:7600\n"); err == nil {
		t.Error("the control: the example with a key that is none was read")
	}
}

// A directory of plugins loads every <name>.wasm in it, after the
// plugins given one by one, and nothing else it holds; one that holds
// none is refused by name. The control is a daemon given no directory,
// which loads what it was given alone.
func TestADirectoryOfPluginsLoadsEveryModuleInIt(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"go.wasm", "buf.wasm", "README.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "not-a-plugin.wasm"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := settings.Modules([]string{"/opt/one.wasm"}, dir)
	if want := []string{"/opt/one.wasm", filepath.Join(dir, "buf.wasm"), filepath.Join(dir, "go.wasm")}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("plugins = %v, %v; want %v", got, err, want)
	}
	if got, err := settings.Modules([]string{"/opt/one.wasm"}, ""); err != nil || !reflect.DeepEqual(got, []string{"/opt/one.wasm"}) {
		t.Errorf("with no directory: %v, %v; want the one plugin given", got, err)
	}
	empty := t.TempDir()
	if _, err := settings.Modules(nil, empty); err == nil || !strings.Contains(err.Error(), "holds no <name>.wasm") {
		t.Errorf("a directory of no plugins: %v, want it refused by name", err)
	}
}

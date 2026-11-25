package settings_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/daemon/build"
	"github.com/frontseat-dev/frontseat/build/daemon/settings"
	"github.com/frontseat-dev/frontseat/common/record/buildfile"
	"github.com/frontseat-dev/frontseat/testsupport/testauthority"
	"github.com/frontseat-dev/frontseat/testsupport/testmodule"
)

// sound is a settings file that says everything one may, of files that
// are there: %s is the directory they are in.
const sound = `version: 1
plugins: plugins
grid:
  address: grid.example:8980
  instance: frontseat/b
  reads: [frontseat/p, frontseat/q]
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
// <name>.wasm of the directory it names, by name, with the directory
// they were listed from.
func TestABuildDaemonsSettingsAreReadFromOneFile(t *testing.T) {
	dir := laid(t)
	got, err := read(t, dir, sound)
	if err != nil {
		t.Fatal(err)
	}
	want := settings.Settings{
		Plugins: []string{filepath.Join(dir, "plugins", "cargo.wasm"), filepath.Join(dir, "plugins", "go.wasm")}, PluginDirectory: filepath.Join(dir, "plugins"),
		Grid: build.Grid{Address: "grid.example:8980", Instance: "frontseat/b", Reads: []string{"frontseat/p", "frontseat/q"}, Assets: "assets.example:8983", TLS: true, Authorities: filepath.Join(dir, "ca.pem"), Token: filepath.Join(dir, "token"),
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

// The grid is asked over TLS where the settings say so, by the system's
// authorities, or name the authorities it is known by; told neither, it
// is asked in the clear.
func TestTheGridIsAskedOverTLSWhereTheSettingsSaySoOrNameAuthorities(t *testing.T) {
	plain := "version: 1\nplugins: plugins\ngrid:\n  address: grid.example:8980\n"
	for name, said := range map[string]struct {
		more        string
		tls         bool
		authorities string
	}{
		"tls alone":         {"  tls: true\n", true, ""},
		"authorities alone": {"  authorities: ca.pem\n", true, "ca.pem"},
		"tls said false":    {"  tls: false\n", false, ""},
		"neither":           {"", false, ""},
	} {
		dir := laid(t)
		got, err := read(t, dir, plain+said.more)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := said.authorities
		if want != "" {
			want = filepath.Join(dir, want)
		}
		if got.Grid.TLS != said.tls || got.Grid.Authorities != want {
			t.Errorf("told %s, the grid is asked over TLS: %t, by the authorities %q; want %t, by %q", name, got.Grid.TLS, got.Grid.Authorities, said.tls, want)
		}
	}
}

// The file is read strictly, and what is wrong with it is found when
// the daemon starts, not at the first build, and said: a key it does not
// know, another version, a second document, a rewrite that is no
// expression or is given twice, a platform's property twice, plugins
// that are not there, a grid that could not be asked so: a token is
// never sent in the clear nor to a grid known by the system's
// authorities, which the environment chooses, and a file named is one
// the daemon can read. It is written plainly: what has a value read
// elsewhere than where it is written, an anchor, a merge key, a tag, or
// as another kind than it is, a truth or a null taken for a text, is
// refused by name, and an address is a host and a port. Each is one
// mistake, refused for what it is. No refusal repeats the token. The
// control is the sound file, which
// TestABuildDaemonsSettingsAreReadFromOneFile reads.
func TestSettingsThatAreWrongAreRefusedWhenTheyAreRead(t *testing.T) {
	for name, change := range map[string][3]string{
		"a key that is none":                    {"version: 1\n", "version: 1\nlisten: 0.0.0.0:7600\n", "field listen not found"},
		"a key of the grid's that is none":      {"  instance: frontseat/b\n", "  instance: frontseat/b\n  proxy: http://proxy\n", "field proxy not found"},
		"another version":                       {"version: 1\n", "version: 2\n", "version is 2"},
		"no version":                            {"version: 1\n", "", "version is 0"},
		"a version written as a fraction":       {"version: 1\n", "version: 1.0\n", "version is an integer"},
		"a version written in sixteens":         {"version: 1\n", "version: 0x1\n", "version is an integer"},
		"a second document":                     {"publish:\n", "---\npublish:\n", "hold another"},
		"the mark that ends a document":         {"  environment: [GH_TOKEN, NPM_TOKEN]\n", "  environment: [GH_TOKEN, NPM_TOKEN]\n...\n", "ends a document"},
		"an anchor":                             {"  instance: frontseat/b\n", "  instance: &named frontseat/b\n", "an anchor or an alias"},
		"an alias":                              {"  assets: assets.example:8983\n", "  assets: *named\n", "unknown anchor"},
		"a merge key":                           {"  OSFamily: linux\n", "  <<: {OSFamily: linux}\n", "a merge key"},
		"a tag":                                 {"  instance: frontseat/b\n", "  instance: !!binary ZnJvbnRzZWF0\n", "a tag is not read"},
		"no grid":                               {"  address: grid.example:8980\n  instance: frontseat/b\n  reads: [frontseat/p, frontseat/q]\n", "", "grid.address is not said"},
		"instances and no grid":                 {"  address: grid.example:8980\n", "", "grid.address names none"},
		"an address that is a truth":            {"  address: grid.example:8980\n", "  address: true\n", "grid.address is written as what is read as bool"},
		"an address with a space":               {"  address: grid.example:8980\n", "  address: 'grid example:8980'\n", "grid.address is no address"},
		"an address with a line":                {"  address: grid.example:8980\n", "  address: \"grid.example:8980\\nx\"\n", "grid.address holds a control character"},
		"an address of no port":                 {"  address: grid.example:8980\n", "  address: grid.example\n", "grid.address is no address"},
		"an address that is a socket":           {"  address: grid.example:8980\n", "  address: unix:///run/grid\n", "grid.address is no address"},
		"an asset service of no port":           {"  assets: assets.example:8983\n", "  assets: assets.example\n", "grid.assets is no address"},
		"an instance with a space":              {"  instance: frontseat/b\n", "  instance: frontseat p\n", "grid.instance holds a space"},
		"an instance read with a space":         {"[frontseat/p, frontseat/q]", "[frontseat/p, 'frontseat q']", "grid.reads' entry 2 holds a space"},
		"an instance read of no name":           {"[frontseat/p, frontseat/q]", "[frontseat/p, '']", "grid.reads' entry 2 names no instance"},
		"an instance read of a word kept":       {"[frontseat/p, frontseat/q]", "[frontseat/p, frontseat/blobs]", "grid.reads' entry 2 has a part that is a word the Remote Execution API keeps"},
		"an instance of a word kept":            {"  instance: frontseat/b\n", "  instance: frontseat/uploads\n", "grid.instance has a part that is a word the Remote Execution API keeps"},
		"an instance of two slashes":            {"  instance: frontseat/b\n", "  instance: frontseat//b\n", "grid.instance is parted by one slash"},
		"an instance read that is nothing":      {"[frontseat/p, frontseat/q]", "[frontseat/p, ~]", "grid.reads is written as what is read as null"},
		"an instance read twice":                {"[frontseat/p, frontseat/q]", "[frontseat/p, frontseat/p]", "grid.reads' entry 2 is its entry 1 again"},
		"its own instance read":                 {"[frontseat/p, frontseat/q]", "[frontseat/p, frontseat/b]", "grid.reads' entry 2 is the daemon's own instance"},
		"instances read that are one word":      {"[frontseat/p, frontseat/q]", "frontseat/p", "into []string"},
		"no plugins":                            {"plugins: plugins\n", "", "plugins names no directory"},
		"a directory of plugins not there":      {"plugins: plugins\n", "plugins: elsewhere\n", "the directory of plugins"},
		"a rewrite that is no expression":       {`from: '^https://b c/(.*)$'`, `from: '^(https://b c/(.*)$'`, "error parsing regexp"},
		"a rewrite given twice":                 {`from: '^https://b c/(.*)$'`, `from: '^https://static\.crates\.io/crates/([^/]+)/([^/]+)/download$'`, "a rewrite is given twice"},
		"a rewrite to nowhere":                  {"      to: 'http://other/$1'\n", "", "one of them is not said"},
		"a rewrite of nothing":                  {"    - from: '^https://b c/(.*)$'\n      to:", "    - to:", "one of them is not said"},
		"a property given twice":                {"  ISA: x86-64\n", "  ISA: x86-64\n  ISA: aarch64\n", "already defined"},
		"a property of no name":                 {"  ISA: x86-64\n", "  '': x86-64\n", "a key of no name"},
		"a property that is nothing":            {"  ISA: x86-64\n", "  ISA: ~\n", "platform.ISA is written as what is read as null"},
		"a property that is a number":           {"  ISA: x86-64\n", "  ISA: 64\n", "platform.ISA is written as what is read as int"},
		"a token in the clear":                  {"  authorities: ca.pem\n", "", "never sent in the clear"},
		"a token, and the system's authorities": {"  authorities: ca.pem\n", "  tls: true\n", "known by authorities the daemon was named"},
		"tls that is no truth":                  {"  authorities: ca.pem\n", "  authorities: ca.pem\n  tls: 1\n", "grid.tls is true or false"},
		"a token's file that is not there":      {"  token: token\n", "  token: missing\n", "the grid token"},
		"authorities that are not there":        {"  authorities: ca.pem\n  token: token\n", "  authorities: missing\n", "the grid's authorities"},
		"authorities that hold none":            {"  authorities: ca.pem\n  token: token\n", "  authorities: plugins/README\n", "holds no certificate"},
		"a token where authorities fail":        {"  authorities: ca.pem\n", "  authorities: plugins/README\n", "holds no certificate"},
		"a variable that is no name":            {"[GH_TOKEN, NPM_TOKEN]", "[GH_TOKEN, 'NPM TOKEN']", "is no variable's name"},
		"a variable given twice":                {"[GH_TOKEN, NPM_TOKEN]", "[GH_TOKEN, GH_TOKEN]", "names GH_TOKEN twice"},
		"a variable that is nothing":            {"[GH_TOKEN, NPM_TOKEN]", "[~, NPM_TOKEN]", "publish.environment is written as what is read as null"},
		"a platform that is no properties":      {"platform:\n  OSFamily: linux\n  ISA: x86-64\n  container-image: docker://frontseat.dev/workers/v1\n", "platform: linux\n", "cannot construct !!str `linux` into map"},
		"plugins that are a list, not a path":   {"plugins: plugins\n", "plugins: [plugins]\n", "cannot construct !!seq into string"},
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(sound, change[0]) {
				t.Fatalf("the settings hold no %q to change", change[0])
			}
			if got, err := read(t, laid(t), strings.Replace(sound, change[0], change[1], 1)); err == nil {
				t.Errorf("it was read: %+v", got)
			} else if strings.Contains(err.Error(), "aaa.bbb.ccc") {
				t.Errorf("the refusal repeats the token: %v", err)
			} else if !strings.Contains(err.Error(), change[2]) {
				t.Errorf("it was refused for something else: %v; want it to say %q", err, change[2])
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
	// A file of more than a mebibyte is not read, and one of just that is.
	padding := strings.Repeat("# said for nothing\n", (buildfile.Limit-len(sound))/19)
	padding += strings.Repeat("#", buildfile.Limit-len(sound)-len(padding)-1) + "\n"
	if _, err := read(t, laid(t), sound+padding); err != nil {
		t.Errorf("the control: settings of a mebibyte: %v", err)
	}
	if _, err := read(t, laid(t), sound+padding+"#"); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Errorf("settings of more than a mebibyte: %v; want them refused, saying the bound", err)
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

// A directory of plugins loads every <name>.wasm in it, by name, and
// nothing else it holds; one that holds none is refused by name. A
// <name>.wasm that is no file of its own, a link or a directory, is
// refused by name too, and never left out in silence: the daemon would
// build with fewer plugins than it was pointed at.
func TestADirectoryOfPluginsLoadsEveryModuleInIt(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"go.wasm", "buf.wasm", "README.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "not-a-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := settings.Modules(dir)
	if want := []string{filepath.Join(dir, "buf.wasm"), filepath.Join(dir, "go.wasm")}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("plugins = %v, %v; want %v", got, err, want)
	}
	empty := t.TempDir()
	if _, err := settings.Modules(empty); err == nil || !strings.Contains(err.Error(), "holds no <name>.wasm") {
		t.Errorf("a directory of no plugins: %v, want it refused by name", err)
	}
	for name, make := range map[string]func(string) error{
		"a directory": func(path string) error { return os.Mkdir(path, 0o755) },
		"a link":      func(path string) error { return os.Symlink(filepath.Join(dir, "go.wasm"), path) },
	} {
		odd := filepath.Join(dir, "odd.wasm")
		if err := make(odd); err != nil {
			t.Fatal(err)
		}
		if got, err := settings.Modules(dir); err == nil || !strings.Contains(err.Error(), odd) {
			t.Errorf("with a plugin that is %s: %v, %v; want it refused, naming %s", name, got, err, odd)
		}
		if err := os.Remove(odd); err != nil {
			t.Fatal(err)
		}
	}
}

// What a box's supervisor writes, the file's shape and no more, is what
// a daemon reads: the grid, its assets, the platform and the plugins it
// was told, with nothing said of how the grid is asked. The control is
// the same file with no grid, which no daemon can be told.
func TestWhatIsWrittenAsTheFilesShapeIsReadAsSettings(t *testing.T) {
	dir := laid(t)
	file := filepath.Join(dir, "build.yaml")
	written := buildfile.File{Plugins: filepath.Join(dir, "plugins"), Grid: buildfile.Grid{Address: "grid:8980", Assets: "grid:8983"}, Platform: map[string]string{"OSFamily": "linux", "ISA": "aarch64"}}
	if err := buildfile.Write(file, written); err != nil {
		t.Fatal(err)
	}
	got, err := settings.Read(file)
	if err != nil {
		t.Fatal(err)
	}
	if got.Grid.Address != "grid:8980" || got.Grid.Assets != "grid:8983" || got.Grid.TLS || got.Platform["ISA"] != "aarch64" || len(got.Plugins) != 2 {
		t.Errorf("read %+v; want what was written", got)
	}
	written.Grid = buildfile.Grid{}
	if err := buildfile.Write(file, written); err != nil {
		t.Fatal(err)
	}
	if got, err := settings.Read(file); err == nil {
		t.Errorf("the control: settings of no grid were read: %+v", got)
	}
}

// The authorities the settings name are read once, by whoever reads the
// settings and identifies the daemon by what it read: the grid is
// checked against the very bytes it is then known by, and the file is
// not read again to check it. Here the file holds no certificate and
// what was read of it is the authorities, which are taken; and the file
// holds the authorities and what was read of it holds none, which is
// refused. Control: given no reading of them, the file is read, and its
// holding none is refused.
func TestTheGridIsCheckedAgainstTheAuthoritiesAsTheyWereRead(t *testing.T) {
	dir := laid(t)
	known, err := os.ReadFile(filepath.Join(dir, "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "build.yaml")
	raw := []byte(strings.Replace(sound, "  authorities: ca.pem\n", "  authorities: plugins/README\n", 1))
	readAs := func(content []byte) func(string) ([]byte, error) {
		return func(string) ([]byte, error) { return content, nil }
	}
	got, err := settings.Parse(raw, file, readAs(known))
	if err != nil || string(got.Grid.Known) != string(known) {
		t.Errorf("the file holding none, read as the authorities: known by %d bytes (%v); want the bytes read, checked", len(got.Grid.Known), err)
	}
	if _, err := settings.Parse([]byte(sound), file, readAs([]byte("no certificate\n"))); err == nil || !strings.Contains(err.Error(), "holds no certificate") {
		t.Errorf("the file holding the authorities, read as none: %v; want the bytes read refused", err)
	}
	if _, err := settings.Parse(raw, file, nil); err == nil || !strings.Contains(err.Error(), "holds no certificate") {
		t.Errorf("the control, given no reading of them: %v; want the file read and refused", err)
	}
}

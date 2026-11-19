package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTheWorkersPlatformIsWhatTheDaemonIsTold(t *testing.T) {
	image := "docker://example.com/runner@sha256:0000"
	got, err := platformOf([]string{"OSFamily=linux", "container-image=" + image})
	if want := map[string]string{"OSFamily": "linux", "container-image": image}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("platform = %v, %v; want %v", got, err, want)
	}
	// The control: a daemon told nothing asks for Linux on this machine's
	// architecture.
	if got, err := platformOf(nil); err != nil || got["OSFamily"] != "linux" || got["ISA"] == "" {
		t.Errorf("told nothing: %v, %v; want Linux on this machine's architecture", got, err)
	}
}

func TestAPlatformThatIsNotPropertiesIsRefused(t *testing.T) {
	for name, flags := range map[string][]string{
		"a flag with no value":   {"OSFamily"},
		"a flag with no name":    {"=linux"},
		"a property given twice": {"ISA=aarch64", "ISA=x86-64"},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := platformOf(flags); err == nil {
				t.Errorf("platform = %v; want it refused", got)
			}
		})
	}
}

// Which proxy stands in for a download's site is read strictly: a flag
// that is not a pattern, a space and an address is refused at the
// start, not found out at the first download.
func TestTheDownloadRewritesAreReadStrictly(t *testing.T) {
	rewrites, err := rewritesOf([]string{`^https://a\.example/(.*)$ http://proxy/$1`, `^https://b c/(.*)$ http://other/$1`})
	if err != nil || rewrites[`^https://a\.example/(.*)$`] != "http://proxy/$1" || rewrites[`^https://b c/(.*)$`] != "http://other/$1" {
		t.Fatalf("sound rewrites: %v, %v", rewrites, err)
	}
	if rewrites, err := rewritesOf(nil); err != nil || rewrites != nil {
		t.Errorf("nothing told: %v, %v; want no rewrites and no error", rewrites, err)
	}
	for name, given := range map[string][]string{
		"no address":             {`^https://a\.example/(.*)$`},
		"an address and nothing": {` http://proxy/`},
		"a space and nothing":    {`^a$ `},
		"no regular expression":  {`^(a$ http://proxy/`},
		"a pattern given twice":  {`^a$ http://one/`, `^a$ http://two/`},
	} {
		if _, err := rewritesOf(given); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// A sandbox holds one daemon: the first to start there holds the
// sandbox's one lock, and another is refused, saying which workspace is
// managed already, whatever directory, port or state it was pointed at.
// The control is the daemon that starts once the first is gone.
func TestASandboxHoldsOneDaemon(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "kept", "daemon.lock")
	first, err := sole(lock, "payments")
	if err != nil {
		t.Fatalf("the first daemon of a sandbox was refused: %v", err)
	}
	// A second is refused whatever it was told, a name or none.
	for _, told := range []string{"payments", "", "another"} {
		if _, err := sole(lock, told); err == nil || !strings.Contains(err.Error(), "payments") || !strings.Contains(err.Error(), "one daemon") {
			t.Fatalf("a second daemon in the sandbox, told %q: %v, want it refused, naming the workspace", told, err)
		}
	}
	first()
	again, err := sole(lock, "")
	if err != nil {
		t.Fatalf("a daemon started once the first was gone was refused: %v", err)
	}
	if _, err := sole(lock, "payments"); err == nil || !strings.Contains(err.Error(), "one daemon") {
		t.Fatalf("a daemon beside one told no workspace: %v, want it refused", err)
	}
	again()
}

// The daemon stands in its workspace: its working directory is the
// workspace's, so that what it starts begins there, and what it was told
// by a path beside where it was started is still found.
func TestTheDaemonStandsInItsWorkspace(t *testing.T) {
	started := t.TempDir()
	t.Chdir(started)
	for _, made := range []string{"work", "kept"} {
		if err := os.Mkdir(made, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	workspace, state, nothing := "work", "kept", ""
	plugins := []string{"kept/a.wasm", "/opt/b.wasm"}
	if err := stood(&workspace, plugins, &state, &nothing); err != nil {
		t.Fatal(err)
	}
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	real := func(path string) string {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	if real(here) != real(filepath.Join(started, "work")) || real(workspace) != real(here) {
		t.Errorf("the daemon stands in %s and its workspace is %s, want both the workspace's directory", here, workspace)
	}
	if !filepath.IsAbs(state) || real(state) != real(filepath.Join(started, "kept")) {
		t.Errorf("its state is kept in %s, want the directory beside where it was started", state)
	}
	if !filepath.IsAbs(plugins[0]) || plugins[1] != "/opt/b.wasm" || nothing != "" {
		t.Errorf("what it was told by a path is now %q and %v, and what it was told nothing of %q", plugins[0], plugins[1], nothing)
	}
	missing := "no-such-directory"
	if err := stood(&missing, nil); err == nil {
		t.Error("a workspace that is no directory was stood in")
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
	got, err := withDirectory([]string{"/opt/one.wasm"}, dir)
	if want := []string{"/opt/one.wasm", filepath.Join(dir, "buf.wasm"), filepath.Join(dir, "go.wasm")}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("plugins = %v, %v; want %v", got, err, want)
	}
	if got, err := withDirectory([]string{"/opt/one.wasm"}, ""); err != nil || !reflect.DeepEqual(got, []string{"/opt/one.wasm"}) {
		t.Errorf("with no directory: %v, %v; want the one plugin given", got, err)
	}
	empty := t.TempDir()
	if _, err := withDirectory(nil, empty); err == nil || !strings.Contains(err.Error(), "holds no <name>.wasm") {
		t.Errorf("a directory of no plugins: %v, want it refused by name", err)
	}
}

// A sandbox's daemon told nothing of its workers' platform is refused
// by name; the control is the same daemon told it by its flags.
func TestASandboxDaemonToldNoPlatformIsRefused(t *testing.T) {
	if err := toldPlatform(nil); err == nil || !strings.Contains(err.Error(), "--platform") {
		t.Errorf("told nothing: %v, want it refused naming --platform", err)
	}
	if err := toldPlatform([]string{"OSFamily=linux"}); err != nil {
		t.Errorf("told by its flags: %v, want it allowed", err)
	}
}

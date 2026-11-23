package testmodule

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
)

// Where no directory of prepared modules is named, or the one named is
// not there, a module is not found and is not compiled: the refusal
// says how they are prepared. The control is the test below, which
// reads one that was.
func TestAModuleIsReadOnlyWhereItWasPrepared(t *testing.T) {
	for name, directory := range map[string]string{"no directory named": "", "a directory that is not there": filepath.Join(t.TempDir(), "absent")} {
		t.Run(name, func(t *testing.T) {
			// Where no repository holds them either: a temporary directory
			// may be beneath one, in a test action.
			t.Chdir("/")
			toldOf(t, "", errors.New("told no repository"))
			t.Setenv(Artifacts, directory)
			for _, read := range []func(string) ([]byte, error){Plugin, SDKExample} {
				if content, err := read("go"); err == nil || !strings.Contains(err.Error(), "prepares them") {
					t.Fatalf("a module that was not prepared was given: %d bytes, %v", len(content), err)
				}
			}
		})
	}
}

// Where no directory is named, the repository is the layout, as a test
// action is given the modules its crates' wasm actions left, found where
// the test is told its repository is; the control is the test above,
// where it is told of none.
func TestARepositoryThatHoldsItsModulesIsTheirLayout(t *testing.T) {
	root := t.TempDir()
	testfiles.Tree(t, root, map[string]string{"plugins/go/" + Released + "/plugin.wasm": "module"})
	t.Chdir(filepath.Join(root, "plugins"))
	toldOf(t, root, nil)
	t.Setenv(Artifacts, "")
	if content, err := Plugin("go"); err != nil || string(content) != "module" {
		t.Fatalf("the repository's module was not read: %q, %v", content, err)
	}
}

// kind is a kind of module: one of it by name, where it is prepared and
// how it is read.
type kind struct {
	name, file string
	read       func(string) ([]byte, error)
}

// kinds is a module of each kind.
var kinds = []kind{
	{"go", "plugins/go/target/wasm32-wasip1/release/frontseat_plugin_go.wasm", Plugin},
	{"cohabiting", "sdk/plugin/target/wasm32-wasip1/release/examples/cohabiting.wasm", SDKExample},
}

// eachKind runs check, as a test of its own, on a module of each kind:
// where it is prepared, among the prepared modules in a directory of the
// test's, with no checkout to hand.
func eachKind(t *testing.T, check func(t *testing.T, fixture kind, artifacts, file string)) {
	t.Helper()
	artifacts := t.TempDir()
	t.Setenv(Artifacts, artifacts)
	t.Chdir(t.TempDir())
	for _, fixture := range kinds {
		t.Run(fixture.name, func(t *testing.T) {
			check(t, fixture, artifacts, filepath.Join(artifacts, fixture.file))
		})
	}
}

// A module is read where it was prepared, from a directory that holds no
// checkout and has no compiler to hand; one that is not there is not
// found.
func TestAPreparedModuleIsReadWithNoCheckoutOrCompiler(t *testing.T) {
	eachKind(t, func(t *testing.T, fixture kind, artifacts, file string) {
		content := "declared output of " + fixture.name
		testfiles.Write(t, file, content)
		got, err := fixture.read(fixture.name)
		if err != nil || string(got) != content {
			t.Fatalf("prebuilt fixture was not read: %q, %v", got, err)
		}
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.read(fixture.name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a module that was not prepared was given: %v", err)
		}
	})
}

// A link among the prepared modules is followed inside them and no further.
func TestAPreparedModulesLinksStayAmongThePreparedModules(t *testing.T) {
	eachKind(t, func(t *testing.T, fixture kind, artifacts, file string) {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		// Kept elsewhere in the tree: a plugin's crate leaves one module.
		inside := filepath.Join(artifacts, "kept", fixture.name+".wasm")
		testfiles.Write(t, inside, "inside")
		link, err := filepath.Rel(filepath.Dir(file), inside)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(link, file); err != nil {
			t.Fatal(err)
		}
		if got, err := fixture.read(fixture.name); err != nil || string(got) != "inside" {
			t.Fatalf("a link inside the artifact tree must work: %q, %v", got, err)
		}
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "outside.wasm")
		if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, file); err != nil {
			t.Fatal(err)
		}
		if got, err := fixture.read(fixture.name); err == nil {
			t.Fatalf("a link outside the artifact tree was read: %q", got)
		}
	})
}

func TestModuleNamesCannotBePathsOrOptions(t *testing.T) {
	t.Setenv(Artifacts, t.TempDir())
	for _, name := range []string{"", ".", "..", "../outside", "dir/name", `dir\name`, "/absolute", "nul\x00name", "--help"} {
		for _, read := range []func(string) ([]byte, error){Plugin, SDKExample} {
			if _, err := read(name); err == nil || os.IsNotExist(err) {
				t.Fatalf("invalid name %q was used as a file name: %v", name, err)
			}
		}
	}
}

// A plugin's module is the one its crate left, whatever the crate calls
// it, and the plugins are the ones that were prepared: a list of them is
// kept nowhere. A plugin that left two modules, or none, is refused.
func TestAPluginsModuleIsTheOneItsCrateLeft(t *testing.T) {
	artifacts := t.TempDir()
	t.Setenv(Artifacts, artifacts)
	write := func(at, content string) {
		t.Helper()
		testfiles.Write(t, filepath.Join(artifacts, filepath.FromSlash(at)), content)
	}
	write("plugins/go/"+Released+"/frontseat_plugin_go.wasm", "go")
	write("plugins/odd/"+Released+"/another_name.wasm", "odd")
	write("plugins/odd/"+Released+"/another_name.d", "no module")
	plugins, err := Plugins()
	if err != nil || len(plugins) != 2 || string(plugins["go"]) != "go" || string(plugins["odd"]) != "odd" {
		t.Fatalf("the plugins prepared are %v, %v", plugins, err)
	}
	if content, err := Plugin("odd"); err != nil || string(content) != "odd" {
		t.Fatalf("a module its crate names otherwise: %q, %v", content, err)
	}
	write("plugins/two/"+Released+"/one.wasm", "one")
	write("plugins/two/"+Released+"/other.wasm", "other")
	if _, err := Plugin("two"); err == nil || !strings.Contains(err.Error(), "more than one module") {
		t.Fatalf("a plugin that left two modules was given one: %v", err)
	}
	write("plugins/none/"+Released+"/notes.txt", "no module")
	if _, err := Plugin("none"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a plugin that left no module was given one: %v", err)
	}
	if _, err := Plugins(); err == nil {
		t.Fatal("the plugins were listed though two of them have no one module")
	}
}

// toldOf has the test support take the repository to be where a test is
// told, for one test, as if it had been told it when it started.
func toldOf(t *testing.T, root string, err error) {
	t.Helper()
	was, wasErr := told, toldErr
	told, toldErr = root, err
	t.Cleanup(func() { told, toldErr = was, wasErr })
}

// The repository a test was told of is where it was when it started,
// whatever it moves to after: Frontseat tells it as a path from there.
// The child run below is told "..", from a directory beneath the one it
// must find, and moves to the system's root before it asks; asked at
// once, the path would lead elsewhere.
func TestTheRepositoryATestIsToldOfIsWhereItStarted(t *testing.T) {
	if os.Getenv("TESTMODULE_RUN_AS_TOLD") == "1" {
		t.Chdir("/")
		root, err := Root()
		fmt.Printf("ROOT=%s ERR=%v\n", root, err)
		return
	}
	repository, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	beneath := filepath.Join(repository, "package")
	if err := os.Mkdir(beneath, 0o755); err != nil {
		t.Fatal(err)
	}
	// By its whole path: it is started elsewhere than this test was, and
	// a test program is run by a path from where its action runs.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(self, "-test.run=^TestTheRepositoryATestIsToldOfIsWhereItStarted$", "-test.count=1")
	child.Dir = beneath
	for _, each := range os.Environ() {
		if !strings.HasPrefix(each, SourceVariable+"=") {
			child.Env = append(child.Env, each)
		}
	}
	child.Env = append(child.Env, "TESTMODULE_RUN_AS_TOLD=1", SourceVariable+"=..")
	out, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if want := "ROOT=" + repository + " ERR=<nil>"; !strings.Contains(string(out), want) {
		t.Errorf("told .. from %s and moved to /, the test support said:\n%s\nwant %s", beneath, out, want)
	}
}

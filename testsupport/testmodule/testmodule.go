// Package testmodule gives a test a plugin as the wasm module the host
// loads. A test reads what was prepared for it and compiles nothing:
// the modules are laid out, each where the crate that makes it leaves
// it, in the directory FRONTSEAT_TEST_ARTIFACTS names. A task that runs
// tests prepares them from this checkout first (prepare) and is told
// the directory they were laid out in, which is never changed after.
package testmodule

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// Artifacts names the directory the prepared modules are laid out in.
const Artifacts = "FRONTSEAT_TEST_ARTIFACTS"

// Released is where a crate leaves what it builds for wasm, beneath its
// own directory: where its module sits in the layout.
const Released = "target/wasm32-wasip1/release"

// Plugin returns the module of the plugin named, e.g. "go": the one
// module its crate left. What the crate calls it is the crate's to say.
func Plugin(name string) ([]byte, error) {
	layout, err := preparedFor(name)
	if err != nil {
		return nil, err
	}
	defer layout.Close()
	return plugin(layout, name)
}

// PluginOrExit is Plugin for a TestMain, which has no test to fail: where
// the module cannot be read, it says why and ends the test binary.
func PluginOrExit(name string) []byte {
	content, err := Plugin(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return content
}

// Plugins returns the module of every plugin that was prepared, by the
// name of its directory.
func Plugins() (map[string][]byte, error) {
	layout, err := prepared()
	if err != nil {
		return nil, err
	}
	defer layout.Close()
	return plugins(layout)
}

// PluginsIn is Plugins of the modules laid out in a directory, for what
// lays them out to read them as a test will.
func PluginsIn(dir string) (map[string][]byte, error) {
	layout, err := os.OpenRoot(dir)
	if err != nil {
		return nil, unprepared(dir, err)
	}
	defer layout.Close()
	return plugins(layout)
}

func plugins(layout *os.Root) (map[string][]byte, error) {
	entries, err := fs.ReadDir(layout.FS(), "plugins")
	if err != nil {
		return nil, unprepared("plugins", err)
	}
	out := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if out[entry.Name()], err = plugin(layout, entry.Name()); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SDKExample returns a shared SDK contract fixture.
func SDKExample(name string) ([]byte, error) {
	layout, err := preparedFor(name)
	if err != nil {
		return nil, err
	}
	defer layout.Close()
	at := path.Join("sdk/plugin", Released, "examples", name+".wasm")
	content, err := layout.ReadFile(at)
	if err != nil {
		return nil, unprepared(at, err)
	}
	return content, nil
}

func checkName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") || strings.HasPrefix(name, "-") {
		return fmt.Errorf("module name must be one nonempty file name, not an option")
	}
	return nil
}

// preparedFor opens the layout to read the module of one name.
func preparedFor(name string) (*os.Root, error) {
	if err := checkName(name); err != nil {
		return nil, err
	}
	return prepared()
}

// prepared opens the layout. OpenRoot keeps what is read, and its links,
// inside it.
func prepared() (*os.Root, error) {
	dir := os.Getenv(Artifacts)
	if dir == "" {
		// In a test action, the repository is the layout: each plugin's
		// module is where its crate's wasm action left it, which its
		// Component's catalog file gives its tests.
		if root, err := Root(); err == nil && laidOut(root) {
			dir = root
		}
	}
	if dir == "" {
		return nil, fmt.Errorf("%s names no directory of prepared modules: a test that loads one is run by a mise task (`mise run go:test -- ...`), which prepares them, or by frontseat test", Artifacts)
	}
	layout, err := os.OpenRoot(dir)
	if err != nil {
		return nil, unprepared(dir, err)
	}
	return layout, nil
}

// plugin reads the one module a plugin's crate left.
func plugin(layout *os.Root, name string) ([]byte, error) {
	dir := path.Join("plugins", name, Released)
	entries, err := fs.ReadDir(layout.FS(), dir)
	if err != nil {
		return nil, unprepared(dir, err)
	}
	var module string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".wasm") {
			continue
		}
		if module != "" {
			return nil, fmt.Errorf("the plugin %s left more than one module: %s and %s", name, module, entry.Name())
		}
		module = entry.Name()
	}
	if module == "" {
		return nil, unprepared(dir, fmt.Errorf("the plugin %s left no module there: %w", name, fs.ErrNotExist))
	}
	at := path.Join(dir, module)
	content, err := layout.ReadFile(at)
	if err != nil {
		return nil, unprepared(at, err)
	}
	return content, nil
}

// unprepared is a module that is not there: an error that says how the
// modules are prepared, never a reason to compile where the test runs.
func unprepared(what string, err error) error {
	return fmt.Errorf("the prepared modules, %s: %w; a mise task that runs tests prepares them (`mise run go:test -- ...`)", what, err)
}

// SourceVariable is what a test is told its repository is at, by what
// runs it: Frontseat (stage.SourceVariable, held equal to it), as a path
// from the directory the test runs in, or the gate, whole.
const SourceVariable = "FRONTSEAT_TEST_SRCDIR"

// Root returns the root of the repository the test runs in, as it was
// told it: a test never looks up from where it runs for it.
func Root() (string, error) { return told, toldErr }

// told is the root as the test was told it, read once, before any test
// moves from where it was started: Frontseat tells it as a path from
// there.
var told, toldErr = func() (string, error) {
	said := os.Getenv(SourceVariable)
	if said == "" {
		return "", fmt.Errorf("%s is not set: a test is told where its repository is by what runs it (frontseat test, or `mise run go:test -- ...`)", SourceVariable)
	}
	return filepath.Abs(said)
}()

// Workspace returns the directories of the modules the go.work at root
// uses, as go reads them (`go work edit -json`), each relative to root
// with slashes and without the "./" before it.
func Workspace(root string) ([]string, error) {
	work := filepath.Join(root, "go.work")
	if _, err := os.Stat(work); err != nil {
		return nil, err
	}
	read := exec.Command("go", "work", "edit", "-json", work)
	read.Dir = root
	read.Env = append(os.Environ(), "GOWORK="+work)
	printed, err := read.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("go work edit -json %s: %w: %s", work, err, exit.Stderr)
		}
		return nil, err
	}
	var said struct{ Use []struct{ DiskPath string } }
	if err := json.Unmarshal(printed, &said); err != nil {
		return nil, fmt.Errorf("go work edit -json %s: %w", work, err)
	}
	var dirs []string
	for _, use := range said.Use {
		dirs = append(dirs, filepath.ToSlash(filepath.Clean(use.DiskPath)))
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("the go.work at %s uses no module", root)
	}
	return dirs, nil
}

// laidOut reports whether a directory holds a plugin's module where its
// crate leaves one: the repository, in a test action given them.
func laidOut(dir string) bool {
	found, err := filepath.Glob(filepath.Join(dir, "plugins", "*", filepath.FromSlash(Released), "*.wasm"))
	return err == nil && len(found) > 0
}

package pluginhost_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"github.com/frontseat-dev/frontseat/testsupport/testmodule"
	"github.com/tetratelabs/wazero"
)

// The variables a test that keeps compiled plugins on disk tells the
// process it starts: Compiled changes what every host of a process
// shares, so it is told only in a process of its own.
const (
	compiledDirectory = "FRONTSEAT_TEST_COMPILED_DIRECTORY"
	compiledRole      = "FRONTSEAT_TEST_COMPILED_ROLE"
)

// inAProcessOfItsOwn runs the test that calls it again in a process of
// its own, told the directory and the role, and returns what that
// process printed; it fails the test where that process failed.
func inAProcessOfItsOwn(t *testing.T, directory, role string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v")
	command.Env = append(os.Environ(), compiledDirectory+"="+directory, compiledRole+"="+role)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", role, err, out)
	}
	return string(out)
}

// keptModule is a module of a digest of its own: one of the real plugins
// with a custom section, which wasm ignores.
func keptModule(t *testing.T) []byte {
	t.Helper()
	base, err := testmodule.Plugin("npm")
	if err != nil {
		t.Fatal(err)
	}
	return append(append([]byte{}, base...), 0, 6, 4, 'k', 'e', 'p', 't', 1)
}

// child is what a process of a test's own does: keep compiled plugins in
// the directory it is told, and load the module.
func child(t *testing.T) {
	t.Helper()
	if err := pluginhost.Compiled(os.Getenv(compiledDirectory)); err != nil {
		t.Fatal(err)
	}
	loadAllocations(t, context.Background(), keptModule(t))
}

// What one process compiled and kept on disk another loads from there,
// compiling nothing: a process that compiles writes what it compiled
// anew, to a file of its own moved into place, and one that loads from
// disk leaves the file as it was. The control: the first process kept
// the module.
func TestAPluginKeptCompiledOnDiskIsLoadedByAnotherProcessWithoutCompiling(t *testing.T) {
	if os.Getenv(compiledRole) != "" {
		child(t)
		return
	}
	directory := filepath.Join(t.TempDir(), "compiled")
	inAProcessOfItsOwn(t, directory, "first")
	before := keptFiles(t, directory)
	if len(before) == 0 {
		t.Fatal("the first process kept nothing on disk")
	}
	inAProcessOfItsOwn(t, directory, "again")
	after := keptFiles(t, directory)
	for name, was := range before {
		if now, there := after[name]; !there || !os.SameFile(was, now) {
			t.Errorf("%s was written again: the next process compiled the module again", name)
		}
	}
}

// keptFiles is every file compiled code is kept in beneath the directory,
// by its path.
func keptFiles(t *testing.T, directory string) map[string]os.FileInfo {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(directory, "wazero-*", "*"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]os.FileInfo{}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		files[path] = info
	}
	return files
}

// Compiled code on disk that is not whole costs time and nothing else:
// the plugin loads, compiled in memory, and what could not be used is
// removed. The control: wazero
// alone, told the same directory, fails to compile the module.
func TestCompiledCodeOnDiskThatCannotBeUsedFailsNoLoad(t *testing.T) {
	if os.Getenv(compiledRole) != "" {
		child(t)
		return
	}
	directory := filepath.Join(t.TempDir(), "compiled")
	inAProcessOfItsOwn(t, directory, "keeps")
	kept, err := filepath.Glob(filepath.Join(directory, "wazero-*", "*"))
	if err != nil || len(kept) == 0 {
		t.Fatalf("nothing was kept on disk (%v)", err)
	}
	for _, file := range kept {
		if err := os.WriteFile(file, []byte("not whole"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	cache, err := wazero.NewCompilationCacheWithDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	// Configured as a host's runtime is, which wazero keys what it
	// compiled by.
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCloseOnContextDone(true).WithCompilationCache(cache))
	if _, err := rt.CompileModule(ctx, keptModule(t)); err == nil {
		t.Fatal("wazero compiled the module beside code on disk that is not whole: the control shows nothing")
	}
	_ = rt.Close(ctx)

	inAProcessOfItsOwn(t, directory, "loads beside what is not whole")
	for _, file := range kept {
		if held, err := os.ReadFile(file); err == nil && string(held) == "not whole" {
			t.Errorf("%s, which could not be used, was left on disk", filepath.Base(file))
		}
	}
	if info, err := os.Lstat(filepath.Dir(kept[0])); err != nil || !info.IsDir() {
		t.Errorf("the directory the daemons beside keep in was removed (%v)", err)
	}
}

// A directory others may write is not kept in, being machine code a
// daemon runs. The control: the same directory, its user's alone, is.
func TestCompiledPluginsAreKeptOnlyInADirectoryOfTheirUsersAlone(t *testing.T) {
	if os.Getenv(compiledRole) != "" {
		if err := pluginhost.Compiled(os.Getenv(compiledDirectory)); err != nil {
			t.Fatal(err)
		}
		return
	}
	directory := filepath.Join(t.TempDir(), "compiled")
	if err := os.Mkdir(directory, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o777); err != nil {
		t.Fatal(err)
	}
	// Refused before anything changes, so in this process.
	if err := pluginhost.Compiled(directory); err == nil || !strings.Contains(err.Error(), "others may write it") {
		t.Fatalf("a directory others may write was not refused: %v", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	inAProcessOfItsOwn(t, directory, "its user's alone")
}

// What another version of wazero kept for this architecture and system
// is removed; what one kept for another architecture, and what is no
// version's, is left. The control: this version's own is kept.
func TestWhatAnotherVersionCompiledForThisMachineIsRemoved(t *testing.T) {
	if os.Getenv(compiledRole) != "" {
		child(t)
		return
	}
	directory := filepath.Join(t.TempDir(), "compiled")
	inAProcessOfItsOwn(t, directory, "keeps")
	ours, err := filepath.Glob(filepath.Join(directory, "wazero-*-"+runtime.GOARCH+"-"+runtime.GOOS))
	if err != nil || len(ours) != 1 {
		t.Fatalf("this version keeps %v (%v)", ours, err)
	}
	older := filepath.Join(directory, "wazero-v0.0.1-"+runtime.GOARCH+"-"+runtime.GOOS)
	elsewhere := filepath.Join(directory, "wazero-v0.0.1-otherarch-"+runtime.GOOS)
	notes := filepath.Join(directory, "notes")
	for _, made := range []string{older, elsewhere, notes} {
		if err := os.Mkdir(made, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	inAProcessOfItsOwn(t, directory, "loads again")
	for path, there := range map[string]bool{older: false, elsewhere: true, notes: true, ours[0]: true} {
		if _, err := os.Stat(path); (err == nil) != there {
			t.Errorf("%s: there %v, and was to be %v", filepath.Base(path), err == nil, there)
		}
	}
}

// What this version keeps beneath the directory that is not as it would
// have written it (a file others may read or write, a file it did not
// write, a link where its directory is) is removed before any of it is
// read, and what a link led to is left as it was. The control: what it
// wrote itself is kept, and read.
func TestCompiledCodeThisUserDidNotKeepIsRemovedBeforeItIsRead(t *testing.T) {
	if os.Getenv(compiledRole) != "" {
		child(t)
		return
	}
	directory := filepath.Join(t.TempDir(), "compiled")
	inAProcessOfItsOwn(t, directory, "keeps")
	before := keptFiles(t, directory)
	inAProcessOfItsOwn(t, directory, "reads what it kept")
	after := keptFiles(t, directory)
	for name, was := range before {
		if now, there := after[name]; !there || !os.SameFile(was, now) {
			t.Fatalf("the control: %s, which it kept itself, was not kept", name)
		}
	}
	version := filepath.Dir(func() string {
		for name := range before {
			return name
		}
		return ""
	}())

	planted := filepath.Join(version, "planted")
	if err := os.WriteFile(planted, []byte("machine code"), 0o644); err != nil {
		t.Fatal(err)
	}
	inAProcessOfItsOwn(t, directory, "beside a file others may read")
	if _, err := os.Stat(planted); err == nil {
		t.Error("a file others may read was left where compiled code is read")
	}
	for name := range before {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("%s, which it kept itself, was removed beside a file others may read: %v", filepath.Base(name), err)
		}
	}

	aside := t.TempDir()
	led := filepath.Join(aside, "led")
	if err := os.WriteFile(led, []byte("machine code"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(version, "link")
	if err := os.Symlink(led, link); err != nil {
		t.Fatal(err)
	}
	inAProcessOfItsOwn(t, directory, "beside a link")
	if _, err := os.Lstat(link); err == nil {
		t.Error("a link was left where compiled code is read")
	}
	if _, err := os.Stat(led); err != nil {
		t.Errorf("what a link led to was touched: %v", err)
	}

	elsewhere := t.TempDir()
	theirs := filepath.Join(elsewhere, "theirs")
	if err := os.WriteFile(theirs, []byte("machine code"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(version); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, version); err != nil {
		t.Fatal(err)
	}
	inAProcessOfItsOwn(t, directory, "where its directory is a link")
	if info, err := os.Lstat(version); err != nil || !info.IsDir() {
		t.Errorf("the link was not replaced by a directory of its own (%v)", err)
	}
	if _, err := os.Stat(theirs); err != nil {
		t.Errorf("what the link led to was touched: %v", err)
	}
}

// A directory that cannot be kept in refuses it, and a host made after
// loads its plugins all the same, compiling them in memory. The control:
// the same process loads the module.
func TestADirectoryThatCannotBeKeptInIsRefusedAndPluginsLoadAllTheSame(t *testing.T) {
	if os.Getenv(compiledRole) != "" {
		if err := pluginhost.Compiled(os.Getenv(compiledDirectory)); err == nil {
			t.Fatal("a directory that cannot be kept in was taken")
		}
		loadAllocations(t, context.Background(), keptModule(t))
		return
	}
	file := filepath.Join(t.TempDir(), "compiled")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	inAProcessOfItsOwn(t, file, "a file where the directory is")
	inAProcessOfItsOwn(t, filepath.Join(t.TempDir(), "absent", "compiled"), "beneath a directory that is not there")
}

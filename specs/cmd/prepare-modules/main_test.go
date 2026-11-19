package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/frontseat-dev/frontseat/server/testsupport/testmodule"
)

const metadata = `{"workspace_root":"/repository","target_directory":"/elsewhere/target","packages":[
 {"name":"frontseat-plugin-go","manifest_path":"/repository/plugins/go/Cargo.toml","targets":[{"name":"frontseat_plugin_go","kind":["cdylib"],"crate_types":["cdylib"]}]},
 {"name":"odd","manifest_path":"/repository/plugins/odd/Cargo.toml","targets":[{"name":"other-name","kind":["cdylib","rlib"],"crate_types":["cdylib","rlib"]}]},
 {"name":"conventional-commits","manifest_path":"/repository/plugins/release/conventional-commits/Cargo.toml","targets":[{"name":"conventional_commits","kind":["lib"],"crate_types":["lib"]}]},
 {"name":"nested-module","manifest_path":"/repository/plugins/release/nested/Cargo.toml","targets":[{"name":"nested","kind":["cdylib"],"crate_types":["cdylib"]}]},
 {"name":"elsewhere","manifest_path":"/repository/api/Cargo.toml","targets":[{"name":"elsewhere","kind":["cdylib"],"crate_types":["cdylib"]}]},
 {"name":"frontseat-plugin","manifest_path":"/repository/sdk/plugin/Cargo.toml","targets":[
   {"name":"frontseat_plugin","kind":["lib"],"crate_types":["lib"]},
   {"name":"options","kind":["example"],"crate_types":["cdylib"]},
   {"name":"runtime_inputs","kind":["example"],"crate_types":["cdylib"]},
   {"name":"printed","kind":["example"],"crate_types":["bin"]}]}]}`

// Which crates are modules is what cargo says: the library of a crate in
// a directory of plugins/ that is built as a module, and each such
// example of the crate in sdk/plugin/. A crate nested deeper, one elsewhere, a
// library that is no module and an example that is none are not built.
func TestTheModulesBuiltAreTheOnesCargoNames(t *testing.T) {
	chosen, kept, err := selected([]byte(metadata))
	if err != nil {
		t.Fatal(err)
	}
	// Kept where cargo keeps what it builds, wherever that is.
	if kept != filepath.FromSlash("/elsewhere/target/test-modules") {
		t.Fatalf("the sets are kept in %q", kept)
	}
	if _, _, err := selected([]byte(strings.Replace(metadata, `"/elsewhere/target"`, `"target"`, 1))); err == nil {
		t.Fatal("a directory of what is built that is no full path was taken")
	}
	var names []string
	for _, each := range chosen {
		names = append(names, each.directory+" "+each.crate+" "+each.example)
	}
	if want := []string{"plugins/go frontseat-plugin-go ", "plugins/odd odd ", "sdk/plugin frontseat-plugin options", "sdk/plugin frontseat-plugin runtime_inputs"}; !slices.Equal(names, want) {
		t.Fatalf("chose %q, want %q", names, want)
	}
	if got := builds(chosen); len(got) != 2 || strings.Join(got[0], " ") != "-p frontseat-plugin-go -p odd" || strings.Join(got[1], " ") != "-p frontseat-plugin --example options --example runtime_inputs" {
		t.Fatalf("cargo is told %v", got)
	}
}

// The file a module is, and where cargo built it, are what cargo build
// says it made, whatever the crate calls it and wherever cargo builds:
// each is laid where its crate leaves it, under that name. A module
// cargo made no file for, or two, is a mistake that names it.
func TestAModuleIsTheFileCargoSaysItMade(t *testing.T) {
	chosen, _, err := selected([]byte(metadata))
	if err != nil {
		t.Fatal(err)
	}
	artifact := func(manifest, name, kind string, files ...string) string {
		return `{"reason":"compiler-artifact","manifest_path":"` + manifest + `","target":{"name":"` + name + `","kind":["` + kind + `"],"crate_types":["cdylib"]},"filenames":["` + strings.Join(files, `","`) + `"]}`
	}
	all := []string{
		`{"reason":"build-script-executed","package_id":"x"}`,
		artifact("/repository/plugins/go/Cargo.toml", "frontseat_plugin_go", "cdylib", "/elsewhere/target/wasm32-wasip1/release/frontseat_plugin_go.wasm"),
		artifact("/repository/plugins/odd/Cargo.toml", "other-name", "cdylib", "/elsewhere/target/wasm32-wasip1/release/libother.rlib", "/elsewhere/target/wasm32-wasip1/release/as_cargo_says.wasm"),
		artifact("/repository/sdk/plugin/Cargo.toml", "options", "example", "/elsewhere/target/wasm32-wasip1/release/examples/options.wasm"),
		artifact("/repository/sdk/plugin/Cargo.toml", "runtime_inputs", "example", "/elsewhere/target/wasm32-wasip1/release/examples/runtime_inputs.wasm"),
		`{"reason":"build-finished","success":true}`,
	}
	found, err := built(chosen, []byte(strings.Join(all, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	var laid []string
	for _, each := range found {
		laid = append(laid, each.built+" -> "+filepath.ToSlash(each.laid))
	}
	want := []string{
		"/elsewhere/target/wasm32-wasip1/release/frontseat_plugin_go.wasm -> plugins/go/target/wasm32-wasip1/release/frontseat_plugin_go.wasm",
		"/elsewhere/target/wasm32-wasip1/release/as_cargo_says.wasm -> plugins/odd/target/wasm32-wasip1/release/as_cargo_says.wasm",
		"/elsewhere/target/wasm32-wasip1/release/examples/options.wasm -> sdk/plugin/target/wasm32-wasip1/release/examples/options.wasm",
		"/elsewhere/target/wasm32-wasip1/release/examples/runtime_inputs.wasm -> sdk/plugin/target/wasm32-wasip1/release/examples/runtime_inputs.wasm",
	}
	if !slices.Equal(laid, want) {
		t.Fatalf("laid out\n%s\nwant\n%s", strings.Join(laid, "\n"), strings.Join(want, "\n"))
	}
	if _, err := built(chosen, []byte(strings.Join(all[:3], "\n"))); err == nil || !strings.Contains(err.Error(), "example options") {
		t.Fatalf("a module cargo made no file for was laid out: %v", err)
	}
	two := append(slices.Clone(all), artifact("/repository/plugins/go/Cargo.toml", "frontseat_plugin_go", "cdylib", "/elsewhere/target/wasm32-wasip1/release/second.wasm"))
	if _, err := built(chosen, []byte(strings.Join(two, "\n"))); err == nil || !strings.Contains(err.Error(), "frontseat-plugin-go") {
		t.Fatalf("a module cargo made two files for was laid out: %v", err)
	}
	if _, err := built(chosen, []byte("not a message")); err == nil {
		t.Fatal("what is no message of cargo's was read as one")
	}
}

// The modules are laid out as one whole set, named by what it holds:
// the same modules are the same set, made once and left as it is; other
// modules are another set, and the one before stays as it was for
// whatever still reads it. Nothing that was there is changed or removed,
// and a set that could not be made leaves nothing behind.
func TestModulesAreLaidOutAsASetNamedByWhatItHolds(t *testing.T) {
	built := t.TempDir()
	write := func(at, content string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(at, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return at
	}
	kept := filepath.Join(t.TempDir(), "target", "test-modules")
	first := []module{{built: write(filepath.Join(built, "a.wasm"), "first"), laid: filepath.Join("plugins", "a", "a.wasm")}}
	one, err := lay(kept, first)
	if err != nil || filepath.Dir(one) != kept {
		t.Fatalf("laid out in %q, %v", one, err)
	}
	if content, err := os.ReadFile(filepath.Join(one, "plugins", "a", "a.wasm")); err != nil || string(content) != "first" {
		t.Fatalf("laid out %q, %v", content, err)
	}
	if info, err := os.Stat(one); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("the set is %v, %v, and others read it", info.Mode(), err)
	}
	// The same modules: the same set, the one that was made.
	made, _ := os.Stat(one)
	if again, err := lay(kept, first); err != nil || again != one {
		t.Fatalf("the same modules were laid out as %q, %v", again, err)
	}
	if now, err := os.Stat(one); err != nil || !os.SameFile(made, now) {
		t.Fatalf("a set that was there was made again: %v", err)
	}
	// Other modules: another set, in whatever order they are given, and the first as it was.
	second := []module{
		{built: write(filepath.Join(built, "b.wasm"), "second"), laid: filepath.Join("plugins", "b", "b.wasm")},
		{built: filepath.Join(built, "a.wasm"), laid: filepath.Join("plugins", "a", "a.wasm")},
	}
	two, err := lay(kept, second)
	if err != nil || two == one {
		t.Fatalf("other modules were laid out as %q, %v", two, err)
	}
	slices.Reverse(second)
	if again, err := lay(kept, second); err != nil || again != two {
		t.Fatalf("the same modules in another order were laid out as %q, %v", again, err)
	}
	if _, err := os.Stat(filepath.Join(one, "plugins", "b")); !os.IsNotExist(err) {
		t.Fatalf("the set before now holds a module of the set after: %v", err)
	}
	// A module built anew with other content is another set; built as it was, the first again.
	write(filepath.Join(built, "a.wasm"), "first, rebuilt")
	if three, err := lay(kept, first); err != nil || three == one {
		t.Fatalf("a module built anew was laid out as the set before: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(one, "plugins", "a", "a.wasm")); err != nil || string(content) != "first" {
		t.Fatalf("the set before now reads %q, %v", content, err)
	}
	write(filepath.Join(built, "a.wasm"), "first")
	if again, err := lay(kept, first); err != nil || again != one {
		t.Fatalf("the modules as they were are not the set they were: %q, %v", again, err)
	}
	// Not to be written again, and held to its name where it is found: a
	// set something changed or emptied is refused, and left for whoever
	// removes it, beside the untouched one, which is given.
	laid := filepath.Join(two, "plugins", "b", "b.wasm")
	if info, err := os.Stat(laid); err != nil || info.Mode().Perm() != 0o444 {
		t.Fatalf("a module of a set is %v, %v, and can be written again", info.Mode(), err)
	}
	if err := os.Chmod(laid, 0o644); err != nil {
		t.Fatal(err)
	}
	write(laid, "changed after")
	if set, err := lay(kept, second); err == nil || !strings.Contains(err.Error(), "does not hold the modules it is named by") {
		t.Fatalf("a set that was changed was given as %q: %v", set, err)
	}
	if err := os.Remove(laid); err != nil {
		t.Fatal(err)
	}
	if set, err := lay(kept, second); err == nil {
		t.Fatalf("a set that lacks a module was given as %q", set)
	}
	if content, err := os.ReadFile(laid); !os.IsNotExist(err) {
		t.Fatalf("a set that was changed was mended: %q, %v", content, err)
	}
	if again, err := lay(kept, first); err != nil || again != one {
		t.Fatalf("the set that nothing changed is no longer given: %q, %v", again, err)
	}
	// A module that was not built: no set, and nothing left in the making.
	if set, err := lay(kept, []module{{built: filepath.Join(built, "absent.wasm"), laid: "c.wasm"}}); err == nil {
		t.Fatalf("a module that was not built was laid out in %q", set)
	}
	entries, err := os.ReadDir(kept)
	if err != nil || len(entries) != 3 {
		t.Fatalf("the sets kept are %v, %v, want the three that were laid out and nothing in the making", entries, err)
	}
}

// Many lay modules out at once, the same and others, while one reads:
// every one succeeds, and whoever reads a set reads it whole.
func TestLayingOutAtOnceLeavesEverySetWhole(t *testing.T) {
	built := t.TempDir()
	kept := filepath.Join(t.TempDir(), "test-modules")
	sets := make([][]module, 4)
	for i := range sets {
		for _, name := range []string{"a", "b", "c"} {
			file := filepath.Join(built, fmt.Sprint(i), name+".wasm")
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(fmt.Sprint("set ", i)), 0o644); err != nil {
				t.Fatal(err)
			}
			sets[i] = append(sets[i], module{built: file, laid: filepath.Join("plugins", name, filepath.FromSlash(testmodule.Released), name+".wasm")})
		}
	}
	var laying sync.WaitGroup
	failed := make(chan error, 8*50)
	for worker := range 8 {
		laying.Add(1)
		go func() {
			defer laying.Done()
			for round := range 50 {
				which := (worker + round) % len(sets)
				set, err := lay(kept, slices.Clone(sets[which]))
				if err != nil {
					failed <- err
					continue
				}
				// As a test reads them: through the loader, told the set.
				plugins, err := testmodule.PluginsIn(set)
				if err != nil || len(plugins) != 3 {
					failed <- fmt.Errorf("a set that was laid out holds %d plugins: %v", len(plugins), err)
					continue
				}
				for name, content := range plugins {
					if string(content) != fmt.Sprint("set ", which) {
						failed <- fmt.Errorf("the set laid out for %d holds %q as %s", which, content, name)
					}
				}
			}
		}()
	}
	laying.Wait()
	close(failed)
	for err := range failed {
		t.Errorf("laying out at once: %v", err)
	}
	if entries, err := os.ReadDir(kept); err != nil || len(entries) != len(sets) {
		t.Fatalf("the sets kept are %v, %v, want the %d that were laid out", entries, err, len(sets))
	}
}

// With -dist each plugin's module is written as <name>.wasm, the name
// its directory's, and no example of the SDK; a module there already is
// written over, and one of a plugin that is gone is removed, so that the
// box holds no plugin the workspace does not. What a desktop leaves there
// is let be. A directory that holds anything else is no directory of
// plugins and is refused, before anything is written or removed.
func TestEachPluginIsWrittenAsItsNameAndAGoneOneIsRemoved(t *testing.T) {
	built := t.TempDir()
	write := func(dir, name, content string) string {
		t.Helper()
		at := filepath.Join(dir, name)
		if err := os.WriteFile(at, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return at
	}
	found := []module{
		{built: write(built, "frontseat_plugin_go.wasm", "go"), laid: "plugins/go/x.wasm", plugin: "go"},
		{built: write(built, "other-name.wasm", "odd"), laid: "plugins/odd/x.wasm", plugin: "odd"},
		{built: write(built, "options.wasm", "example"), laid: "sdk/plugin/x/options.wasm"},
	}
	dist := filepath.Join(t.TempDir(), "plugins")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	write(dist, "go.wasm", "old")
	write(dist, "gone.wasm", "a plugin no longer")
	write(dist, ".DS_Store", "desktop")
	if err := distribute(dist, found); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dist)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		content, _ := os.ReadFile(filepath.Join(dist, entry.Name()))
		names = append(names, entry.Name()+"="+string(content))
	}
	if want := []string{".DS_Store=desktop", "go.wasm=go", "odd.wasm=odd"}; !slices.Equal(names, want) {
		t.Fatalf("the directory holds %q, want %q", names, want)
	}

	source := t.TempDir()
	write(source, "Cargo.toml", "[package]\n")
	write(source, "stale.wasm", "kept")
	if err := distribute(source, found); err == nil || !strings.Contains(err.Error(), "Cargo.toml") {
		t.Fatalf("a directory that holds source was written to: %v", err)
	}
	if entries, _ := os.ReadDir(source); len(entries) != 2 {
		t.Fatalf("the refused directory holds %d files, want the two it held", len(entries))
	}
	// The control: a directory that is not there yet is made.
	if err := distribute(filepath.Join(t.TempDir(), "new"), found); err != nil {
		t.Fatal(err)
	}
}

// -dist names the build's own directory of plugins and no other, however
// it is spelt, since what is there that is no plugin's is removed.
func TestDistIsTheBuildsDirectoryAlone(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, given := range []string{"dist/plugins", "./dist/plugins/", filepath.Join(root, "dist", "plugins"), "dist/../dist/plugins"} {
		if at, err := distDirectory(root, given); err != nil || at != filepath.Join(root, "dist", "plugins") {
			t.Errorf("-dist %s: %q, %v", given, at, err)
		}
	}
	for _, given := range []string{"plugins", "dist", "dist/plugins/x", "/tmp/plugins", "../dist/plugins", filepath.Join(t.TempDir(), "dist", "plugins")} {
		if at, err := distDirectory(root, given); err == nil {
			t.Errorf("-dist %s was taken, as %s", given, at)
		}
	}
}

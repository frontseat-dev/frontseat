// Command prepare builds the wasm modules the tests load, lays them out
// as package modules reads them, and prints the directory they are laid
// out in: every plugin of the repository, and every example of the SDK
// that is a module, each where the crate that makes it leaves it. It is
// what compiles for the tests; the tests read what was prepared and
// compile nothing.
//
// A task that runs tests is told the directory it prints
// (FRONTSEAT_TEST_ARTIFACTS, in mise.toml), so each run of tests reads
// the modules that were built for it and no others. Which crates there
// are is cargo's own word (cargo metadata), and so are the file each
// module is (what cargo build says it made) and where what is built is
// kept: none of it is kept or worked out here.
//
// With -dist it writes each plugin's module, too, as <name>.wasm in the
// directory named, the name being its directory's beneath plugins/: what
// `mise run build` builds the box image from. It is the one program that
// builds the plugins, for the tests and for the box alike. It writes
// there each plugin's file whole, and removes the file of a plugin that
// is gone.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/frontseat-dev/frontseat/local"
	"github.com/google/renameio/v2"

	"github.com/frontseat-dev/frontseat/server/testsupport/testmodule"
)

// selection is one module cargo is asked to build: a crate's library,
// or one example of it.
type selection struct {
	// manifest is the crate's Cargo.toml, as cargo names it, and crate
	// its name.
	manifest, crate string
	// example is the example of the crate that is built; empty for the
	// crate's library.
	example string
	// directory is the crate's, in the repository, with slashes.
	directory string
}

// module is one module that was built, and where it is laid out.
type module struct {
	// built is the file cargo made.
	built string
	// laid is where the tests read it, beneath the layout's root.
	laid string
	// plugin is the name of the plugin it is, its directory's beneath
	// plugins/; empty for an example of the SDK.
	plugin string
}

func main() {
	dist := flag.String("dist", "", "write each plugin's module as <name>.wasm into this directory too, which holds nothing else")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: prepare [-dist DIR], in the repository; it prints where the modules are laid out")
	}
	flag.Parse()
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	set, err := prepare(*dist)
	if err != nil {
		fmt.Fprintln(os.Stderr, "prepare:", err)
		os.Exit(1)
	}
	fmt.Println(set)
}

func prepare(dist string) (string, error) {
	// Held to the lock, as every build of this repository is: preparing
	// the tests' modules changes nothing of it.
	said, err := cargo("metadata", "--locked", "--no-deps", "--format-version", "1")
	if err != nil {
		return "", err
	}
	chosen, kept, err := selected(said)
	if err != nil {
		return "", err
	}
	if len(chosen) == 0 {
		return "", fmt.Errorf("cargo names no plugin and no SDK example here")
	}
	var made []byte
	for _, selection := range builds(chosen) {
		// Its diagnostics are shown as it shows them; what it made is
		// read from what it prints.
		printed, err := cargo(append([]string{"build", "--locked", "--release", "--target", "wasm32-wasip1", "--message-format=json-render-diagnostics"}, selection...)...)
		if err != nil {
			return "", err
		}
		made = append(made, printed...)
	}
	found, err := built(chosen, made)
	if err != nil {
		return "", err
	}
	if dist != "" {
		root, err := local.Checkout()
		if err != nil {
			return "", err
		}
		at, err := distDirectory(root, dist)
		if err != nil {
			return "", err
		}
		if err := distribute(at, found); err != nil {
			return "", err
		}
	}
	return lay(kept, found)
}

// Dist is where the build lays the plugins out, beneath the repository's
// root: the one directory -dist may name, since what is there that is no
// plugin's now is removed.
const Dist = "dist/plugins"

// distDirectory returns the directory -dist names, if it is the build's
// own (Dist beneath root), however it is spelt; anything else is refused.
func distDirectory(root, given string) (string, error) {
	at, err := filepath.Abs(given)
	if err != nil {
		return "", err
	}
	if want := filepath.Join(root, filepath.FromSlash(Dist)); at != want {
		return "", fmt.Errorf("-dist %s is not the build's %s beneath %s, which alone this program writes and removes plugins in", given, Dist, root)
	}
	return at, nil
}

// distribute writes each plugin's module into dir as <name>.wasm, each
// file whole or as it was, and removes each <name>.wasm there that is no
// plugin's now. Those are this program's own to remove: the directory
// holds nothing but what it writes, which it checks before it changes
// anything, and a plugin that is gone must not stay in the box. A
// directory that holds anything else (but what a desktop leaves, a name
// beginning with a dot, which is let be) is no directory of plugins, and
// is refused before anything is written or removed.
func distribute(dir string, found []module) error {
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	plugins := map[string]bool{}
	for _, each := range found {
		if each.plugin != "" {
			plugins[each.plugin+".wasm"] = true
		}
	}
	var gone []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(name, ".wasm") {
			return fmt.Errorf("%s holds %s, and so is no directory of plugins: nothing is written there", dir, name)
		}
		if !plugins[name] {
			gone = append(gone, name)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, each := range found {
		if each.plugin == "" {
			continue
		}
		content, err := os.ReadFile(each.built)
		if err != nil {
			return err
		}
		if err := renameio.WriteFile(filepath.Join(dir, each.plugin+".wasm"), content, 0o644); err != nil {
			return err
		}
	}
	for _, name := range gone {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

// cargo runs cargo here, and returns what it printed.
func cargo(arguments ...string) ([]byte, error) {
	run := exec.Command("cargo", arguments...)
	run.Stderr = os.Stderr
	printed, err := run.Output()
	if err != nil {
		return nil, fmt.Errorf("cargo %s: %w", strings.Join(arguments, " "), err)
	}
	return printed, nil
}

// isModule reports whether a target of a crate is built as a module.
func isModule(crateTypes []string) bool { return slices.Contains(crateTypes, "cdylib") }

// selected reads what cargo said of the repository's crates (cargo
// metadata), and returns the modules among them: the library of a crate
// in a directory of plugins/, and each example of the crate in sdk/plugin/,
// where it is built as a module. It returns, too, where the sets of
// modules are kept: in cargo's own directory of what it builds, which is
// cleared when that is.
func selected(said []byte) (chosen []selection, kept string, err error) {
	var metadata struct {
		WorkspaceRoot   string `json:"workspace_root"`
		TargetDirectory string `json:"target_directory"`
		Packages        []struct {
			Name         string
			ManifestPath string `json:"manifest_path"`
			Targets      []struct {
				Name       string
				Kind       []string
				CrateTypes []string `json:"crate_types"`
			}
		}
	}
	if err := json.Unmarshal(said, &metadata); err != nil {
		return nil, "", fmt.Errorf("cargo metadata: %w", err)
	}
	if !filepath.IsAbs(metadata.TargetDirectory) {
		return nil, "", fmt.Errorf("cargo metadata names %q as where it builds, which is no full path", metadata.TargetDirectory)
	}
	for _, crate := range metadata.Packages {
		directory, err := filepath.Rel(metadata.WorkspaceRoot, filepath.Dir(crate.ManifestPath))
		if err != nil {
			return nil, "", err
		}
		directory = filepath.ToSlash(directory)
		for _, each := range crate.Targets {
			if !isModule(each.CrateTypes) {
				continue
			}
			example := slices.Contains(each.Kind, "example")
			switch {
			case filepath.Dir(directory) == "plugins" && !example:
				chosen = append(chosen, selection{manifest: crate.ManifestPath, crate: crate.Name, directory: directory})
			case directory == "sdk/plugin" && example:
				chosen = append(chosen, selection{manifest: crate.ManifestPath, crate: crate.Name, example: each.Name, directory: directory})
			}
		}
	}
	return chosen, filepath.Join(metadata.TargetDirectory, "test-modules"), nil
}

// builds returns how cargo is told to build what was chosen: the
// libraries together, and then the examples of each crate that has some,
// since cargo builds examples alone where it is told of one.
func builds(chosen []selection) [][]string {
	var libraries []string
	examples := map[string][]string{}
	for _, each := range chosen {
		if each.example == "" {
			libraries = append(libraries, "-p", each.crate)
		} else {
			examples[each.crate] = append(examples[each.crate], "--example", each.example)
		}
	}
	var out [][]string
	if len(libraries) > 0 {
		out = append(out, libraries)
	}
	for _, crate := range slices.Sorted(maps.Keys(examples)) {
		out = append(out, append([]string{"-p", crate}, examples[crate]...))
	}
	return out
}

// built reads what cargo build printed, a message a line, and returns
// each module that was chosen with the file cargo says it made, laid
// where its crate leaves it. One that cargo made no module for, or more
// than one, is a mistake that names it.
func built(chosen []selection, printed []byte) ([]module, error) {
	type made struct{ manifest, example string }
	files := map[made][]string{}
	lines := bufio.NewScanner(bytes.NewReader(printed))
	lines.Buffer(nil, 16<<20)
	for lines.Scan() {
		var message struct {
			Reason       string
			ManifestPath string `json:"manifest_path"`
			Target       struct {
				Name       string
				Kind       []string
				CrateTypes []string `json:"crate_types"`
			}
			Filenames []string
		}
		if err := json.Unmarshal(lines.Bytes(), &message); err != nil {
			return nil, fmt.Errorf("cargo build printed what is no message: %w", err)
		}
		if message.Reason != "compiler-artifact" || !isModule(message.Target.CrateTypes) {
			continue
		}
		of := made{manifest: message.ManifestPath}
		if slices.Contains(message.Target.Kind, "example") {
			of.example = message.Target.Name
		}
		for _, file := range message.Filenames {
			if strings.HasSuffix(file, ".wasm") && !slices.Contains(files[of], file) {
				files[of] = append(files[of], file)
			}
		}
	}
	if err := lines.Err(); err != nil {
		return nil, err
	}
	found := make([]module, 0, len(chosen))
	for _, each := range chosen {
		what, plugin := each.crate, path.Base(each.directory)
		beneath := testmodule.Released
		if each.example != "" {
			what, plugin, beneath = each.crate+"'s example "+each.example, "", testmodule.Released+"/examples"
		}
		made := files[made{each.manifest, each.example}]
		if len(made) != 1 {
			return nil, fmt.Errorf("cargo made %d modules for %s, and one is laid out: %v", len(made), what, made)
		}
		found = append(found, module{built: made[0], laid: filepath.Join(filepath.FromSlash(each.directory), filepath.FromSlash(beneath), filepath.Base(made[0])), plugin: plugin})
	}
	return found, nil
}

// lay lays the modules out as one set, and returns its directory. It
// replaces and removes nothing that was there.
//
// Each set is a directory, among those kept, named by what it holds:
// the same modules are the same directory, made once and never changed
// after. A set is copied into a directory of this call's own making
// beside the others, which becomes the set by one rename; where another
// call made the same set first, this one's copy is dropped.
//
// Sets of earlier moments stay where they are: nothing here can tell
// one that a test still reads from one that none does. They are in
// cargo's own directory of what was built, and go when that is cleared.
func lay(kept string, found []module) (string, error) {
	// What the set holds names it: where each module is laid, and its content.
	found = slices.SortedFunc(slices.Values(found), func(a, b module) int { return strings.Compare(a.laid, b.laid) })
	contents := make([][]byte, len(found))
	named := sha256.New()
	for i, each := range found {
		content, err := os.ReadFile(each.built)
		if err != nil {
			return "", err
		}
		contents[i] = content
		fmt.Fprintf(named, "%s\x00%x\n", filepath.ToSlash(each.laid), sha256.Sum256(content))
	}
	set := filepath.Join(kept, hex.EncodeToString(named.Sum(nil)))
	if _, err := os.Lstat(set); err == nil {
		// A set is named by what it holds, and is held to its name where
		// it is found: one that something emptied or changed is not
		// handed to a test as whole, and is not this program's to mend.
		for i, each := range found {
			laid, err := os.ReadFile(filepath.Join(set, each.laid))
			if err != nil || !bytes.Equal(laid, contents[i]) {
				return "", fmt.Errorf("%s is there and does not hold the modules it is named by (%s): something changed it after it was laid out; remove it, and it is laid out anew", set, filepath.ToSlash(each.laid))
			}
		}
		return set, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(kept, 0o755); err != nil {
		return "", err
	}
	making, err := os.MkdirTemp(kept, "making-")
	if err != nil {
		return "", err
	}
	// This call's own, made a moment ago: gone unless it becomes the set.
	defer os.RemoveAll(making)
	if err := os.Chmod(making, 0o755); err != nil {
		return "", err
	}
	for i, each := range found {
		at := filepath.Join(making, each.laid)
		if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
			return "", err
		}
		// Not to be written again: a set is never changed after.
		if err := os.WriteFile(at, contents[i], 0o444); err != nil {
			return "", err
		}
	}
	// Another call may have made the same set meanwhile: it is the same,
	// and this copy is dropped.
	if err := os.Rename(making, set); err != nil {
		if _, there := os.Lstat(set); there != nil {
			return "", err
		}
	}
	return set, nil
}

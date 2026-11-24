package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// numbered returns what a Go source file's variable holds, where it is
// a map written out from numbers to texts; nothing where the file
// declares no such variable.
func numbered(t *testing.T, source, variable string) map[uint64]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var held map[uint64]string
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || spec.Names[0].Name != variable || len(spec.Values) != 1 {
			return true
		}
		written, ok := spec.Values[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		held = map[uint64]string{}
		for _, each := range written.Elts {
			pair := each.(*ast.KeyValueExpr)
			number, err := strconv.ParseUint(pair.Key.(*ast.BasicLit).Value, 0, 64)
			if err != nil {
				t.Fatal(err)
			}
			text, err := strconv.Unquote(pair.Value.(*ast.BasicLit).Value)
			if err != nil {
				t.Fatal(err)
			}
			held[number] = text
		}
		return false
	})
	return held
}

// The file systems the stack's spec takes a build daemon to trust are
// the ones it does trust: the spec's list, which cannot import the
// engine's, is the engine's (linuxDates). The control is the engine's
// list with one more on it, which is not the spec's.
func TestTheFileSystemsASpecTrustsAreTheOnesABuildDaemonTrusts(t *testing.T) {
	root := repositoryRoot(t)
	read := func(path string) string {
		t.Helper()
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	trusted := numbered(t, read("build/engine/engine/source/filesystem.go"), "linuxDates")
	if len(trusted) == 0 {
		t.Fatal("the build engine names no file system it trusts (linuxDates): nothing was held to it")
	}
	mirrored := numbered(t, read("specs/internal/dated/dated.go"), "Linux")
	if !maps.Equal(trusted, mirrored) {
		t.Errorf("the spec takes a build daemon to trust %v, and it trusts %v: they are one list, written twice", mirrored, trusted)
	}
	one := numbered(t, "package source\nvar linuxDates = map[uint32]string{0x0000ef53: \"ext4\"}\n", "linuxDates")
	more := numbered(t, "package source\nvar linuxDates = map[uint32]string{0x0000ef53: \"ext4\", 0x65735546: \"fuse\"}\n", "linuxDates")
	if len(one) != 1 || one[0xef53] != "ext4" || maps.Equal(one, more) || more[0x65735546] != "fuse" {
		t.Errorf("a list of one read as %v and one with another on it as %v; want them told apart", one, more)
	}
	if none := numbered(t, "package source\nvar other = 1\n", "linuxDates"); none != nil {
		t.Errorf("a file that declares no such list read as %v; want nothing", none)
	}
}

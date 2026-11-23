package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/testsupport/testmodule"
)

// opens are the calls that open, resolve or move to a path: a test that
// gives one a path that climbs out of where it runs looks up for what it
// reads, where it is to be told (testmodule.SourceVariable). A call of
// another package named as one of these (local.Read) opens too.
var opens = map[string][]string{
	"os":       {"ReadFile", "Open", "OpenFile", "ReadDir", "Stat", "Lstat", "DirFS", "OpenRoot", "Chdir"},
	"filepath": {"Glob", "Abs", "EvalSymlinks", "WalkDir", "Walk"},
	"":         {"Read", "ReadFile", "Open", "Load", "Stat", "Chdir"},
}

// climbs are the places a Go file opens a path that climbs: a call above
// given one, or a command's directory (Dir) set to one. A path climbs
// where it is "..", or begins "../" or "./../", whether written out, a
// constant of the file, or a part of what filepath.Join joins. A test
// that climbs within its own fixture writes the fixture's path whole.
func climbs(file *ast.File) []string {
	consts := stringConstants(file)
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		switch at := n.(type) {
		case *ast.CallExpr:
			if name, ok := opening(at.Fun); ok && len(at.Args) > 0 && up(at.Args[0], consts) {
				found = append(found, name)
			}
		case *ast.AssignStmt:
			for i, lhs := range at.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "Dir" && i < len(at.Rhs) && up(at.Rhs[i], consts) {
					found = append(found, "a command's Dir")
				}
			}
		}
		return true
	})
	return found
}

// stringConstants are the constants a Go file declares as string
// literals, by their names.
func stringConstants(file *ast.File) map[string]string {
	consts := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value := spec.(*ast.ValueSpec)
			for i, name := range value.Names {
				if i < len(value.Values) {
					if lit, ok := value.Values[i].(*ast.BasicLit); ok {
						if said, err := strconv.Unquote(lit.Value); err == nil {
							consts[name.Name] = said
						}
					}
				}
			}
		}
	}
	return consts
}

// opening says whether a call opens a path, and names it.
func opening(fun ast.Expr) (string, bool) {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg := ""
	if ident, ok := sel.X.(*ast.Ident); ok {
		pkg = ident.Name
	}
	if names, known := opens[pkg]; known && slices.Contains(names, sel.Sel.Name) {
		return pkg + "." + sel.Sel.Name, true
	}
	if slices.Contains(opens[""], sel.Sel.Name) {
		return pkg + "." + sel.Sel.Name, true
	}
	return "", false
}

// up says whether a path climbs.
func up(arg ast.Expr, consts map[string]string) bool {
	climbing := func(value string) bool {
		value = strings.TrimPrefix(value, "./")
		return value == ".." || strings.HasPrefix(value, "../")
	}
	switch at := arg.(type) {
	case *ast.BasicLit:
		value, err := strconv.Unquote(at.Value)
		return err == nil && climbing(value)
	case *ast.Ident:
		value, ok := consts[at.Name]
		return ok && climbing(value)
	case *ast.CallExpr:
		if sel, ok := at.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Join" {
			return slices.ContainsFunc(at.Args, func(part ast.Expr) bool { return up(part, consts) })
		}
	}
	return false
}

// rustClimb is a Rust path that climbs: joined from "..", made of one, or
// the crate's own directory with ".." after it. What a crate reads beyond
// itself it is told where to find, as a Go test is; a plugin's own code,
// which runs in no directory, has none either.
var rustClimb = regexp.MustCompile(`\.join\("\.\.("|/)|Path::new\("\.\.("|/)|CARGO_MANIFEST_DIR"\)[^;]*"/?\.\.`)

// No test looks up for what it reads, by a path that climbs out of where
// it runs: it is told where its repository is (testmodule.SourceVariable)
// and reads there, as its Component's catalog file names it. The controls
// are a fixture of each that climbs, refused, beside one that reads where
// it is told, allowed.
func TestNoTestLooksUpForWhatItReads(t *testing.T) {
	for name, c := range map[string]struct {
		source string
		climbs bool
	}{
		"a literal path that climbs":     {`package a; import "os"; func f() { os.ReadFile("../../x/y.json") }`, true},
		"a path joined from ..":          {`package a; import ("os"; "path/filepath"); func f() { os.Stat(filepath.Join("..", "x")) }`, true},
		"a path from where it is told":   {`package a; import ("os"; "path/filepath"); func f(root string) { os.ReadFile(filepath.Join(root, "x")) }`, false},
		"a link that leads above":        {`package a; import ("os"; "path/filepath"); func f() { os.Symlink(filepath.Join("..", "x"), "y") }`, false},
		"a later part that climbs":       {`package a; import ("os"; "path/filepath"); func f(wd string) { os.ReadDir(filepath.Join(wd, "..")) }`, true},
		"a constant that climbs":         {`package a; import "os"; const above = "./../x"; func f() { os.Open(above) }`, true},
		"moving up":                      {`package a; import "testing"; func f(t *testing.T) { t.Chdir("..") }`, true},
		"a command run above":            {`package a; import "os/exec"; func f() { c := exec.Command("x"); c.Dir = "../.." }`, true},
		"another package's reader":       {`package a; import "x/local"; func f() { local.Read("..") }`, true},
		"a command run where it is told": {`package a; import "os/exec"; func f(root string) { c := exec.Command("x"); c.Dir = root }`, false},
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "a_test.go", c.source, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(climbs(file)) > 0; got != c.climbs {
			t.Errorf("%s: climbs %v, want %v", name, got, c.climbs)
		}
	}
	for rust, climbs := range map[string]bool{
		`manifest.join("../../../api")`:                  true,
		`Path::new("../x")`:                              true,
		`concat!(env!("CARGO_MANIFEST_DIR"), "/../api")`: true,
		`root.join("api")`:                               false,
		`concat!(env!("CARGO_MANIFEST_DIR"), "/src")`:    false,
	} {
		if rustClimb.MatchString(rust) != climbs {
			t.Errorf("the Rust check takes %s to climb: %v, want %v", rust, !climbs, climbs)
		}
	}

	root := repositoryRoot(t)
	for _, source := range goSources(t, root, root) {
		if !strings.HasSuffix(source.rel, "_test.go") && !strings.HasPrefix(source.rel, "testsupport/") {
			continue
		}
		for _, call := range climbs(source.file) {
			t.Errorf("%s opens a path that climbs (%s): a test is told where its repository is (%s), and reads there", source.rel, call, testmodule.SourceVariable)
		}
	}
	for _, file := range repositoryFiles(t, root) {
		if !strings.HasSuffix(file, ".rs") || strings.Contains(file, "/testdata/") || strings.HasPrefix(file, "sdk/plugin/src/gen/") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		if rustClimb.Match(raw) {
			t.Errorf("%s joins a path that climbs: a test is told where its repository is (%s), and reads there", file, testmodule.SourceVariable)
		}
	}
}

// What a test is told of its repository is said by one name, in the
// places that must say it, each of its own language or module: the host
// that tells it, the test support that reads it, the Rust generator's
// test, and the task that runs the Rust tests. Nothing else says it, so a
// name changed in one place is a name changed in all.
func TestWhatATestIsToldOfItsRepositoryIsSaidByOneName(t *testing.T) {
	root := repositoryRoot(t)
	saying := []string{
		"build/engine/exec/stage/stage.go",
		"testsupport/testmodule/testmodule.go",
		"sdk/plugin/generate/src/main.rs",
		"mise.toml",
	}
	var said []string
	for _, file := range repositoryFiles(t, root) {
		if !strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, ".rs") && file != "mise.toml" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), `"`+testmodule.SourceVariable+`"`) || strings.Contains(string(raw), testmodule.SourceVariable+"=") {
			said = append(said, file)
		}
	}
	slices.Sort(said)
	slices.Sort(saying)
	if !slices.Equal(said, saying) {
		t.Errorf("%s is said in %v, want %v", testmodule.SourceVariable, said, saying)
	}
}

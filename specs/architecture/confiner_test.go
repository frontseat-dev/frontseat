package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// frontseat-action is confine's and nothing else, so that its form, the
// digest of confine.go and routes.go, is what the program does
// (server/internal/confine): its directory holds main.go alone, which
// imports confine alone, by its own name, and whose main calls
// confine.Run and does nothing more, with no directive to the go command;
// and confine's holds exactly those source files, beside its tests, importing the
// standard library alone, which the form holds by the Go release. A file
// of any other kind in either, assembly or an object to link, is refused
// too; what git ignores (a desktop's or an editor's own files) is not the
// repository's, and is passed by.
// It is read here, from the repository, and not by a test of the program,
// which is given no source to read.
func TestTheConfinerIsConfineAlone(t *testing.T) {
	programIsItsPackageAlone(t, "frontseat-action", "confine", []string{"confine.go", "routes.go"}, "confine_test.go", "doc.go", "program_test.go")
}

// frontseat-place, what a fetch's command runs in place of a shell, is
// place's and nothing else, as the confiner is confine's, so that its
// form, the digest of place.go, is what it does to a fetch
// (server/internal/place).
func TestThePlacerIsPlaceAlone(t *testing.T) {
	programIsItsPackageAlone(t, "frontseat-place", "place", []string{"place.go"}, "doc.go", "place_test.go")
}

// programIsItsPackageAlone holds a program of server/cmd to the one
// package of server/internal its form is the digest of: its directory
// holds main.go alone, which calls the package's Run and does nothing
// more; the package's holds exactly the sources hashed and the others
// named, and the hashed import the standard library alone.
func programIsItsPackageAlone(t *testing.T, program, pkg string, hashed []string, others ...string) {
	t.Helper()
	root := repositoryRoot(t)
	dir := filepath.Join(root, "server", "cmd", program)
	if files := filesIn(t, dir); !slices.Equal(files, []string{"main.go"}) {
		t.Fatalf("%s's directory holds %v, want main.go alone", program, files)
	}
	want := slices.Sorted(slices.Values(append(slices.Clone(hashed), others...)))
	if files := filesIn(t, filepath.Join(root, "server", "internal", pkg)); !slices.Equal(files, want) {
		t.Fatalf("%s's directory holds %v, want %v: its hashed sources, their tests and its doc", pkg, files, want)
	}
	if err := programProblem(filepath.Join(dir, "main.go"), pkg); err != "" {
		t.Fatal(err)
	}
	for _, source := range hashed {
		if err := standardAlone(filepath.Join(root, "server", "internal", pkg, source)); err != "" {
			t.Fatal(err)
		}
	}
}

// standardAlone says what a file imports that is not of the standard
// library, or nothing.
func standardAlone(at string) string {
	file, err := parser.ParseFile(token.NewFileSet(), at, nil, parser.ImportsOnly)
	if err != nil {
		return err.Error()
	}
	for _, imported := range file.Imports {
		path, _ := strconv.Unquote(imported.Path.Value)
		if first, _, _ := strings.Cut(path, "/"); strings.Contains(first, ".") {
			return filepath.Base(at) + " imports " + path + ", which is not of the standard library: the form would not follow it"
		}
	}
	return ""
}

// The program's main is held to its one shape: each other shape is
// refused, and the program as it is passes, for each such program.
func TestAConfinerThatDoesMoreIsRefused(t *testing.T) {
	for _, pkg := range []string{"confine", "place"} {
		mainThatDoesMoreIsRefused(t, pkg)
	}
}

// mainThatDoesMoreIsRefused is the shapes of a main over a package, the
// one it must have and each it must not.
func mainThatDoesMoreIsRefused(t *testing.T, pkg string) {
	good := strings.ReplaceAll("package main\n\nimport \"github.com/frontseat-dev/frontseat/server/internal/confine\"\n\nfunc main() { confine.Run() }\n", "confine", pkg)
	for way, source := range map[string]string{
		"as it is":                 good,
		"importing more":           strings.Replace(good, "import \"", "import _ \"os\"\nimport \"", 1),
		"importing it as another":  strings.Replace(good, "import \"", "import c \"", 1),
		"with a directive":         strings.Replace(good, "package main", "//go:debug madvdontneed=1\npackage main", 1),
		"declaring more":           good + "\nfunc init() {}\n",
		"doing more in main":       strings.Replace(good, "{ "+pkg+".Run() }", "{ println(); "+pkg+".Run() }", 1),
		"calling another Run":      strings.Replace(good, pkg+".Run()", "a.b.Run()", 1),
		"with a statement no call": strings.Replace(good, "{ "+pkg+".Run() }", "{ _ = 1 }", 1),
		"calling Run with more":    strings.Replace(good, pkg+".Run()", pkg+".Run(1)", 1),
		"over the other package":   strings.ReplaceAll(good, pkg, map[string]string{"confine": "place", "place": "confine"}[pkg]),
		"with a line directive":    strings.Replace(good, "func main()", "/*line x.go:1*/ func main()", 1),
	} {
		at := filepath.Join(t.TempDir(), "main.go")
		if err := os.WriteFile(at, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		problem := programProblem(at, pkg)
		if way == "as it is" && problem != "" {
			t.Errorf("%s: the program as it is is refused: %s", pkg, problem)
		}
		if way != "as it is" && problem == "" {
			t.Errorf("%s: a main.go %s passed", pkg, way)
		}
	}
}

// programProblem says what of a main.go is more than a call of the
// package's Run, or nothing.
func programProblem(at, pkg string) string {
	file, err := parser.ParseFile(token.NewFileSet(), at, nil, parser.ParseComments)
	if err != nil {
		return err.Error()
	}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if strings.HasPrefix(comment.Text, "//go:") || strings.HasPrefix(comment.Text, "//line") || strings.HasPrefix(comment.Text, "/*line") {
				return "main.go holds a directive to the go command: " + comment.Text
			}
		}
	}
	if len(file.Imports) != 1 || file.Imports[0].Name != nil {
		return "main.go imports something other than " + pkg + ", by its own name, alone"
	}
	if path, _ := strconv.Unquote(file.Imports[0].Path.Value); path != module+"/server/internal/"+pkg {
		return "main.go imports " + path + ", not " + pkg
	}
	if len(file.Decls) != 2 {
		return "main.go declares more than its import and main"
	}
	main, is := file.Decls[1].(*ast.FuncDecl)
	if !is || main.Name.Name != "main" || main.Recv != nil || main.Body == nil || len(main.Body.List) != 1 {
		return "main.go's second declaration is not a main of one statement"
	}
	statement, is := main.Body.List[0].(*ast.ExprStmt)
	if !is {
		return "main's one statement is not a call"
	}
	run, is := statement.X.(*ast.CallExpr)
	if !is || len(run.Args) != 0 {
		return "main's one statement is not a call of nothing"
	}
	selector, is := run.Fun.(*ast.SelectorExpr)
	if !is || selector.Sel.Name != "Run" {
		return "main calls something other than Run"
	}
	if name, is := selector.X.(*ast.Ident); !is || name.Name != pkg {
		return "main calls a Run other than " + pkg + "'s"
	}
	return ""
}

// filesIn are the names of the files of a directory, of every kind, but
// those git ignores, which are no part of the repository.
func filesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if root := repositoryRoot(t); !checkout(root) {
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			t.Fatal(err)
		}
		matcher := untracked(t, root)
		var out []string
		for _, entry := range entries {
			if !matcher.Match(append(strings.Split(filepath.ToSlash(rel), "/"), entry.Name()), entry.IsDir()) {
				out = append(out, entry.Name())
			}
		}
		return out
	}
	asked := exec.Command("git", "-C", dir, "check-ignore", "--stdin")
	asked.Stdin = strings.NewReader(strings.Join(names, "\n") + "\n")
	said, err := asked.Output()
	// check-ignore exits 1 where nothing is ignored.
	if err != nil {
		if exit, is := err.(*exec.ExitError); !is || exit.ExitCode() != 1 {
			t.Fatalf("asking git what it ignores in %s: %v", dir, err)
		}
	}
	ignored := strings.Fields(string(said))
	var out []string
	for _, name := range names {
		if !slices.Contains(ignored, name) {
			out = append(out, name)
		}
	}
	return out
}

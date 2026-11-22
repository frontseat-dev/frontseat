package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// skippedOutright returns each test of a Go test file that begins by
// skipping itself, whatever it is given: its first statement is a call
// of Skip, Skipf or SkipNow on its own parameter. Such a test runs
// nothing and is counted beside those that do.
func skippedOutright(name string, source []byte) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), name, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var found []string
	for _, declared := range file.Decls {
		function, ok := declared.(*ast.FuncDecl)
		if !ok || function.Recv != nil || function.Body == nil || len(function.Body.List) == 0 || !strings.HasPrefix(function.Name.Name, "Test") {
			continue
		}
		parameters := function.Type.Params.List
		if len(parameters) != 1 || len(parameters[0].Names) != 1 {
			continue
		}
		statement, ok := function.Body.List[0].(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := statement.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		selected, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		on, ok := selected.X.(*ast.Ident)
		if ok && on.Name == parameters[0].Names[0].Name && slices.Contains([]string{"Skip", "Skipf", "SkipNow"}, selected.Sel.Name) {
			found = append(found, function.Name.Name)
		}
	}
	return found, nil
}

// No test begins by skipping itself: a requirement that is not built is
// in its design, not in the test binary, where it is counted as a spec
// and proves nothing. A test that skips where something it needs is
// missing asks for it first.
func TestNoTestIsSkippedOutright(t *testing.T) {
	root := repositoryRoot(t)
	for _, file := range repositoryFiles(t, root) {
		if !strings.HasSuffix(file, "_test.go") || slices.Contains(strings.Split(file, "/"), "testdata") {
			continue
		}
		source, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		found, err := skippedOutright(file, source)
		if err != nil {
			t.Fatal(err)
		}
		for _, test := range found {
			t.Errorf("%s: %s begins by skipping itself and runs nothing: delete it, or skip only where what it needs is missing", file, test)
		}
	}
}

func TestATestSkippedOutrightIsFound(t *testing.T) {
	const source = `package p

import "testing"

func TestPending(t *testing.T) { t.Skip("Pending requirement"); t.Fatal("never") }
func TestPendingByAnotherName(tb *testing.T) { tb.Skipf("pending %s", "x") }
func TestPendingNow(t *testing.T) { t.SkipNow() }
func TestSkippedWhereSomethingIsMissing(t *testing.T) { if testing.Short() { t.Skip("slow") }; t.Log("ran") }
func TestSkippedAfterItAsked(t *testing.T) { t.Helper(); t.Skip("no grid") }
func TestThatRuns(t *testing.T) { t.Log("ran") }
func TestOfAnother(t *testing.T) { other.Skip("x") }
func helper(t *testing.T) { t.Skip("a helper's skip is its caller's to take") }
type suite struct{}
func (suite) TestAMethod(t *testing.T) { t.Skip("no test of go's") }
`
	found, err := skippedOutright("a/a_test.go", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"TestPending", "TestPendingByAnotherName", "TestPendingNow"}; !slices.Equal(found, want) {
		t.Errorf("found %q, want %q", found, want)
	}
}

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

// skippedOutright returns each test, benchmark and fuzz test of a Go
// test file that skips itself whatever it is given: a call of Skip,
// Skipf or SkipNow on its own parameter that is a statement of its body,
// at any place in it, or of an if whose condition is the constant true,
// or the same in the body of a function literal given to Run. A skip
// that is taken only where something is missing is inside an if that
// asks for it. Such a test runs nothing and is counted beside those that
// do.
func skippedOutright(name string, source []byte) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), name, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var found []string
	for _, declared := range file.Decls {
		function, ok := declared.(*ast.FuncDecl)
		if !ok || function.Recv != nil || function.Body == nil || !slices.ContainsFunc([]string{"Test", "Benchmark", "Fuzz"}, func(prefix string) bool {
			return strings.HasPrefix(function.Name.Name, prefix)
		}) {
			continue
		}
		skips := false
		if parameter, ok := soleParameter(function.Type); ok {
			skips = skipsItself(function.Body.List, parameter)
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			if selected, ok := call.Fun.(*ast.SelectorExpr); !ok || selected.Sel.Name != "Run" {
				return true
			}
			literal, ok := call.Args[len(call.Args)-1].(*ast.FuncLit)
			if !ok {
				return true
			}
			if parameter, ok := soleParameter(literal.Type); ok && skipsItself(literal.Body.List, parameter) {
				skips = true
			}
			return true
		})
		if skips {
			found = append(found, function.Name.Name)
		}
	}
	return found, nil
}

// soleParameter is the name of a function's one parameter.
func soleParameter(function *ast.FuncType) (string, bool) {
	parameters := function.Params.List
	if len(parameters) != 1 || len(parameters[0].Names) != 1 {
		return "", false
	}
	return parameters[0].Names[0].Name, true
}

// skipsItself says whether a statement of a body, or of an if of the
// constant true in it, calls Skip, Skipf or SkipNow on the parameter.
func skipsItself(statements []ast.Stmt, parameter string) bool {
	for _, statement := range statements {
		switch statement := statement.(type) {
		case *ast.ExprStmt:
			call, ok := statement.X.(*ast.CallExpr)
			if !ok {
				continue
			}
			selected, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			on, ok := selected.X.(*ast.Ident)
			if ok && on.Name == parameter && slices.Contains([]string{"Skip", "Skipf", "SkipNow"}, selected.Sel.Name) {
				return true
			}
		case *ast.IfStmt:
			if always, ok := statement.Cond.(*ast.Ident); ok && always.Name == "true" && skipsItself(statement.Body.List, parameter) {
				return true
			}
		}
	}
	return false
}

// No Go test skips itself whatever it is given: a requirement that is
// not built is in its design, not in the test binary, where it is
// counted as a spec and proves nothing. A test that skips where
// something it needs is missing asks for it first. A Rust test marked
// ignored is not read here.
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
			t.Errorf("%s: %s skips itself whatever it is given and runs nothing: delete it, or skip only where what it needs is missing", file, test)
		}
	}
}

func TestATestSkippedOutrightIsFound(t *testing.T) {
	const source = `package p

import "testing"

func TestPending(t *testing.T) { t.Skip("Pending requirement"); t.Fatal("never") }
func TestPendingByAnotherName(tb *testing.T) { tb.Skipf("pending %s", "x") }
func TestPendingNow(t *testing.T) { t.SkipNow() }
func TestSkippedAfterItAsked(t *testing.T) { t.Helper(); t.Skip("no grid") }
func TestSkippedAfterParallel(t *testing.T) { t.Parallel(); t.Log("x"); t.Skip("pending") }
func TestSkippedIfTrue(t *testing.T) { if true { t.Skip("pending") } }
func TestASubtestSkipped(t *testing.T) { t.Run("one", func(s *testing.T) { s.Skip("pending") }) }
func BenchmarkPending(b *testing.B) { b.Skip("pending") }
func FuzzPending(f *testing.F) { f.Skip("pending") }
func TestSkippedWhereSomethingIsMissing(t *testing.T) { if testing.Short() { t.Skip("slow") }; t.Log("ran") }
func TestASubtestSkippedWhereSomethingIsMissing(t *testing.T) { t.Run("one", func(s *testing.T) { if testing.Short() { s.Skip("slow") } }) }
func TestThatRuns(t *testing.T) { t.Log("ran") }
func TestOfAnother(t *testing.T) { other.Skip("x") }
func BenchmarkThatRuns(b *testing.B) { for b.Loop() {} }
func helper(t *testing.T) { t.Skip("a helper's skip is its caller's to take") }
type suite struct{}
func (suite) TestAMethod(t *testing.T) { t.Skip("no test of go's") }
`
	found, err := skippedOutright("a/a_test.go", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"TestPending", "TestPendingByAnotherName", "TestPendingNow", "TestSkippedAfterItAsked", "TestSkippedAfterParallel", "TestSkippedIfTrue", "TestASubtestSkipped", "BenchmarkPending", "FuzzPending"}
	if !slices.Equal(found, want) {
		t.Errorf("found %q, want %q", found, want)
	}
}

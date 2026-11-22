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
	"unicode"
)

// words are the words a Go name is written in, by its case and its
// underscores: TestMiseServicePOCRejects is Test, Mise, Service, POC and
// Rejects.
func words(name string) []string {
	var out []string
	runes := []rune(name)
	start := 0
	for i := 1; i <= len(runes); i++ {
		if i < len(runes) && runes[i] != '_' {
			upper := unicode.IsUpper(runes[i])
			afterLower := !unicode.IsUpper(runes[i-1]) && runes[i-1] != '_'
			beforeLower := i+1 < len(runes) && unicode.IsLower(runes[i+1]) && unicode.IsUpper(runes[i-1])
			if !upper || !(afterLower || beforeLower) {
				continue
			}
		}
		if word := strings.Trim(string(runes[start:i]), "_"); word != "" {
			out = append(out, word)
		}
		start = i
	}
	return out
}

// occasionedTests returns each test, benchmark, fuzz test and example a
// Go test file declares that is named for an occasion (anOccasion), a
// word of its name being one.
func occasionedTests(name string, source []byte) ([]string, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), name, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, declared := range parsed.Decls {
		function, ok := declared.(*ast.FuncDecl)
		if !ok || function.Recv != nil {
			continue
		}
		name := function.Name.Name
		if !strings.HasPrefix(name, "Test") && !strings.HasPrefix(name, "Benchmark") && !strings.HasPrefix(name, "Fuzz") && !strings.HasPrefix(name, "Example") {
			continue
		}
		for _, word := range words(name) {
			if slices.Contains(anOccasion, strings.ToLower(word)) {
				out = append(out, name)
				break
			}
		}
	}
	return out, nil
}

// A test is named for its subject, never for the occasion it was written
// on, as its file is: no word of a Go test's name is an occasion's.
func TestATestIsNamedForItsSubject(t *testing.T) {
	root := repositoryRoot(t)
	for _, file := range repositoryFiles(t, root) {
		if !strings.HasSuffix(file, "_test.go") || slices.Contains(strings.Split(file, "/"), "testdata") {
			continue
		}
		source, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		named, err := occasionedTests(file, source)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range named {
			t.Errorf("%s: %s is named for an occasion (%v): name it for what it holds", file, name, anOccasion)
		}
	}
}

func TestATestNamedForAnOccasionIsFound(t *testing.T) {
	const source = `package a

import "testing"

func TestReviewAPartIsHeld(t *testing.T)                   {}
func TestMiseServicePOCRejectsAWrongChecksum(t *testing.T) {}
func TestProductionReviewSpellings(t *testing.T)           {}
func TestAProofIsKept(t *testing.T)                        {}
func Test_review_by_underscores(t *testing.T)              {}
func BenchmarkReviewOfParts(b *testing.B)                  {}
func FuzzThePOC(f *testing.F)                              {}
func ExampleReview()                                       {}
func TestAReviewerIsNamed(t *testing.T)                    {}
func TestProofreadingIsASubject(t *testing.T)              {}
func TestAPocketHoldsIt(t *testing.T)                      {}
func TestAPartIsHeld(t *testing.T)                         {}
func reviewHelper(t *testing.T)                            {}

type s struct{}

func (s) TestReviewOfAMethod() {}
`
	got, err := occasionedTests("a/a_test.go", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"TestReviewAPartIsHeld", "TestMiseServicePOCRejectsAWrongChecksum", "TestProductionReviewSpellings", "TestAProofIsKept", "Test_review_by_underscores", "BenchmarkReviewOfParts", "FuzzThePOC", "ExampleReview"}
	if !slices.Equal(got, want) {
		t.Errorf("found %q named for an occasion, want %q", got, want)
	}
}

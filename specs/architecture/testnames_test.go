package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
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

// ofAnOccasion reports whether a word of a name is an occasion's
// (anOccasion): the word itself, however it is cased, several of it
// (Reviews) or one of several (Review2, POC2), and a word in capitals
// that ends in one, since a name's article joins an acronym after it
// (APOC, APIPOC).
func ofAnOccasion(word string) bool {
	word = strings.TrimRight(word, "0123456789")
	lower := strings.TrimSuffix(strings.ToLower(word), "s")
	if slices.Contains(anOccasion, lower) {
		return true
	}
	if word != strings.ToUpper(word) {
		return false
	}
	return slices.ContainsFunc(anOccasion, func(occasion string) bool { return strings.HasSuffix(lower, occasion) })
}

// occasionedTests returns each test, benchmark, fuzz test and example a
// Go test file declares that is named for an occasion (anOccasion), a
// word of its name being one, or two of them where the second is the one
// capital an occasion is spelled to end in (PoC).
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
		in := words(name)
		for i, word := range in {
			if i+1 < len(in) && len(in[i+1]) == 1 {
				word += in[i+1]
			}
			if ofAnOccasion(in[i]) || ofAnOccasion(word) {
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
	goTestFiles(t, root, func(file string, source []byte) {
		named, err := occasionedTests(file, source)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range named {
			t.Errorf("%s: %s is named for an occasion (%v): name it for what it holds", file, name, anOccasion)
		}
	})
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
func TestAPOCIsKept(t *testing.T)                          {}
func TestServicePoCRejects(t *testing.T)                   {}
func TestReview2Parts(t *testing.T)                        {}
func TestPOC2(t *testing.T)                                {}
func TestAReviewsList(t *testing.T)                        {}
func TestProofsHold(t *testing.T)                          {}
func TestAPIPOCThing(t *testing.T)                         {}
func TestTheRepoCIsBuilt(t *testing.T)                     {}
func TestAnEPOCHIsRead(t *testing.T)                       {}
func TestPocketsHoldThem(t *testing.T)                     {}
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
	want := []string{"TestReviewAPartIsHeld", "TestMiseServicePOCRejectsAWrongChecksum", "TestProductionReviewSpellings", "TestAProofIsKept", "Test_review_by_underscores", "BenchmarkReviewOfParts", "FuzzThePOC", "ExampleReview", "TestAPOCIsKept", "TestServicePoCRejects", "TestReview2Parts", "TestPOC2", "TestAReviewsList", "TestProofsHold", "TestAPIPOCThing"}
	if !slices.Equal(got, want) {
		t.Errorf("found %q named for an occasion, want %q", got, want)
	}
}

// aTestNamed is a Go test as prose names it, and aTestDeclared one as
// its file declares it, a method of a suite too.
var (
	aTestNamed    = regexp.MustCompile(`\bTest[A-Z]\w*`)
	aTestDeclared = regexp.MustCompile(`(?m)^func (?:\([^)]*\) )?(Test\w*)\(`)
)

// notTests are names that read as a Go test's and are something else,
// each in the one file of rules that writes it.
var notTests = map[string][]string{
	// The plugin testkit's types (sdk/plugin-test).
	"plugins/AGENTS.md": {"TestPlugin", "TestHost"},
	// The test a spec writes into its fixture, differing at every run.
	"specs/AGENTS.md": {"TestOfThisRun"},
}

// testsNamedAbsent returns each Go test a text names that no test file
// declares, but those allowed that file. A name cut short (`TestReview…`)
// names no one test.
func testsNamedAbsent(file, text string, declared func(string) bool, allowed map[string][]string) []string {
	var out []string
	for _, at := range aTestNamed.FindAllStringIndex(text, -1) {
		name := text[at[0]:at[1]]
		if strings.HasPrefix(text[at[1]:], "…") || declared(name) || slices.Contains(allowed[file], name) || slices.Contains(out, name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// A rule says what holds it, and a test it names is there: every Go
// test an AGENTS.md names is declared by a test file of the repository.
// Not held: a Rust test's name, which reads as any other word.
func TestEveryTestTheRulesNameIsThere(t *testing.T) {
	root := repositoryRoot(t)
	declared := map[string]bool{}
	goTestFiles(t, root, func(_ string, source []byte) {
		for _, found := range aTestDeclared.FindAllSubmatch(source, -1) {
			declared[string(found[1])] = true
		}
	})
	files := repositoryFiles(t, root)
	for _, file := range files {
		if path.Base(file) != "AGENTS.md" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		for _, absent := range testsNamedAbsent(file, string(content), func(name string) bool { return declared[name] }, notTests) {
			t.Errorf("%s names the test %s, which no test file declares", file, absent)
		}
	}
	for _, stale := range allowedForNothing(root, files, notTests) {
		t.Errorf("notTests allows %s", stale)
	}
}

// The controls: a test that is declared is allowed, a function's or a
// suite's; one that is not is found, once; a name cut short names none;
// and what is allowed one file is allowed it alone.
func TestATestNamedButAbsentIsFound(t *testing.T) {
	const source = "package a\n\nfunc TestHeld(t *testing.T) {}\n\nfunc (s *suite) TestOfASuite() {}\n\nfunc helper() { TestCalled() }\n"
	declared := map[string]bool{}
	for _, found := range aTestDeclared.FindAllStringSubmatch(source, -1) {
		declared[found[1]] = true
	}
	const text = "Held by: `TestHeld`, `TestOfASuite`, `TestGone` (`TestReview…`), TestCalled and `TestGone`; the testkit's `TestHost`."
	allowed := map[string][]string{"plugins/AGENTS.md": {"TestHost"}}
	for file, want := range map[string]string{
		"plugins/AGENTS.md": "TestGone TestCalled",
		"AGENTS.md":         "TestGone TestCalled TestHost",
	} {
		if got := strings.Join(testsNamedAbsent(file, text, func(name string) bool { return declared[name] }, allowed), " "); got != want {
			t.Errorf("%s: found %q absent, want %q", file, got, want)
		}
	}
}

package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// fromSource is every file of this repository that has go build, run or
// install a program, and what it builds of whose: a Component that
// builds another's program from source depends on that Component in a
// way no go.mod says, so it says so here, and test support never does it
// (the gate builds the box before the tests it runs), since a test of
// the Component built would otherwise depend on itself unseen.
var fromSource = map[string]string{
	"stack/box/box.go":                      "the box image: frontseat (cli), and frontseatd, frontseat-runner, frontseat-server, frontseat-stack and frontseat-confine (server), for Linux; run by mise run build, by the stack (frontseat-stack up) and by the gate, never by a test of the server or of the command line",
	"sandbox/service_route_test.go":         "its own fixture (testdata/service-route), which links confine, of the server itself",
	"build/engine/exec/tools/tools_test.go": "frontseat-confine, which the daemon's tool staging starts an action by: of the server itself",
}

// goRuns says, of a file, each way it has the go command build: a call
// whose first two string literals are "go" and build, run or install,
// whatever it calls (exec.Command, exec.CommandContext, or a wrapper of
// the file's own that runs what it is given). What it asks of go
// otherwise (list, vet, version, work) builds nothing that runs.
func goRuns(source []byte) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "f.go", source, 0)
	if err != nil {
		return nil, err
	}
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, is := n.(*ast.CallExpr)
		if !is {
			return true
		}
		var words []string
		for _, argument := range call.Args {
			if literal, is := argument.(*ast.BasicLit); is && literal.Kind == token.STRING {
				word, _ := strconv.Unquote(literal.Value)
				words = append(words, word)
			}
		}
		if len(words) >= 2 && words[0] == "go" && slices.Contains([]string{"build", "run", "install"}, words[1]) {
			out = append(out, "go "+words[1])
		}
		return true
	})
	return out, nil
}

// unsaid says of each file that has go build a program and is not on the
// list, and of each entry of the list that is no such file, what is
// wrong. Specs are left out: nothing depends on them.
func unsaid(files map[string][]byte, listed map[string]string) []string {
	var out []string
	seen := map[string]bool{}
	for _, rel := range slices.Sorted(maps.Keys(files)) {
		if beneath(rel, "specs") || slices.Contains(strings.Split(rel, "/"), "testdata") {
			continue
		}
		runs, err := goRuns(files[rel])
		if err != nil {
			out = append(out, fmt.Sprintf("%s: %v", rel, err))
			continue
		}
		if len(runs) == 0 {
			continue
		}
		seen[rel] = true
		if listed[rel] == "" {
			out = append(out, fmt.Sprintf("%s runs %s, building a program from source, and does not say what of whose (fromSource)", rel, strings.Join(runs, ", ")))
		}
	}
	for _, rel := range slices.Sorted(maps.Keys(listed)) {
		if !seen[rel] {
			out = append(out, fmt.Sprintf("%s is on the list of what builds from source, and builds nothing", rel))
		}
	}
	return out
}

// A Component that builds another's program from source says so, and
// test support does not build the box.
func TestWhatBuildsAProgramFromSourceSaysSo(t *testing.T) {
	root := repositoryRoot(t)
	files := map[string][]byte{}
	for _, source := range goSources(t, root, root) {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(source.rel)))
		if err != nil {
			t.Fatal(err)
		}
		files[source.rel] = content
	}
	for _, wrong := range unsaid(files, fromSource) {
		t.Error(wrong)
	}
	for rel := range fromSource {
		if beneath(path.Dir(rel), "testsupport") {
			t.Errorf("%s is test support that builds a program from source: the gate builds it before the tests", rel)
		}
	}
}

// The check's control: a file that has go build a program unlisted is
// found, in each spelling; one that asks go for a list is not; a listed
// file that builds nothing is found.
func TestAProgramBuiltFromSourceUnsaidIsFound(t *testing.T) {
	held := map[string][]byte{
		"box/box.go":        []byte(`package box; import "os/exec"; var _ = exec.Command("go", "build", "./x")`),
		"a/list.go":         []byte(`package a; import "os/exec"; var _ = exec.Command("go", "list", "./...")`),
		"specs/x/x_test.go": []byte(`package x; import "os/exec"; var _ = exec.Command("go", "build", "./x")`),
	}
	listed := map[string]string{"box/box.go": "the box"}
	if wrong := unsaid(held, listed); len(wrong) != 0 {
		t.Errorf("refused what is held: %v", wrong)
	}
	for name, source := range map[string]string{
		"build":             `package a; import "os/exec"; var _ = exec.Command("go", "build", "./x")`,
		"run, in a test":    `package a; import "os/exec"; var _ = exec.CommandContext(nil, "go", "run", "./x")`,
		"install":           `package a; import x "os/exec"; var _ = x.Command("go", "install", "./x")`,
		"through a wrapper": `package a; func output(root, program string, arguments ...string) {}; func f(root string) { output(root, "go", "run", "./x") }`,
	} {
		files := maps.Clone(held)
		files["a/b.go"] = []byte(source)
		if wrong := unsaid(files, listed); len(wrong) != 1 {
			t.Errorf("%s: %v, want it found", name, wrong)
		}
	}
	if wrong := unsaid(map[string][]byte{"a/list.go": held["a/list.go"]}, listed); len(wrong) != 1 {
		t.Errorf("a listed file that builds nothing: %v", wrong)
	}
}

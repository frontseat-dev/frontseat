package architecture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// repositoryFiles are the files of the repository as git sees them: what
// it tracks, and what it would track, that is on disk. In a test action,
// which is given the repository's source and no .git, they are the files
// it was given that the repository's .gitignore files do not name.
func repositoryFiles(t *testing.T, root string) []string {
	t.Helper()
	if !checkout(root) {
		return untracked(t, root).files(t, root)
	}
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, file := range strings.Split(string(raw), "\x00") {
		if file == "" || slices.Contains(out, file) {
			continue
		}
		if info, err := os.Lstat(filepath.Join(root, file)); err == nil && info.Mode().IsRegular() {
			out = append(out, file)
		}
	}
	return out
}

// goTestFiles gives each Go test file of the repository, beside its
// source, leaving out fixtures, which are no test of this repository.
func goTestFiles(t *testing.T, root string, each func(file string, source []byte)) {
	t.Helper()
	for _, file := range repositoryFiles(t, root) {
		if !strings.HasSuffix(file, "_test.go") || slices.Contains(strings.Split(file, "/"), "testdata") {
			continue
		}
		source, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		each(file, source)
	}
}

// fixture reports whether a file is a test's own: what it names is the
// fixture's, and not a path of this repository.
func fixture(file string) bool {
	return strings.HasSuffix(file, "_test.go") || slices.Contains(strings.Split(file, "/"), "testdata")
}

// aPath is a path of the repository as text writes it: one of its
// top-level directories and what is beneath it, standing alone, not part
// of another path, an address or a list in braces.
var aPath = regexp.MustCompile(`(?:^|[^\w./@{,-])(?:\./)?((?:api|box|cli|server|internal|local|specs|plugins|sdk|docs)/[\w./@-]*)`)

// namedAbsent returns each path of the repository a text names that is
// not there, but those the allowed name for that file. What stands for a
// pattern (`*`, `{`, `<`, `$`, `%` after it) names no one path; `/...`
// after a directory is go's, and names the directory.
func namedAbsent(file string, content []byte, there func(string) bool, allowed map[string][]string) []string {
	if bytes.IndexByte(content, 0) >= 0 {
		return nil
	}
	var out []string
	text := string(content)
	for _, match := range aPath.FindAllStringSubmatchIndex(text, -1) {
		named := text[match[2]:match[3]]
		if next := text[match[3]:min(match[3]+1, len(text))]; next != "" && strings.ContainsAny(next, "*{<$%") {
			continue
		}
		named = strings.TrimSuffix(strings.TrimRight(named, "."), "/...")
		named = strings.TrimRight(named, "/.")
		if strings.Contains(named, "..") || there(named) || slices.Contains(allowed[file], named) || slices.Contains(out, named) {
			continue
		}
		out = append(out, named)
	}
	return out
}

// namedElsewhere are names that read as a path of this repository and
// are not, each in the one file that writes it: a path of another
// repository, or of a directory the file stands for, or a fixture a
// program writes.
var namedElsewhere = map[string][]string{
	// git's source, and the host program the hardening was taken from.
	"common/git/git.go": {"internal/hostgit"},
	// The fixture repository the forge writes for a test.
	"testsupport/testforge/generated.go": {"api/buf.gen.lib.yaml", "api/buf.gen.yaml", "api/gen/go/go.mod", "api/gen/go/go.sum", "api/gen/go/keep.go", "api/hello/v1/hello.proto"},
	"testsupport/testforge/testforge.go": {"docs/guide.md"},
	// An output of a Buf workspace, as the Buf plugin's tests spell one.
	"plugins/buf/src/manifest.rs": {"api/gen/go"},
	// Words that read as paths: local, git and linked.
	"plugins/npm/README.md": {"local/git/linked"},
	// Packages of a fixture module, and of the go command's source.
	"plugins/go/src/composition_tests.rs": {"internal/ledger", "internal/other"},
	"plugins/go/src/programs.rs":          {"internal/load/pkg.go", "internal/vendor/vendor_test.go"},
	// GitHub's command line, as mise's lock names where it comes from.
	"mise.lock": {"cli/cli"},
	// wazero's package.
	"build/engine/pluginhost/host.go": {"internal/version.GetWazeroVersion"},
}

// N3: every path of this repository that a file names is there: a doc, a
// comment, a catalog file, a task, a message a person reads. What a test
// names is its fixture's.
func TestEveryPathAFileNamesIsThere(t *testing.T) {
	root := repositoryRoot(t)
	files := repositoryFiles(t, root)
	there := held(files)
	for _, file := range files {
		if fixture(file) {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		for _, absent := range namedAbsent(file, content, there, namedElsewhere) {
			t.Errorf("%s names %s, which is not there", file, absent)
		}
	}
	for file, names := range namedElsewhere {
		if !slices.Contains(files, file) {
			t.Errorf("namedElsewhere allows names in %s, which is not there", file)
		}
		content, _ := os.ReadFile(filepath.Join(root, file))
		for _, name := range names {
			if !bytes.Contains(content, []byte(name)) {
				t.Errorf("namedElsewhere allows %s in %s, which no longer names it", name, file)
			}
		}
	}
}

// held returns whether a path is a file of the repository, or a directory
// of its files, as git spells them: what git ignores, a build left, or a
// name another case of which the disk would find, is not there.
func held(files []string) func(string) bool {
	paths := map[string]bool{}
	for _, file := range files {
		for at := file; at != "." && at != "/"; at = path.Dir(at) {
			paths[at] = true
		}
	}
	return func(named string) bool { return paths[named] }
}

// What the repository holds is what git holds of it: a file a build
// left, or git ignores, is not; nor is a name in another case, which a
// disk that folds case would find; a directory of its files is.
func TestAPathIsThereWhereGitHoldsIt(t *testing.T) {
	there := held([]string{"build/daemon/build/build.go", "docs/index.md"})
	for named, want := range map[string]bool{
		"build/daemon/build/build.go": true,
		"build/daemon/build":          true,
		"build":                       true,
		"docs/index.md":               true,
		"Build/daemon/build/build.go": false,
		"docs/Index.md":               false,
		"dist/bin/frontseat":          false,
		"build/daemo":                 false,
	} {
		if got := there(named); got != want {
			t.Errorf("%s is there: %v, want %v", named, got, want)
		}
	}
}

func TestAPathNamedButAbsentIsFound(t *testing.T) {
	there := func(named string) bool { return named == "build/daemon/build" || named == "docs/index.md" }
	cases := map[string]string{
		"see docs/rulings/tests.md.":                        "docs/rulings/tests.md",
		"`build/daemon/build/` and `server/gone`":           "server/gone",
		"go test ./build/daemon/build/... ./cli/...":        "cli",
		"(docs/local-development.md says how)":              "docs/local-development.md",
		"docs/index.md, build/daemon/build, plugins/<name>": "",
		"github.com/frontseat-dev/frontseat/server/gone":    "",
		"genproto/googleapis/{api/annotations,rpc/x}":       "",
		"plugins/*/Cargo.toml and /var/lib/x-local/y":       "",
		"frontseat-local/openshell, cmd/../server/../etc":   "",
	}
	for text, want := range cases {
		if got := strings.Join(namedAbsent("f", []byte(text), there, nil), " "); got != want {
			t.Errorf("%q: found %q absent, want %q", text, got, want)
		}
	}
	// The control: what is allowed for a file is allowed for it alone.
	allowed := map[string][]string{"f": {"server/gone"}}
	if got := namedAbsent("f", []byte("server/gone"), there, allowed); len(got) != 0 {
		t.Errorf("an allowed name was found absent: %q", got)
	}
	if got := namedAbsent("g", []byte("server/gone"), there, allowed); len(got) != 1 {
		t.Errorf("a name allowed in another file was not found absent in this one: %q", got)
	}
}

// pluginNames are the plugins of the Cargo workspace, by the name of
// their directory beneath plugins/: cargo's word.
func pluginNames(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, p := range cargoPackages(t, root) {
		rel, err := filepath.Rel(root, filepath.Dir(p.ManifestPath))
		if err != nil {
			t.Fatal(err)
		}
		if dir := filepath.ToSlash(rel); path.Dir(dir) == "plugins" {
			out = append(out, path.Base(dir))
		}
	}
	slices.Sort(out)
	return out
}

// listed says where the tasks write out a list of the repository's own
// parts: a Go package pattern over many (`/...`), a glob beneath a module
// that is not a top-level directory, more than one package to one go
// command (a glob is none), or a plugin by any of its names, in a command
// or in what a task reads or makes.
func listed(commands, paths, modules, plugins []string) []string {
	var out []string
	look := func(field string) {
		if strings.HasSuffix(field, "/...") {
			out = append(out, field+" lists packages")
		}
		for _, dir := range modules {
			if strings.Contains(dir, "/") && strings.HasPrefix(strings.TrimPrefix(field, "./"), dir+"/") && strings.Contains(field, "*") {
				out = append(out, field+" lists the files of the module "+dir)
			}
		}
		for _, plugin := range plugins {
			for _, name := range []string{"plugins/" + plugin, "frontseat-plugin-" + plugin, "frontseat_plugin_" + strings.ReplaceAll(plugin, "-", "_"), plugin + ".wasm"} {
				if strings.Contains(field, name) && !strings.Contains(field, name+"-") && !strings.Contains(field, name+"_") {
					out = append(out, field+" names the plugin "+plugin)
				}
			}
		}
	}
	for _, command := range commands {
		fields := strings.FieldsFunc(command, func(r rune) bool { return strings.ContainsRune(" \t\n\"',[]=", r) })
		packages := 0
		for i, field := range fields {
			if field == "-args" {
				break
			}
			look(field)
			if strings.HasPrefix(field, "./") && !strings.Contains(field, "*") && !(i > 0 && fields[i-1] == "run") {
				packages++
			}
		}
		if packages > 1 {
			out = append(out, fmt.Sprintf("%q names %d packages", command, packages))
		}
	}
	for _, each := range paths {
		look(each)
	}
	return out
}

// N4: mise.toml writes out no list of the repository's modules, packages
// or plugins: the gates read go.work and the source
// (specs/cmd/gate), the plugins are the Cargo workspace's
// (prepare). A task names a program it runs and the one package a test
// run is of. Every task of this repository's mise.toml is read as mise
// reads it: each command it runs, and what it reads and makes.
func TestMiseWritesOutNoModuleOrPlugin(t *testing.T) {
	root := repositoryRoot(t)
	cmd := exec.Command("mise", "tasks", "ls", "--json")
	cmd.Dir = root
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var tasks []struct {
		Name, Source     string
		Run              []json.RawMessage
		Sources, Outputs []string
	}
	if err := json.Unmarshal(raw, &tasks); err != nil {
		t.Fatal(err)
	}
	var commands, paths []string
	read := 0
	for _, task := range tasks {
		if task.Source != filepath.Join(root, "mise.toml") {
			continue
		}
		read++
		for _, each := range task.Run {
			// A step that runs another task ({task = "build"}) is no command.
			var command string
			if json.Unmarshal(each, &command) == nil {
				commands = append(commands, command)
			}
		}
		paths = append(append(paths, task.Sources...), task.Outputs...)
	}
	if read == 0 || len(commands) == 0 {
		t.Fatalf("mise read no task of %s", filepath.Join(root, "mise.toml"))
	}
	// And the file as it is written, whatever mise makes of it: its
	// environment, its variables and anything else, comments aside.
	content, err := os.ReadFile(filepath.Join(root, "mise.toml"))
	if err != nil {
		t.Fatal(err)
	}
	paths = append(paths, written(string(content))...)
	for _, problem := range listed(commands, paths, workspaceModules(t, root), pluginNames(t, root)) {
		t.Errorf("mise.toml: %s", problem)
	}
	for _, problem := range taskFiles(repositoryFiles(t, root), string(content)) {
		t.Errorf("%s", problem)
	}
}

// written returns the words of a TOML file as it is written, but its
// comments: what lists the repository's parts in a value mise reads
// otherwise than as a task (an environment, a variable) is found there.
func written(content string) []string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		out = append(out, strings.FieldsFunc(line, func(r rune) bool { return strings.ContainsRune(" \t\"',[]={}", r) })...)
	}
	return out
}

// taskFiles refuses tasks kept elsewhere than mise.toml, which this check
// does not read: a directory of file tasks mise finds by itself, or files
// mise.toml includes. Every task is in mise.toml.
func taskFiles(files []string, content string) []string {
	var out []string
	for _, file := range files {
		for _, tasks := range []string{"mise-tasks/", ".mise-tasks/", "mise/tasks/", ".mise/tasks/", ".config/mise/tasks/"} {
			if strings.HasPrefix(file, tasks) || strings.Contains(file, "/"+tasks) {
				out = append(out, file+" is a task kept outside mise.toml")
			}
		}
	}
	for _, word := range written(content) {
		if word == "task_config" || word == "[task_config]" || word == "includes" {
			out = append(out, "mise.toml includes tasks from elsewhere ("+word+")")
		}
	}
	return out
}

func TestATaskKeptOutsideMiseOrAListWrittenAnywhereInItIsFound(t *testing.T) {
	if got := taskFiles([]string{"mise.toml", "docs/tasks/x.md", "local/stack"}, "[tasks.a]\nrun = \"x\"\n# includes = [\"elsewhere\"]\n"); len(got) != 0 {
		t.Errorf("refused %q", got)
	}
	for _, each := range []struct {
		files   []string
		content string
	}{
		{[]string{"mise-tasks/build"}, ""},
		{[]string{".mise/tasks/check"}, ""},
		{[]string{"local/.mise-tasks/up"}, ""},
		{nil, "[task_config]\nincludes = [\"tasks\"]\n"},
	} {
		if got := taskFiles(each.files, each.content); len(got) == 0 {
			t.Errorf("%q %q: not refused", each.files, each.content)
		}
	}
	modules, plugins := []string{"core/telemetry"}, []string{"go"}
	for _, content := range []string{
		"[env]\nPLUGINS = \"plugins/go\"\n",
		"[vars]\npackages = \"./core/telemetry/...\"\n",
		"[env]\nX = { value = \"frontseat-plugin-go\" }\n",
	} {
		if got := listed(nil, written(content), modules, plugins); len(got) == 0 {
			t.Errorf("%q: not refused", content)
		}
	}
	if got := listed(nil, written("# plugins/go and ./core/telemetry/...\n[env]\nX = \"plugins/**\"\n"), modules, plugins); len(got) != 0 {
		t.Errorf("a comment or a glob of every plugin was refused: %q", got)
	}
}

func TestAListOfModulesOrPluginsInMiseIsFound(t *testing.T) {
	modules := []string{"cli", "core/telemetry", "specs"}
	plugins := []string{"go", "cargo"}
	allowed := [][2][]string{
		{{"go run ./specs/cmd/gate test -except specs/build -- -count=1 -race"}},
		{{"go run ./specs/cmd/gate go-test -count=1 -run TestCompare ./specs/compare/ -args -compare ./x/..."}},
		{nil, {"cli/**", "internal/**", "plugins/**", "go.work", "dist/plugins"}},
		{{"go run ./specs/cmd/prepare-modules -dist dist/plugins"}},
		{{"cargo build -p frontseat-plugin"}},
		{{"jscpd --ignore './api/gen/**,./target/**' ."}},
	}
	for _, each := range allowed {
		if got := listed(each[0], each[1], modules, plugins); len(got) != 0 {
			t.Errorf("%q: refused %q", each, got)
		}
	}
	refused := [][2][]string{
		{{"go vet ./cli/... ./specs/..."}},
		{nil, {"core/telemetry/**/*.go"}},
		{{"go test -count=1 ./sandbox/ ./build/daemon/build/"}},
		{{"cargo build -p frontseat-plugin-go"}},
		{{"cp target/wasm32-wasip1/release/frontseat_plugin_cargo.wasm dist/plugins/cargo.wasm"}},
		{nil, {"dist/plugins/go.wasm"}},
		{{"cp -r plugins/go x"}},
		// A command of several lines is one command, every line of it read.
		{{"mkdir -p dist\ngo test ./cli/..."}},
	}
	for _, each := range refused {
		if got := listed(each[0], each[1], modules, plugins); len(got) == 0 {
			t.Errorf("%q: not refused", each)
		}
	}
}

// reservations returns the directories that hold nothing but what
// reserves them: a Go package whose files that are no test declare
// nothing (a doc.go, or a file that is its package clause alone), or a
// directory of a README or a .gitkeep alone with nothing beneath it. A
// package of tests alone is a package of tests. read gives a file's
// content.
func reservations(files []string, read func(string) []byte) []string {
	held := map[string][]string{}
	beneath := map[string]bool{}
	for _, file := range files {
		if fixture(file) && !strings.HasSuffix(file, "_test.go") {
			continue
		}
		dir := path.Dir(file)
		held[dir] = append(held[dir], path.Base(file))
		for parent := path.Dir(dir); ; parent = path.Dir(parent) {
			beneath[parent] = true
			if parent == "." || parent == "/" {
				break
			}
		}
	}
	var out []string
	for dir, names := range held {
		code, empty, tests, marks, other := false, false, false, false, false
		for _, name := range names {
			switch {
			case strings.HasSuffix(name, "_test.go"):
				tests = true
			case strings.HasSuffix(name, ".go"):
				if declares(read(path.Join(dir, name))) {
					code = true
				} else {
					empty = true
				}
			case strings.HasPrefix(strings.ToLower(name), "readme") || name == ".gitkeep":
				marks = true
			default:
				other = true
			}
		}
		if empty && !code && !tests || marks && !code && !empty && !tests && !other && !beneath[dir] {
			out = append(out, dir)
		}
	}
	slices.Sort(out)
	return out
}

// declares reports whether a Go file declares anything beside its
// package clause and imports. One that cannot be read declares, so that
// what cannot be parsed is the compiler's to refuse.
func declares(source []byte) bool {
	file, err := parser.ParseFile(token.NewFileSet(), "", source, parser.SkipObjectResolution)
	if err != nil {
		return true
	}
	for _, declared := range file.Decls {
		if general, ok := declared.(*ast.GenDecl); ok && general.Tok == token.IMPORT {
			continue
		}
		return true
	}
	return false
}

// N7: nothing is reserved. A package that declares nothing, or a
// directory that holds only a README or a .gitkeep, is a promise of code
// that is not there.
func TestNothingIsReserved(t *testing.T) {
	root := repositoryRoot(t)
	read := func(file string) []byte {
		content, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		return content
	}
	for _, dir := range reservations(repositoryFiles(t, root), read) {
		t.Errorf("%s holds nothing but what reserves it", dir)
	}
}

func TestAReservationIsFound(t *testing.T) {
	contents := map[string]string{
		"a/a.go":   "package a\n\nfunc A() {}\n",
		"k/k.go":   "// Package k is k.\npackage k\n\nimport _ \"embed\"\n",
		"l/doc.go": "package l\n",
		"l/l.go":   "package l\n\nvar L = 1\n",
	}
	read := func(file string) []byte {
		if content, ok := contents[file]; ok {
			return []byte(content)
		}
		if path.Base(file) == "doc.go" {
			return []byte("// Package x is x.\npackage x\n")
		}
		return []byte("package x\n\nvar X = 1\n")
	}
	files := []string{
		"a/doc.go", "a/a.go", // a package
		"b/doc.go", "b/b_test.go", // a package of tests
		"c/README.md", "c/d/d.go", // a README over what is beneath it
		"e/README.md", "e/schema.json", // a README beside what it describes
		"f/doc.go",             // reserved
		"g/README.md",          // reserved
		"h/doc.go", "h/i/i.go", // an umbrella doc over packages: reserved
		"j/testdata/README.md", // a fixture's
		"k/k.go",               // a package clause and an import alone: reserved
		"l/doc.go", "l/l.go",   // a doc and a declaration
		"m/.gitkeep",                // reserved
		"n/.gitkeep", "n/README.md", // reserved
	}
	if got, want := strings.Join(reservations(files, read), " "), "f g h k m n"; got != want {
		t.Errorf("found %q reserved, want %q", got, want)
	}
}

// anOccasion is a word that names how a test came to be, not what it holds.
var anOccasion = []string{"review", "poc", "proof"}

// occasioned reports whether a test file is named for an occasion.
func occasioned(file string) bool {
	name := path.Base(file)
	if !strings.HasSuffix(name, "_test.go") && !strings.HasSuffix(name, "_tests.rs") {
		return false
	}
	for _, word := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return r == '_' || r == '-' || r == '.' }) {
		if slices.Contains(anOccasion, word) {
			return true
		}
	}
	return false
}

// A test file is named for its subject, never for the occasion it was
// written on (a review, a proof of concept, a proof).
func TestATestFileIsNamedForItsSubject(t *testing.T) {
	files := repositoryFiles(t, repositoryRoot(t))
	for _, file := range files {
		if occasioned(file) {
			t.Errorf("%s is named for an occasion (%v): name it for what it holds", file, anOccasion)
		}
	}
}

func TestATestFileNamedForAnOccasionIsFound(t *testing.T) {
	for file, want := range map[string]bool{
		"a/inputs_review_test.go": true,
		"a/review_parts_test.go":  true,
		"a/route_poc_test.go":     true,
		"a/proof_test.go":         true,
		"a/x_review_tests.rs":     true,
		"a/reviewer_test.go":      false,
		"a/proofread_test.go":     false,
		"a/review.go":             false,
		"a/inputs_test.go":        false,
		"a/review/inputs_test.go": false,
	} {
		if got := occasioned(file); got != want {
			t.Errorf("%s: named for an occasion %v, want %v", file, got, want)
		}
	}
}

// What this repository must say twice, because a tool reads it where it
// is and nowhere else, says the same: Rust and the wasm standard library
// it links are one release, every go directive is the Go mise pins, and
// the TechDocs image is one image wherever a task runs it.
func TestWhatIsSaidTwiceAgrees(t *testing.T) {
	root := repositoryRoot(t)
	cmd := exec.Command("mise", "ls", "--current", "--json")
	cmd.Dir = root
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var tools map[string][]struct {
		Requested string `json:"requested_version"`
	}
	if err := json.Unmarshal(raw, &tools); err != nil {
		t.Fatal(err)
	}
	pinned := func(tool string) string {
		if len(tools[tool]) != 1 || tools[tool][0].Requested == "" {
			t.Fatalf("mise pins %s %v times, want once", tool, tools[tool])
		}
		return tools[tool][0].Requested
	}
	if rust, std := pinned("http:rust"), pinned("http:rust-std-wasm32-wasip1"); rust != std {
		t.Errorf("mise pins Rust %s and its wasm standard library %s: they are one release", rust, std)
	}
	golang := pinned("go")
	var work struct{ Go string }
	goJSON(t, root, &work, "work", "edit", "-json")
	if work.Go != golang {
		t.Errorf("go.work says go %s, and mise pins %s", work.Go, golang)
	}
	for _, dir := range workspaceModules(t, root) {
		var mod struct{ Go string }
		goJSON(t, filepath.Join(root, dir), &mod, "mod", "edit", "-json")
		if mod.Go != golang {
			t.Errorf("%s/go.mod says go %s, and mise pins %s", dir, mod.Go, golang)
		}
	}
	content, err := os.ReadFile(filepath.Join(root, "mise.toml"))
	if err != nil {
		t.Fatal(err)
	}
	images := regexp.MustCompile(`spotify/techdocs@sha256:[0-9a-f]{64}`).FindAllString(string(content), -1)
	if len(slices.Compact(slices.Sorted(slices.Values(images)))) > 1 {
		t.Errorf("mise.toml runs TechDocs as %d images: %v", len(slices.Compact(slices.Sorted(slices.Values(images)))), images)
	}
}

// reachesTheRuntime reports whether a test file reaches the container
// runtime itself: it runs `docker` (os/exec's Command or CommandContext,
// under any name, with "docker" as the program) or serves sandboxes over
// it (the sandbox service's Runner).
func reachesTheRuntime(file *ast.File) bool {
	execs, sandboxes := map[string]bool{}, map[string]bool{}
	for _, spec := range file.Imports {
		imported, _ := strconv.Unquote(spec.Path.Value)
		name := path.Base(imported)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		switch imported {
		case "os/exec":
			execs[name] = true
		case module + "/sandbox":
			sandboxes[name] = true
		}
	}
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.SelectorExpr:
			if x, ok := node.X.(*ast.Ident); ok && sandboxes[x.Name] && node.Sel.Name == "Runner" {
				found = true
			}
		case *ast.CallExpr:
			selector, ok := node.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := selector.X.(*ast.Ident)
			if !ok || !execs[x.Name] {
				return true
			}
			at, known := map[string]int{"Command": 0, "CommandContext": 1}[selector.Sel.Name]
			if !known || len(node.Args) <= at {
				return true
			}
			if program, ok := node.Args[at].(*ast.BasicLit); ok && program.Value == `"docker"` {
				found = true
			}
		}
		return !found
	})
	return found
}

// testing reports whether a function is given a test (a parameter of
// testing's T or TB), and so can say what it needs itself.
func given(function *ast.FuncDecl, testingAs map[string]bool) bool {
	for _, field := range function.Type.Params.List {
		kind := field.Type
		if star, ok := kind.(*ast.StarExpr); ok {
			kind = star.X
		}
		if selector, ok := kind.(*ast.SelectorExpr); ok {
			if x, ok := selector.X.(*ast.Ident); ok && testingAs[x.Name] && (selector.Sel.Name == "T" || selector.Sel.Name == "TB") {
				return true
			}
		}
	}
	return false
}

// unstacked returns each function of the test files given by path that
// reaches the container runtime, itself or through a function of its
// package that is given no test, and does not say testrequired.Stack, itself
// or through a function of its package it calls plainly. A function given
// no test cannot say it, so what it reaches is its callers' to say; a
// method is called by its name. It reads names, not types or the order of
// what runs, so it can miss a way to the runtime: what holds the gate is
// that a gate with no stack fails where the runtime is asked at all
// (specs/cmd/gate); this check names the test to change. It returns too
// each function that says testrequired.Stack, itself or so, and skips
// itself: what it needs of the stack is said missing through the test
// support (testrequired.Missing, testbox.Runtime), which fails the test
// where the gate runs against the stack, and a skip of its own would
// end that gate green with the test not run.
func unstacked(sources map[string][]byte) ([]string, error) {
	type function struct {
		file  string
		given bool
	}
	packages := map[string]map[string]function{}
	reaches, says, skips := map[string]map[string]bool{}, map[string]map[string]bool{}, map[string]map[string]bool{}
	calls := map[string]map[string][]string{}
	for name, source := range sources {
		file, err := parser.ParseFile(token.NewFileSet(), name, source, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		dir := path.Dir(name)
		if packages[dir] == nil {
			packages[dir], reaches[dir], says[dir], skips[dir], calls[dir] = map[string]function{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string][]string{}
		}
		requires, testingAs := map[string]bool{}, map[string]bool{}
		for _, spec := range file.Imports {
			imported, _ := strconv.Unquote(spec.Path.Value)
			as := path.Base(imported)
			if spec.Name != nil {
				as = spec.Name.Name
			}
			requires[as] = requires[as] || imported == module+"/testsupport/testrequired"
			testingAs[as] = testingAs[as] || imported == "testing"
		}
		for _, declared := range file.Decls {
			decl, ok := declared.(*ast.FuncDecl)
			if !ok || decl.Body == nil {
				continue
			}
			named := decl.Name.Name
			if decl.Recv != nil && len(decl.Recv.List) == 1 {
				named = "(" + types.ExprString(decl.Recv.List[0].Type) + ")." + named
			}
			packages[dir][named] = function{file: name, given: given(decl, testingAs) || packages[dir][named].given}
			if reachesTheRuntime(&ast.File{Name: file.Name, Imports: file.Imports, Decls: []ast.Decl{decl}}) {
				reaches[dir][named] = true
			}
			ast.Inspect(decl.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.SelectorExpr:
					if x, ok := fun.X.(*ast.Ident); ok && requires[x.Name] && fun.Sel.Name == "Stack" {
						says[dir][named] = true
					} else {
						skips[dir][named] = skips[dir][named] || slices.Contains([]string{"Skip", "Skipf", "SkipNow"}, fun.Sel.Name)
						calls[dir][named] = append(calls[dir][named], "."+fun.Sel.Name)
					}
				case *ast.Ident:
					calls[dir][named] = append(calls[dir][named], fun.Name)
				}
				return true
			})
		}
	}
	var out []string
	for dir, functions := range packages {
		// What a call names: a function by its name, and through a
		// selector a function or a method of that name.
		named := func(callee string, holds func(string) bool) bool {
			for key := range functions {
				method := strings.Contains(key, ").")
				if (key == strings.TrimPrefix(callee, ".") && (!method || strings.HasPrefix(callee, "."))) ||
					(strings.HasPrefix(callee, ".") && strings.HasSuffix(key, ")"+callee)) {
					if holds(key) {
						return true
					}
				}
			}
			return false
		}
		for changed := true; changed; {
			changed = false
			for caller, callees := range calls[dir] {
				// What a function says spreads to its callers by a plain call
				// alone: a call through a selector may be any type's method of
				// that name (cmd.Run), and is not taken to say anything.
				if !says[dir][caller] && slices.ContainsFunc(callees, func(callee string) bool {
					return !strings.HasPrefix(callee, ".") && named(callee, func(key string) bool { return says[dir][key] })
				}) {
					says[dir][caller], changed = true, true
				}
				if !reaches[dir][caller] && slices.ContainsFunc(callees, func(callee string) bool {
					return named(callee, func(key string) bool { return reaches[dir][key] && !functions[key].given })
				}) {
					reaches[dir][caller], changed = true, true
				}
			}
		}
		for named, each := range functions {
			if each.given && reaches[dir][named] && !says[dir][named] {
				out = append(out, each.file+": "+named+" reaches the container runtime and does not say testrequired.Stack")
			}
			if says[dir][named] && skips[dir][named] {
				out = append(out, each.file+": "+named+" says testrequired.Stack and skips itself")
			}
		}
	}
	slices.Sort(out)
	return out, nil
}

// A test that reaches the container runtime says it needs the stack
// before it does, so that the gate that runs with none (`mise run check`)
// skips it: a test that reached it otherwise would start containers in
// that gate on a machine that runs them, and fail on one that does not.
func TestATestThatReachesTheContainerRuntimeSaysItNeedsTheStack(t *testing.T) {
	root := repositoryRoot(t)
	sources := map[string][]byte{}
	for _, file := range repositoryFiles(t, root) {
		if !strings.HasSuffix(file, "_test.go") || slices.Contains(strings.Split(file, "/"), "testdata") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		sources[file] = content
	}
	found, err := unstacked(sources)
	if err != nil {
		t.Fatal(err)
	}
	for _, each := range found {
		t.Error(each)
	}
}

func TestATestThatReachesTheContainerRuntimeUnsaidIsFound(t *testing.T) {
	const head = "package p\n\nimport (\n\t\"context\"\n\trun \"os/exec\"\n\t\"testing\"\n\n\ttestrequired \"" + module + "/testsupport/testrequired\"\n\tbox \"" + module + "/sandbox\"\n)\n\n"
	sources := map[string][]byte{"a/a_test.go": []byte(head + `
func TestSaid(t *testing.T) { testrequired.Stack(t); run.Command("docker", "ps").Run() }
func helper(t *testing.T) { testrequired.Stack(t) }
func TestThroughAHelper(t *testing.T) { helper(t); run.CommandContext(context.Background(), "docker").Run() }
func TestAnotherProgram(t *testing.T) { run.Command("git", "docker").Run() }
func TestUnsaid(t *testing.T) { run.Command("docker", "info").Run() }
func TestUnsaidWithAContext(t *testing.T) { run.CommandContext(context.Background(), "docker", "ps").Run() }
func TestARunner(t *testing.T) { _ = box.Runner{} }
func TestStackOfAnotherPackage(t *testing.T) { other.Stack(t); run.Command("docker").Run() }
func logs(id string) { run.Command("docker", "logs", id).Run() }
func TestThroughAnUnsaidHelper(t *testing.T) { logs("x") }
func TestThroughAHelperItSays(t *testing.T) { testrequired.Stack(t); logs("x") }
type probe struct{ t *testing.T }
func (p *probe) create() { run.Command("docker", "create").Run() }
func newProbe(t *testing.T) *probe { testrequired.Stack(t); return &probe{t} }
func TestAMethodOfWhatWasSaid(t *testing.T) { newProbe(t).create() }
func TestAMethodUnsaid(t *testing.T) { (&probe{t}).create() }
func (p *probe) Run() { testrequired.Stack(p.t) }
func TestARunOfAnotherType(t *testing.T) { run.Command("docker").Run() }
var _ = testrequired.Stack
`)}
	found, err := unstacked(sources)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, each := range []string{"TestAMethodUnsaid", "TestARunOfAnotherType", "TestARunner", "TestStackOfAnotherPackage", "TestThroughAnUnsaidHelper", "TestUnsaid", "TestUnsaidWithAContext"} {
		want = append(want, "a/a_test.go: "+each+" reaches the container runtime and does not say testrequired.Stack")
	}
	if !slices.Equal(found, want) {
		t.Errorf("found %q, want %q", found, want)
	}
}

// A test that says it needs the stack, itself or through a helper of its
// package, and then skips itself is found, and so is the helper that
// does; one that says what is missing through the test support, and one
// that skips and says nothing of the stack, are not.
func TestATestThatSkipsItselfAfterSayingItNeedsTheStackIsFound(t *testing.T) {
	const head = "package p\n\nimport (\n\t\"os/exec\"\n\t\"testing\"\n\n\t\"" + module + "/local/testsupport/testbox\"\n\t\"" + module + "/testsupport/testrequired\"\n)\n\n"
	sources := map[string][]byte{"a/a_test.go": []byte(head + `
func TestSkipsAfterIt(t *testing.T) { testrequired.Stack(t); if _, err := exec.LookPath("docker"); err != nil { t.Skip("no runtime") } }
func TestSkipsNowAfterIt(t *testing.T) { testrequired.Stack(t); t.SkipNow() }
func helper(t *testing.T) { testrequired.Stack(t) }
func TestSkipsfAfterAHelper(t *testing.T) { helper(t); t.Skipf("no %s", "engine") }
func newProbe(t *testing.T) { testrequired.Stack(t); t.Skip("no runtime") }
func TestSaysItMissing(t *testing.T) { testrequired.Stack(t); testbox.Runtime(t); testrequired.Missing(t, "no engine") }
func TestSkipsAndNeedsNoStack(t *testing.T) { if _, err := exec.LookPath("ssh"); err != nil { t.Skip("no ssh") } }
`)}
	found, err := unstacked(sources)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, each := range []string{"TestSkipsAfterIt", "TestSkipsNowAfterIt", "TestSkipsfAfterAHelper", "newProbe"} {
		want = append(want, "a/a_test.go: "+each+" says testrequired.Stack and skips itself")
	}
	if !slices.Equal(found, want) {
		t.Errorf("found %q, want %q", found, want)
	}
}

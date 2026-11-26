package architecture

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	gomodule "golang.org/x/mod/module" // the package declares module
	"golang.org/x/mod/semver"
)

// Every module of the workspace requires each module it requires at the
// version the workspace selects. Go holds none of it: under go.work a
// module that requires an older version builds with the newer one the
// workspace selects, so `mise run check` passes. Frontseat's own build
// does not: it builds each module as the one module of its own
// workspace, with the modules of this repository it requires, from what
// its go.sum locks alone and offline (the Go plugin), and the version
// that module asked for is one nothing fetched.
//
// What the workspace selects is read from `go mod graph`, run in it: the
// graph the go command chooses versions over, by its own reading of
// every go.mod and of go.work, each module selected at the highest
// version in it (minimal version selection). It reads only go.mod files,
// as choosing versions does, and fails where one is missing. `go list -m
// all` says the selected versions itself, but asks the module cache of
// more than the graph (each module's .info) and fails offline where only
// go.mod files were fetched; with -e it says what it could read of the
// graph and succeeds, at versions that were not selected.
func TestEveryModuleRequiresWhatTheWorkspaceSelects(t *testing.T) {
	root := repositoryRoot(t)
	for _, found := range unselected(moduleGraph(t, root)) {
		t.Error(found)
	}
}

func TestARequirementTheWorkspaceDoesNotSelectIsFound(t *testing.T) {
	if found := unselected(moduleGraph(t, requirementsFixture(t, "agree"))); len(found) != 0 {
		t.Errorf("modules that require what the workspace selects were refused: %v", found)
	}
	want := []string{"example.com/b requires example.com/dep v1.0.0, and the workspace selects v1.1.0"}
	if found := unselected(moduleGraph(t, requirementsFixture(t, "older"))); !slices.Equal(found, want) {
		t.Errorf("found %q, want %q", found, want)
	}
}

// Each module's go.sum locks what its go.mod requires: the go.mod of
// every version it requires, which choosing versions reads, and the
// archive of every one, direct or `// indirect`: since Go 1.17 prunes the
// graph, a go.mod requires every module that provides a package its
// build imports, and marks those it reaches through another indirect. Frontseat's own build fetches what a module's go.sum locks
// and nothing else. And where two go.sums lock one module at one
// version, they lock it alike: it is fetched once, for whichever locks
// it, and a hash edited by hand in one of them is seen. The go.sums are
// not copies of each other: each locks what its module needs, and may
// lock more.
func TestEachGoSumLocksWhatItsGoModRequires(t *testing.T) {
	root := repositoryRoot(t)
	for _, found := range unlocked(readModuleFiles(t, root)) {
		t.Error(found)
	}
}

func TestAGoSumThatLocksTooLittleOrUnlikeAnotherIsFound(t *testing.T) {
	if found := unlocked(readModuleFiles(t, requirementsFixture(t, "agree"))); len(found) != 0 {
		t.Errorf("go.sums that lock what their go.mod requires, alike, were refused: %v", found)
	}
	for fixture, want := range map[string][]string{
		"unlocked":           {"b/go.sum does not lock the go.mod of example.com/dep v1.1.0, which b/go.mod requires"},
		"noarchive":          {"b/go.sum does not lock example.com/dep v1.1.0, which b/go.mod requires"},
		"noarchive-indirect": {"b/go.sum does not lock example.com/dep v1.1.0, which b/go.mod requires // indirect"},
		"unlike":             {"a/go.sum and b/go.sum lock the go.mod of example.com/dep v1.1.0 unlike"},
	} {
		if found := unlocked(readModuleFiles(t, requirementsFixture(t, fixture))); !slices.Equal(found, want) {
			t.Errorf("%s: found %q, want %q", fixture, found, want)
		}
	}
}

// The architecture test runs `go mod graph` over the whole workspace,
// and the self-build runs it as a test of specs alone, given what
// specs/go.sum locks and nothing fetched beside it: the go command reads
// the go.mod of each module the graph leads it to, whichever module of
// the workspace requires it, and fails where one is missing. It works
// there only because specs/go.sum locks each: so a dependency added to
// another module alone is found here, not on the grid. Which go.mod
// files the go command reads is its own to decide, by pruning and by
// what a module that is not pruned carries down to those beneath it, so
// it is not foretold here: the go command is asked, as on the grid
// (ungraphed says how).
func TestSpecsGoSumLocksTheGoModOfEveryModuleInTheGraph(t *testing.T) {
	root := repositoryRoot(t)
	cache := strings.TrimSpace(string(goOutput(t, root, nil, "env", "GOMODCACHE")))
	for _, found := range ungraphed(t, root, "specs", cache) {
		t.Error(found)
	}
}

func TestAGraphTheReadersGoSumDoesNotLockIsFound(t *testing.T) {
	// The fixtures' dependencies are in the module cache beside them. a
	// requires example.com/new (go 1.22), whose go.mod is read and whose
	// requirement example.com/pruned is not (the cache does not hold it),
	// and example.com/old (go 1.16), which leads through example.com/mid
	// (go 1.20) to example.com/r (no go directive): go 1.16 does not
	// prune, and what it carries down reaches r, whose go.mod is read.
	// b alone requires example.com/other.
	cache := requirementsFixture(t, "modcache")
	if found := ungraphed(t, requirementsFixture(t, "graphed"), "a", cache); len(found) != 0 {
		t.Errorf("a go.sum that locks the whole graph was refused: %v", found)
	}
	want := []string{
		"a/go.sum does not lock the go.mod of example.com/other v1.0.0, which go mod graph reads over the workspace: add its go.mod line to a/go.sum",
		"a/go.sum does not lock the go.mod of example.com/r v1.0.0, which go mod graph reads over the workspace: add its go.mod line to a/go.sum",
	}
	if found := ungraphed(t, requirementsFixture(t, "ungraphed"), "a", cache); !slices.Equal(found, want) {
		t.Errorf("found %q, want %q", found, want)
	}
	// a requires example.com/absent, which the cache does not hold.
	// a/go.sum locks it: what is missing is the cache's. b/go.sum does
	// not: that is said, and what absent leads to could not be read.
	uncached := requirementsFixture(t, "uncached")
	for reader, want := range map[string][]string{
		"a": {"a/go.sum locks the go.mod of example.com/absent v1.0.0, and the module cache at " + cache + " does not hold it: run `go mod download` in a, and test again"},
		"b": {
			"b/go.sum does not lock the go.mod of example.com/absent v1.0.0, which go mod graph reads over the workspace: add its go.mod line to b/go.sum",
			"the module cache at " + cache + " does not hold the go.mod of example.com/absent v1.0.0 either, so what it leads to was not read: run `go mod download` in b, and test again",
		},
	} {
		if found := ungraphed(t, uncached, reader, cache); !slices.Equal(found, want) {
			t.Errorf("%s: found %q, want %q", reader, found, want)
		}
	}
}

// No module of the workspace excludes a version of another, nor retracts
// one of its own. Under go.work the go command applies the excludes of
// every module it uses, and the self-build, which builds each module as
// the one module of its own workspace, applies that module's alone: the
// workspace's graph, which TestEveryModuleRequiresWhatTheWorkspaceSelects
// reads, and the module's own would disagree. A retraction says something
// of a published version to whoever requires it, and nothing requires a
// module of this repository at a version but go.work.
func TestNoModuleExcludesOrRetracts(t *testing.T) {
	for _, found := range amended(readModuleFiles(t, repositoryRoot(t))) {
		t.Error(found)
	}
}

func TestAModuleThatExcludesOrRetractsIsFound(t *testing.T) {
	if found := amended(readModuleFiles(t, requirementsFixture(t, "agree"))); len(found) != 0 {
		t.Errorf("modules that exclude and retract nothing were refused: %v", found)
	}
	want := []string{
		"a/go.mod excludes example.com/dep v1.0.0",
		"b/go.mod retracts [v0.1.0, v0.1.0]",
	}
	if found := amended(readModuleFiles(t, requirementsFixture(t, "amended"))); !slices.Equal(found, want) {
		t.Errorf("found %q, want %q", found, want)
	}
}

// requirementsFixture is the directory of a workspace among the
// fixtures: two modules, a and b, that require example.com/dep, which
// resolves beside them.
func requirementsFixture(t *testing.T, name string) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", "requirements", name))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// moduleGraph is what `go mod graph` prints in the workspace at root.
func moduleGraph(t *testing.T, root string) string {
	t.Helper()
	return string(goOutput(t, root, []string{"GOENV=off", "GOWORK=" + filepath.Join(root, "go.work"), "GOFLAGS="}, "mod", "graph"))
}

// unselected says each requirement of a module of the workspace at a
// version the workspace does not select, read from what `go mod graph`
// prints there: a module of the workspace is printed without a version,
// each requirement of it is an edge from it, and the workspace selects
// each other module at the highest version the graph holds of it. A
// module of the workspace that another requires is the workspace's
// own, whatever version is written, as are go and the toolchain.
func unselected(graph string) []string {
	edges, workspace := readGraph(graph)
	selected := map[string]string{}
	for _, each := range edges {
		for _, node := range []string{each.from, each.to} {
			if path, version, versioned := strings.Cut(node, "@"); versioned && semver.Compare(version, selected[path]) > 0 {
				selected[path] = version
			}
		}
	}
	var found []string
	for _, each := range edges {
		if !workspace[each.from] {
			continue
		}
		path, version, _ := strings.Cut(each.to, "@")
		if workspace[path] || path == "go" || path == "toolchain" {
			continue
		}
		if version != selected[path] {
			found = append(found, fmt.Sprintf("%s requires %s %s, and the workspace selects %s", each.from, path, version, selected[path]))
		}
	}
	slices.Sort(found)
	return found
}

// graphEdge is one requirement `go mod graph` prints: a module, at a
// version unless it is of the workspace, requires another.
type graphEdge struct{ from, to string }

// readGraph reads what `go mod graph` prints: its edges, and the
// modules of the workspace, which it prints without a version.
func readGraph(graph string) ([]graphEdge, map[string]bool) {
	var edges []graphEdge
	workspace := map[string]bool{}
	for line := range strings.Lines(graph) {
		from, to, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		edges = append(edges, graphEdge{from, to})
		for _, node := range []string{from, to} {
			if !strings.Contains(node, "@") {
				workspace[node] = true
			}
		}
	}
	return edges, workspace
}

// moduleFiles is what a module of the workspace says of what it
// requires: its directory, from the workspace's, the modules its go.mod
// requires from outside the workspace, what it excludes and retracts,
// and its go.sum's lines.
type moduleFiles struct {
	dir      string
	requires []requirement
	excludes []requirement
	retracts []retraction
	sum      []string
}

// retraction is a range of its own versions a go.mod retracts.
type retraction struct{ Low, High string }

// requirement is one module a go.mod requires, at a version.
type requirement struct {
	Path, Version string
	Indirect      bool
}

// readModuleFiles reads the go.mod and go.sum of every module the
// go.work at root uses.
func readModuleFiles(t *testing.T, root string) []moduleFiles {
	t.Helper()
	_, used := readGoWork(t, root)
	paths := map[string]bool{}
	mods := make([]struct {
		Module  struct{ Path string }
		Require []requirement
		Exclude []requirement
		Retract []retraction
	}, len(used))
	for i, dir := range used {
		goJSON(t, filepath.Join(root, dir), &mods[i], "mod", "edit", "-json")
		paths[mods[i].Module.Path] = true
	}
	var read []moduleFiles
	for i, dir := range used {
		files := moduleFiles{dir: dir, excludes: mods[i].Exclude, retracts: mods[i].Retract}
		for _, each := range mods[i].Require {
			if !paths[each.Path] {
				files.requires = append(files.requires, each)
			}
		}
		raw, err := os.ReadFile(filepath.Join(root, dir, "go.sum"))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		for line := range strings.Lines(string(raw)) {
			if line := strings.TrimSpace(line); line != "" {
				files.sum = append(files.sum, line)
			}
		}
		read = append(read, files)
	}
	return read
}

// unlocked says what each module's go.sum lacks of what its go.mod
// requires, and each module at a version that two go.sums lock unlike.
// A go.sum's line is a module, a version, and its hash: the version is
// followed by /go.mod where the line locks the module's go.mod alone.
func unlocked(modules []moduleFiles) []string {
	var found []string
	// Who first locked each module at each version, and how.
	type lock struct{ dir, hash string }
	first := map[string]lock{}
	for _, module := range modules {
		locked := map[string]bool{}
		for _, line := range module.sum {
			fields := strings.Fields(line)
			if len(fields) != 3 {
				found = append(found, fmt.Sprintf("%s/go.sum holds a line that is not a module, a version and a hash: %s", module.dir, line))
				continue
			}
			key := fields[0] + " " + fields[1]
			locked[key] = true
			if was, ok := first[key]; !ok {
				first[key] = lock{module.dir, fields[2]}
			} else if was.hash != fields[2] {
				found = append(found, fmt.Sprintf("%s/go.sum and %s/go.sum lock %s unlike", was.dir, module.dir, said(fields[0], fields[1])))
			}
		}
		for _, each := range module.requires {
			if !locked[each.Path+" "+each.Version+"/go.mod"] {
				found = append(found, fmt.Sprintf("%s/go.sum does not lock the go.mod of %s %s, which %s/go.mod requires", module.dir, each.Path, each.Version, module.dir))
			}
			if !locked[each.Path+" "+each.Version] {
				indirect := ""
				if each.Indirect {
					indirect = " // indirect"
				}
				found = append(found, fmt.Sprintf("%s/go.sum does not lock %s %s, which %s/go.mod requires%s", module.dir, each.Path, each.Version, module.dir, indirect))
			}
		}
	}
	slices.Sort(found)
	return found
}

// said is a module at a version as a go.sum's line names it, in words.
func said(path, version string) string {
	if version, ok := strings.CutSuffix(version, "/go.mod"); ok {
		return "the go.mod of " + path + " " + version
	}
	return path + " " + version
}

// ungraphed says the go.mod of each module at each version that `go mod
// graph` reads over the workspace at root and the go.sum of the module
// at reader does not lock. It asks the go command, in the situation of
// reader's test on the grid: over a module cache that holds the go.mod
// files reader's go.sum locks, taken from the module cache at from, and
// nothing else, with nothing fetched. Where the go command fails for
// want of a go.mod, that one is said, taken from from too, and the go
// command asked again, until it reads the whole graph.
func ungraphed(t *testing.T, root, reader, from string) []string {
	t.Helper()
	cache := t.TempDir()
	// take puts the go.mod of a module at a version into the cache, and
	// says whether from holds it.
	take := func(path, version string) bool {
		escapedPath, err := gomodule.EscapePath(path)
		if err != nil {
			t.Fatal(err)
		}
		escapedVersion, err := gomodule.EscapeVersion(version)
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join("cache", "download", escapedPath, "@v", escapedVersion+".mod")
		raw, err := os.ReadFile(filepath.Join(from, file))
		if os.IsNotExist(err) {
			return false
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(cache, file)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cache, file), raw, 0o644); err != nil {
			t.Fatal(err)
		}
		return true
	}
	raw, err := os.ReadFile(filepath.Join(root, reader, "go.sum"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	locked := map[string]bool{}
	for line := range strings.Lines(string(raw)) {
		// A line that is not a module, a version and a hash is unlocked's
		// to say.
		if fields := strings.Fields(line); len(fields) == 3 {
			if version, ok := strings.CutSuffix(fields[1], "/go.mod"); ok {
				locked[fields[0]+" "+version] = true
				take(fields[0], version)
			}
		}
	}
	// Nothing of this machine's go settings reaches the measurement: a
	// module GOPRIVATE or GONOPROXY names is fetched past GOPROXY=off,
	// and a setting written with go env -w is read unless GOENV is off.
	env := []string{"GOENV=off", "GOPRIVATE=", "GONOPROXY=", "GONOSUMDB=", "GOINSECURE=", "GOVCS=*:off", "GOPROXY=off", "GOFLAGS=-mod=mod", "GOSUMDB=off", "GOTOOLCHAIN=local", "GOMODCACHE=" + cache, "GOWORK=" + filepath.Join(root, "go.work")}
	var found []string
	// Each go.mod the go command failed for want of: once taken, it is
	// not wanted again.
	wanted := map[string]bool{}
	for {
		_, complained, err := goRun(root, env, "mod", "graph")
		if err == nil {
			break
		}
		missing := lookupDisabled.FindSubmatch(complained)
		if missing == nil {
			t.Fatalf("go mod graph: %v: %s", err, complained)
		}
		path, version := string(missing[1]), string(missing[2])
		if wanted[path+" "+version] {
			t.Fatalf("go mod graph wants the go.mod of %s %s again, from the cache it was given: %s", path, version, complained)
		}
		wanted[path+" "+version] = true
		if locked[path+" "+version] {
			found = append(found, fmt.Sprintf("%s/go.sum locks the go.mod of %s %s, and the module cache at %s does not hold it: run `go mod download` in %s, and test again", reader, path, version, from, reader))
			break
		}
		found = append(found, fmt.Sprintf("%s/go.sum does not lock the go.mod of %s %s, which go mod graph reads over the workspace: add its go.mod line to %s/go.sum", reader, path, version, reader))
		if !take(path, version) {
			found = append(found, fmt.Sprintf("the module cache at %s does not hold the go.mod of %s %s either, so what it leads to was not read: run `go mod download` in %s, and test again", from, path, version, reader))
			break
		}
	}
	slices.Sort(found)
	return found
}

// lookupDisabled is how the go command says, with GOPROXY=off, which
// module at which version it would have fetched.
var lookupDisabled = regexp.MustCompile(`(\S+)@(\S+): module lookup disabled by GOPROXY=off`)

// amended says each exclude and each retract a module's go.mod writes.
func amended(modules []moduleFiles) []string {
	var found []string
	for _, module := range modules {
		for _, each := range module.excludes {
			found = append(found, fmt.Sprintf("%s/go.mod excludes %s %s", module.dir, each.Path, each.Version))
		}
		for _, each := range module.retracts {
			found = append(found, fmt.Sprintf("%s/go.mod retracts [%s, %s]", module.dir, each.Low, each.High))
		}
	}
	slices.Sort(found)
	return found
}

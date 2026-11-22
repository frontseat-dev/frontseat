package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// public is every package another module of this repository, or
// frontseat-enterprise, needs that is in no SDK, each with why. Every
// other Go package is in the Go SDK (sdk/go), under an internal/ path,
// a binary's main (cmd/<binary>), the test support (server/testsupport,
// which the import rules hold to tests) or a package of tests alone.
var public = map[string]string{
	"server/sandbox":                  "the sandbox service: frontseat-enterprise embeds it (owner's ruling)",
	"server/sandbox/driver":           "the port the sandbox service drives its drivers through, for frontseat-enterprise's drivers to implement (owner's ruling)",
	"server/sandbox/driver/openshell": "the one OpenShell driver, which frontseat-enterprise composes around and never copies (owner's ruling)",
	"server/git":                      "git, run hardened: the command line's version control runs it too",
	"server/telemetry":                "OpenTelemetry: the command line instruments what it asks with it",
	"local":                           "what the local stack is, read from its files: the box's build, the stack's control plane and the test support read it; and the checkout a program is started at (Checkout), which the stack's program, the gate and what prepares the plugins find alike; and the directory the command line keeps what it keeps in (Config), where the stack lays out its owner's token and the command line and the test support read it",
	"local/ports":                     "the ports a box and the local stack are reached on, each said once: the server, the command line and the test support read them",
	"local/box":                       "the box's one build: the stack's program (frontseat-local box, up) and the gate build it here, and a spec holds it to what it was built with",
	"local/stack":                     "the local stack brought up and down from a checkout: the stack's program (frontseat-local up, down) runs it",
}

// goSource is one Go file of the repository, parsed.
type goSource struct {
	// rel is its path from the root, with slashes.
	rel  string
	file *ast.File
}

// goSources are the Go files beneath dir, of the repository at root,
// beneath every module: what is built, fixtures and hidden directories
// left out.
func goSources(t *testing.T, root, dir string) []goSource {
	t.Helper()
	var out []goSource
	err := filepath.WalkDir(dir, func(at string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if at != dir && (at == filepath.Join(root, "target") || d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(at, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), at, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, at)
		out = append(out, goSource{filepath.ToSlash(rel), file})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// fixtureSources are the Go files of the repository's own modules that
// are beneath a testdata directory: fixtures, which go's patterns leave
// out but an import path reaches all the same. What is beneath a go.mod
// of its own is another module's, and no import of this one's.
func fixtureSources(t *testing.T, root string) []goSource {
	t.Helper()
	modules := workspaceModules(t, root)
	var out []goSource
	err := filepath.WalkDir(root, func(at string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, at)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if at != root && (at == filepath.Join(root, "target") || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(at, "go.mod")); err == nil && !slices.Contains(modules, rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(at, ".go") || !slices.Contains(strings.Split(rel, "/"), "testdata") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), at, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		out = append(out, goSource{rel, file})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// goPackage is a directory's package as its files that are not tests
// declare it.
type goPackage struct {
	dir, name string
	// files are the names of its files that are not tests, but a doc.go
	// that declares nothing: a package that has none is of tests alone.
	files []string
	// others are the other packages the directory's files that are not
	// tests declare, as a file built only when asked declares a main
	// beside a library.
	others []string
}

// packagesOf are the packages of the sources, by directory; a directory
// of tests alone has no name and no files.
func packagesOf(sources []goSource) map[string]*goPackage {
	out := map[string]*goPackage{}
	for _, source := range sources {
		dir := path.Dir(source.rel)
		p := out[dir]
		if p == nil {
			p = &goPackage{dir: dir}
			out[dir] = p
		}
		if !strings.HasSuffix(source.rel, "_test.go") {
			if p.name != "" && p.name != source.file.Name.Name && !slices.Contains(p.others, source.file.Name.Name) {
				p.others = append(p.others, source.file.Name.Name)
			}
			if p.name == "" {
				p.name = source.file.Name.Name
			}
			if path.Base(source.rel) != "doc.go" || len(topLevel(source.file)) > 0 {
				p.files = append(p.files, path.Base(source.rel))
			}
		}
	}
	return out
}

// offTheLevels says of each package that is public, in no SDK and not
// on the list given, and of each entry of the list that is no such
// package, what is wrong. A binary is cmd/<binary>, wherever it is but
// a fixture; a fixture is no package anyone imports; and a directory is
// one package.
func offTheLevels(packages []goPackage, listed map[string]string) []string {
	var out []string
	public := map[string]bool{}
	for _, p := range packages {
		parts := strings.Split(p.dir, "/")
		binary := len(parts) > 1 && parts[len(parts)-2] == "cmd"
		tests := len(p.files) == 0
		fixture := slices.Contains(parts, "testdata")
		switch {
		case len(p.others) > 0:
			out = append(out, fmt.Sprintf("%s declares the packages %s and %s: a directory is one package", p.dir, p.name, strings.Join(p.others, ", ")))
		case fixture && p.name != "main" && !tests && !slices.Contains(parts, "internal"):
			out = append(out, fmt.Sprintf("%s is a package beneath testdata that an import reaches: a fixture is a program, or of a module of its own", p.dir))
		case fixture:
		case p.name == "main" && !binary:
			out = append(out, fmt.Sprintf("%s is a program that is not cmd/<binary>", p.dir))
		case beneath(p.dir, "sdk/go"), beneath(p.dir, "server/testsupport"), slices.Contains(parts, "internal"), p.name == "main", tests:
		case listed[p.dir] == "":
			out = append(out, fmt.Sprintf("%s is public and in no SDK: it goes under internal/, or on the list of what another module or frontseat-enterprise needs (public), with why", p.dir))
		default:
			public[p.dir] = true
		}
	}
	for _, dir := range slices.Sorted(maps.Keys(listed)) {
		if !public[dir] {
			out = append(out, fmt.Sprintf("%s is on the list of what is public, and is no such package", dir))
		}
	}
	return out
}

// Public for developers is in an SDK, what another module or
// frontseat-enterprise needs is on one list, and everything else is
// internal or test support.
func TestEveryPublicPackageIsInAnSDKOrOnTheList(t *testing.T) {
	var packages []goPackage
	root := repositoryRoot(t)
	for _, p := range packagesOf(append(goSources(t, root, root), fixtureSources(t, root)...)) {
		packages = append(packages, *p)
	}
	for _, wrong := range offTheLevels(packages, public) {
		t.Error(wrong)
	}
}

func TestAPublicPackageOnNoListIsFound(t *testing.T) {
	listed := map[string]string{"server/sandbox": "the service"}
	held := []goPackage{
		{"sdk/go", "frontseat", []string{"frontseat.go"}, nil},
		{"sdk/go/internal/transport", "transport", []string{"transport.go"}, nil},
		{"cli/internal/cmd", "cmd", []string{"root.go"}, nil},
		{"server/testsupport/testbox", "testbox", []string{"testbox.go"}, nil},
		{"server/internal/build", "build", []string{"build.go"}, nil},
		{"cli/cmd/frontseat", "main", []string{"main.go"}, nil},
		{"specs/cmd/prepare-modules", "main", []string{"main.go"}, nil},
		{"specs/build", "", nil, nil},
		{"specs/compare", "compare", nil, nil},
		{"server/sandbox", "sandbox", []string{"sandbox.go"}, nil},
		// A fixture program, and a fixture's own internal package.
		{"server/sandbox/testdata/service-route", "main", []string{"main.go"}, nil},
		{"server/sandbox/testdata/app/internal/ledger", "ledger", []string{"ledger.go"}, nil},
	}
	if wrong := offTheLevels(held, listed); len(wrong) != 0 {
		t.Errorf("refused what the levels allow: %v", wrong)
	}
	for _, c := range []struct {
		refused goPackage
		says    string
	}{
		// The commands beneath the binary's own directory (P5-08).
		{goPackage{"cli/cmd/frontseat/cmd", "cmd", []string{"root.go"}, nil}, "cli/cmd/frontseat/cmd is public and in no SDK"},
		// A server package outside internal/ that no other module needs.
		{goPackage{"server/controlplane", "controlplane", []string{"controlplane.go"}, nil}, "server/controlplane is public and in no SDK"},
		// A driver for tests, public (P5-02).
		{goPackage{"server/sandbox/driver/memory", "memory", []string{"memory.go"}, nil}, "server/sandbox/driver/memory is public and in no SDK"},
		// A program that is not cmd/<binary>, though internal (P5-11).
		{goPackage{"server/testsupport/modules/prepare", "main", []string{"main.go"}, nil}, "server/testsupport/modules/prepare is a program that is not cmd/<binary>"},
		// A package of tests that has grown a file.
		{goPackage{"specs/compare", "compare", []string{"helper.go"}, nil}, "specs/compare is public and in no SDK"},
		// A doc.go that declares something is no doc alone.
		{goPackage{"server/foo", "foo", []string{"doc.go"}, nil}, "server/foo is public and in no SDK"},
		// A library beneath testdata, which an import reaches.
		{goPackage{"server/sandbox/testdata/lib", "lib", []string{"lib.go"}, nil}, "server/sandbox/testdata/lib is a package beneath testdata"},
		// A program built only when asked, beside a library.
		{goPackage{"server/foo/bar", "bar", []string{"bar.go", "gen.go"}, []string{"main"}}, "server/foo/bar declares the packages bar and main"},
		// The same, in a fixture.
		{goPackage{"server/sandbox/testdata/route", "main", []string{"main.go", "lib.go"}, []string{"route"}}, "server/sandbox/testdata/route declares the packages main and route"},
		// What the SDK's directory spells, beside it.
		{goPackage{"sdk/gox", "gox", []string{"x.go"}, nil}, "sdk/gox is public and in no SDK"},
	} {
		wrong := offTheLevels(append(slices.Clone(held), c.refused), listed)
		if len(wrong) != 1 || !strings.HasPrefix(wrong[0], c.says) {
			t.Errorf("%s: %v, want one that says %q", c.refused.dir, wrong, c.says)
		}
	}
	// An entry of the list that is no public package.
	stale := map[string]string{"server/sandbox": "the service", "server/sandbox/driver/e2b": "gone"}
	if wrong := offTheLevels(held, stale); len(wrong) != 1 || !strings.HasPrefix(wrong[0], "server/sandbox/driver/e2b is on the list") {
		t.Errorf("a stale entry: %v", wrong)
	}
	internal := map[string]string{"server/sandbox": "the service", "cli/internal/cmd": "listed"}
	if wrong := offTheLevels(held, internal); len(wrong) != 1 || !strings.HasPrefix(wrong[0], "cli/internal/cmd is on the list") {
		t.Errorf("an internal package listed: %v", wrong)
	}
}

// generatedAPIs are where the API's Go and the plugin contract's are
// generated: packages named for their version, which a file names for
// their service (buildv1, pluginv1).
var generatedAPIs = []string{"sdk/go/api", "server/internal/pluginapi", "server/internal/runnerapi", "server/internal/sourceapi"}

// version is how a package named for its version is named: v1, v2alpha1.
var version = regexp.MustCompile(`^(v[0-9]+)((alpha|beta)[0-9]*)?$`)

// aliases are the names Go forces on a package of this repository, each
// where it is written, and the one name it is given there: beside
// os/exec, in a package that declares run, in a package named git.
var aliases = map[string]string{
	"server/internal/exec":        "execport",
	"server/internal/exec/run":    "runexec",
	"server/git":                  "rungit",
	"server/internal/source":      "sourced",
	"server/internal/source/told": "tell",
}

// generatedFile is whether a file is one a template of the contracts
// writes, which names what it imports as its generator does: beneath
// where the generated API is, and named as generated Go.
func generatedFile(rel string) bool {
	return slices.ContainsFunc(generatedAPIs, func(root string) bool { return beneath(rel, root) }) && (strings.HasSuffix(rel, ".pb.go") || strings.HasSuffix(rel, ".connect.go"))
}

// misnamed says how a file imports a package of this repository under a
// name other than its own, or by its own where it is generated. An alias
// is allowed where the name is taken already, by another import of this
// repository or of the standard library or by a declaration of the
// file's package (declared), which Go forces, or by the file's own
// package, which Go allows but reads as itself; and then only the name
// the package is given there (aliases). The API's generated packages,
// all named for their version, are always named for their service and
// version (buildv1).
func misnamed(file *ast.File, nameOf func(rel string) string, declared map[string]bool) []string {
	// What each import is called in the file, and whether it is of this
	// repository or the standard library.
	called := make([]string, len(file.Imports))
	ours := make([]bool, len(file.Imports))
	for i, spec := range file.Imports {
		imported, _ := strconv.Unquote(spec.Path.Value)
		rel, local := strings.CutPrefix(imported, module+"/")
		parts := strings.Split(imported, "/")
		ours[i] = local || !strings.Contains(parts[0], ".")
		switch {
		case spec.Name != nil:
			called[i] = spec.Name.Name
		case local:
			called[i] = nameOf(rel)
		case len(parts) > 1 && version.MatchString(parts[len(parts)-1]):
			called[i] = parts[len(parts)-2]
		default:
			called[i] = path.Base(imported)
		}
	}
	var out []string
	for i, spec := range file.Imports {
		imported, _ := strconv.Unquote(spec.Path.Value)
		rel, local := strings.CutPrefix(imported, module+"/")
		if !local {
			continue
		}
		if slices.ContainsFunc(generatedAPIs, func(root string) bool { return beneath(rel, root) }) && version.MatchString(path.Base(rel)) {
			if want := path.Base(path.Dir(rel)) + version.FindStringSubmatch(path.Base(rel))[1]; called[i] != want {
				out = append(out, fmt.Sprintf("imports %s as %s: the API's generated Go is named for its service, %s", rel, called[i], want))
			}
			continue
		}
		if spec.Name == nil || spec.Name.Name == "_" {
			continue
		}
		alias, own := spec.Name.Name, nameOf(rel)
		taken := own == file.Name.Name || declared[own]
		for j := range file.Imports {
			taken = taken || j != i && ours[j] && called[j] == own
		}
		switch {
		case alias == "." || alias == own || !taken:
			out = append(out, fmt.Sprintf("imports %s as %s: a package of this repository is imported by its own name, %s", rel, alias, own))
		case aliases[rel] != alias:
			out = append(out, fmt.Sprintf("imports %s as %s: where its name is taken it is called %q, and a package given no such name is renamed by none (aliases)", rel, alias, aliases[rel]))
		}
	}
	return out
}

// N10: a package of this repository is called what it is called, by
// every file that imports it.
func TestARepositoryPackageIsImportedByItsOwnName(t *testing.T) {
	root := repositoryRoot(t)
	sources := goSources(t, root, root)
	packages := packagesOf(sources)
	// What each package of a directory declares, by its package clause.
	declared := map[string]map[string]bool{}
	for _, source := range sources {
		key := path.Dir(source.rel) + " " + source.file.Name.Name
		if declared[key] == nil {
			declared[key] = map[string]bool{}
		}
		for name := range topLevel(source.file) {
			declared[key][name] = true
		}
	}
	nameOf := func(rel string) string {
		if p := packages[rel]; p != nil && p.name != "" {
			return p.name
		}
		return path.Base(rel)
	}
	for _, source := range sources {
		if generatedFile(source.rel) {
			continue
		}
		for _, wrong := range misnamed(source.file, nameOf, declared[path.Dir(source.rel)+" "+source.file.Name.Name]) {
			t.Errorf("%s %s", source.rel, wrong)
		}
	}
}

// topLevel are the names a file declares in its package's scope.
func topLevel(file *ast.File) map[string]bool {
	out := map[string]bool{}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				out[d.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					out[s.Name.Name] = true
				case *ast.ValueSpec:
					for _, name := range s.Names {
						out[name.Name] = true
					}
				}
			}
		}
	}
	return out
}

func TestAPackageImportedUnderAnotherNameIsFound(t *testing.T) {
	names := map[string]string{"server/git": "git", "server/internal/exec": "exec", "server/internal/exec/run": "run", "server/testsupport/box": "box", "server/other/exec": "exec", "server/yaml": "yaml"}
	nameOf := func(rel string) string {
		if name, ok := names[rel]; ok {
			return name
		}
		return path.Base(rel)
	}
	for name, c := range map[string]struct {
		source   string
		declared []string
		refused  bool
	}{
		"its own name":                             {`package a; import "M/sdk/go/entity"`, nil, false},
		"the generated API, for its service":       {`package a; import buildv1 "M/sdk/go/api/frontseat/build/v1alpha1"`, nil, false},
		"the plugin contract, for its service":     {`package a; import pluginv1 "M/server/internal/pluginapi/frontseat/plugin/v1alpha1"`, nil, false},
		"a generated client, by its own name":      {`package a; import "M/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"`, nil, false},
		"beside another of its name":               {`package fix; import ("os/exec"; execport "M/server/internal/exec")`, nil, false},
		"in a package of its name":                 {`package git; import rungit "M/server/git"`, nil, false},
		"where the package declares its name":      {`package build; import runexec "M/server/internal/exec/run"`, []string{"run"}, false},
		"linked for what it does":                  {`package a; import _ "M/server/internal/build"`, nil, false},
		"under another name":                       {`package a; import entityref "M/sdk/go/entity"`, nil, true},
		"under its own name, said again":           {`package a; import entity "M/sdk/go/entity"`, nil, true},
		"into the file's scope":                    {`package a; import . "M/sdk/go/entity"`, nil, true},
		"test support under its callers' name":     {`package a; import testbox "M/server/testsupport/box"`, nil, true},
		"the generated API by its version":         {`package a; import v1alpha1 "M/sdk/go/api/frontseat/build/v1alpha1"`, nil, true},
		"the generated API by another name":        {`package a; import build "M/sdk/go/api/frontseat/build/v1alpha1"`, nil, true},
		"in the tests of a package of its name":    {`package git_test; import rungit "M/server/git"`, nil, true},
		"where another package declares it":        {`package build_test; import runexec "M/server/internal/exec/run"`, []string{"other"}, true},
		"beside another of a different name":       {`package fix; import ("os/exec"; execport "M/server/internal/exec/run")`, nil, true},
		"two of one name, both renamed":            {`package fix; import (x "M/server/internal/exec"; y "M/server/other/exec")`, nil, true},
		"beside a blank import of its name":        {`package a; import (_ "M/server/internal/build"; otherbuild "M/server/other/build")`, nil, true},
		"beside an external one renamed to it":     {`package a; import (git "github.com/go-git/go-git/v5"; rungit "M/server/git")`, nil, true},
		"beside an external one of its name":       {`package a; import ("gopkg.in/yaml.v3"; yamlnode "M/server/yaml")`, nil, true},
		"forced, under a name of its own choosing": {`package fix; import ("os/exec"; buildexec "M/server/internal/exec")`, nil, true},
		"forced, a package given no name":          {`package fix; import ("os/exec"; otherexec "M/server/other/exec")`, nil, true},
		"the generated API of a later version":     {`package a; import buildv2 "M/sdk/go/api/frontseat/build/v2beta1"`, nil, false},
		"a generated directory that is no version": {`package a; import "M/sdk/go/api/frontseat/build/vendor"`, nil, false},
		"that directory renamed":                   {`package a; import buildv1 "M/sdk/go/api/frontseat/build/vendor"`, nil, true},
		"the generated API unnamed":                {`package a; import "M/sdk/go/api/frontseat/build/v1alpha1"`, nil, true},
	} {
		t.Run(name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "a.go", strings.ReplaceAll(c.source, `"M/`, `"`+module+"/"), parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			declared := map[string]bool{}
			for _, each := range c.declared {
				declared[each] = true
			}
			if wrong := misnamed(file, nameOf, declared); (len(wrong) > 0) != c.refused {
				t.Fatalf("found %v, want refused %v", wrong, c.refused)
			}
		})
	}
}

func TestOnlyGeneratedFilesAreLeftUnnamed(t *testing.T) {
	for rel, generated := range map[string]bool{
		"sdk/go/api/frontseat/build/v1alpha1/run.pb.go":                           true,
		"sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect/run.connect.go": true,
		"server/internal/pluginapi/frontseat/plugin/v1alpha1/plugin.pb.go":        true,
		"sdk/go/api/frontseat/build/v1alpha1/helper.go":                           false,
		"server/internal/build/hand.pb.go":                                        false,
		"server/internal/build/runs.go":                                           false,
	} {
		if generatedFile(rel) != generated {
			t.Errorf("%s: generated %v, want %v", rel, !generated, generated)
		}
	}
}

// publishable are the crates for third parties, released at one
// version, the SDK's: the plugin SDK and its testkit.
var publishable = []string{"frontseat-plugin", "frontseat-plugin-test"}

// publishing says of each crate that is not publishable that it may be
// published, and of each publishable one what it lacks to be published
// at the SDK's version. A publishable crate says nothing of where it is
// published (publish absent or true, which cargo says as nothing), has
// the SDK's version, a description and a licence, and depends by path
// only on another publishable crate, naming its version, as a registry
// is told it, and on nothing from git or another registry; a dependency
// of its tests alone that names no version is left out of what is
// published. Every other crate is unpublished (publish = false, which
// cargo says as no registry).
func publishing(packages []cargoPackage) []string {
	version := map[string]string{}
	for _, p := range packages {
		version[p.Name] = p.Version
	}
	var out []string
	for _, p := range packages {
		if !slices.Contains(publishable, p.Name) {
			if p.Publish == nil || len(*p.Publish) > 0 {
				out = append(out, fmt.Sprintf("%s may be published: only the plugin SDK and its testkit are, and every other crate says publish = false", p.Name))
			}
			continue
		}
		if p.Publish != nil {
			out = append(out, fmt.Sprintf("%s is publishable only to %v: it says no publish, or publish = true", p.Name, *p.Publish))
		}
		if sdk := version["frontseat-plugin"]; p.Version != sdk {
			out = append(out, fmt.Sprintf("%s is at %s: it is released with the plugin SDK, at %s", p.Name, p.Version, sdk))
		}
		if p.Description == "" || p.License == "" {
			out = append(out, fmt.Sprintf("%s says no description or no licence, which a registry requires", p.Name))
		}
		for _, d := range p.Dependencies {
			if d.Kind == "dev" && d.Req == "*" {
				continue // cargo leaves it out of what is published
			}
			if strings.HasPrefix(d.Source, "git+") || d.Registry != "" {
				out = append(out, fmt.Sprintf("%s depends on %s from %s%s: a published crate depends only on crates.io", p.Name, d.Name, d.Source, d.Registry))
				continue
			}
			if d.Path == "" {
				continue
			}
			if !slices.Contains(publishable, d.Name) || (d.Req != "^"+version[d.Name] && d.Req != "="+version[d.Name]) {
				out = append(out, fmt.Sprintf("%s depends on %s by its path as %q: a published crate depends on another published crate at its version, %s", p.Name, d.Name, d.Req, version[d.Name]))
			}
		}
	}
	return out
}

// S15 for Rust: the plugin SDK and its testkit are public, and every
// other crate is Frontseat's own; and every crate is one the workspace
// builds, so none is left out of what is held.
func TestOnlyThePluginSDKAndItsTestkitArePublishable(t *testing.T) {
	root := repositoryRoot(t)
	packages := cargoPackages(t, root)
	for _, wrong := range publishing(packages) {
		t.Error(wrong)
	}
	members := []string{"Cargo.toml"}
	for _, p := range packages {
		rel, err := filepath.Rel(root, p.ManifestPath)
		if err != nil {
			t.Fatal(err)
		}
		members = append(members, filepath.ToSlash(rel))
	}
	var found []string
	err := filepath.WalkDir(root, func(at string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && at != root && (at == filepath.Join(root, "target") || d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == "Cargo.toml" {
			rel, err := filepath.Rel(root, at)
			found = append(found, filepath.ToSlash(rel))
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, stray := range unbuilt(found, members) {
		t.Errorf("%s is a crate the workspace does not build: it is a member of Cargo.toml, or a fixture beneath testdata", stray)
	}
}

// unbuilt are the manifests found that are neither the workspace's own
// nor a fixture's.
func unbuilt(found, members []string) []string {
	var out []string
	for _, manifest := range found {
		if !slices.Contains(members, manifest) && !slices.Contains(strings.Split(manifest, "/"), "testdata") {
			out = append(out, manifest)
		}
	}
	return out
}

func TestACrateTheWorkspaceDoesNotBuildIsFound(t *testing.T) {
	members := []string{"Cargo.toml", "sdk/plugin/Cargo.toml", "plugins/go/Cargo.toml"}
	held := []string{"Cargo.toml", "sdk/plugin/Cargo.toml", "plugins/go/Cargo.toml", "plugins/cargo/testdata/valid/Cargo.toml"}
	if stray := unbuilt(held, members); len(stray) != 0 {
		t.Errorf("refused what is held: %v", stray)
	}
	refused := []string{"plugins/nix/Cargo.toml", "local/tool/Cargo.toml", "plugins/testdatax/Cargo.toml"}
	if stray := unbuilt(append(slices.Clone(held), refused...), members); !slices.Equal(stray, refused) {
		t.Errorf("found %v, want %v", stray, refused)
	}
}

func TestAPublishableCrateIsFound(t *testing.T) {
	none, registry := []string{}, []string{"crates-io"}
	sdk := cargoPackage{Name: "frontseat-plugin", Version: "0.3.0", Description: "d", License: "Apache-2.0"}
	testkit := func(change func(*cargoPackage)) []cargoPackage {
		kit := cargoPackage{Name: "frontseat-plugin-test", Version: "0.3.0", Description: "d", License: "Apache-2.0", Dependencies: []cargoDependency{
			{Name: "frontseat-plugin", Path: "/r/sdk/plugin", Req: "^0.3.0"},
			{Name: "serde", Req: "^1"},
			{Name: "frontseat-plugin-go", Path: "/r/plugins/go", Req: "*", Kind: "dev"},
		}}
		change(&kit)
		return []cargoPackage{sdk, kit, {Name: "frontseat-plugin-go", Version: "0.0.0", Publish: &none}}
	}
	if wrong := publishing(testkit(func(*cargoPackage) {})); len(wrong) != 0 {
		t.Errorf("refused what is held: %v", wrong)
	}
	if wrong := publishing(testkit(func(kit *cargoPackage) { kit.Dependencies[0].Req = "=0.3.0" })); len(wrong) != 0 {
		t.Errorf("refused the SDK named at its exact version: %v", wrong)
	}
	for name, c := range map[string]struct {
		packages []cargoPackage
		reason   string
	}{
		"a plugin that says nothing":           {[]cargoPackage{{Name: "frontseat-plugin-go"}}, "may be published"},
		"a plugin published to a registry":     {[]cargoPackage{{Name: "frontseat-conventional-commits", Publish: &registry}}, "may be published"},
		"the SDK unpublished":                  {[]cargoPackage{{Name: "frontseat-plugin", Version: "0.3.0", Description: "d", License: "Apache-2.0", Publish: &none}}, "publishable only to"},
		"the SDK published to a registry":      {[]cargoPackage{{Name: "frontseat-plugin", Version: "0.3.0", Description: "d", License: "Apache-2.0", Publish: &registry}}, "publishable only to"},
		"the SDK with no description":          {[]cargoPackage{{Name: "frontseat-plugin", Version: "0.3.0", License: "Apache-2.0"}}, "no description"},
		"the SDK with no licence":              {[]cargoPackage{{Name: "frontseat-plugin", Version: "0.3.0", Description: "d"}}, "no licence"},
		"the testkit without the SDK":          {testkit(func(kit *cargoPackage) { kit.Dependencies = kit.Dependencies[1:] })[1:], "released with the plugin SDK"},
		"the testkit unpublished":              {testkit(func(kit *cargoPackage) { kit.Publish = &none }), "publishable only to"},
		"the testkit at a version of its own":  {testkit(func(kit *cargoPackage) { kit.Version = "0.4.0" }), "released with the plugin SDK"},
		"the testkit with no licence":          {testkit(func(kit *cargoPackage) { kit.License = "" }), "no licence"},
		"the testkit on the SDK by path alone": {testkit(func(kit *cargoPackage) { kit.Dependencies[0].Req = "*" }), "at its version"},
		"the testkit on another version of it": {testkit(func(kit *cargoPackage) { kit.Dependencies[0].Req = "^0.2.0" }), "at its version"},
		"the testkit on a plugin by its path":  {testkit(func(kit *cargoPackage) { kit.Dependencies[2].Kind = "" }), "at its version"},
		"the testkit building with a plugin":   {testkit(func(kit *cargoPackage) { kit.Dependencies[2].Kind = "build" }), "at its version"},
		"a test dependency at a version":       {testkit(func(kit *cargoPackage) { kit.Dependencies[2].Req = "^0.0.0" }), "at its version"},
		"the testkit on a git repository":      {testkit(func(kit *cargoPackage) { kit.Dependencies[1].Source = "git+https://example.com/serde" }), "only on crates.io"},
		"the testkit on another registry":      {testkit(func(kit *cargoPackage) { kit.Dependencies[1].Registry = "sparse+https://example.com/" }), "only on crates.io"},
	} {
		if wrong := publishing(c.packages); len(wrong) != 1 || !strings.Contains(wrong[0], c.reason) {
			t.Errorf("%s: %v, want it refused as %q", name, wrong, c.reason)
		}
	}
}

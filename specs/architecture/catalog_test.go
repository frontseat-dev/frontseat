package architecture

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"go.yaml.in/yaml/v4"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

// TestThisRepositorysComponentsDependOnEachOtherWithoutACycle holds the
// catalog frontseat keeps in step to a graph with no cycle: what one
// component needs never needs it back, tests included. A cycle comes
// from where code is put, not from Go: test support that lives with what
// uses what it tests. The self-build holds each dependsOn to what the
// source says; this holds what it says to having no cycle.
func TestThisRepositorysComponentsDependOnEachOtherWithoutACycle(t *testing.T) {
	on := componentsOf(t, repositoryRoot(t))
	if len(on) == 0 {
		t.Fatal("found no component in this repository's catalog files")
	}
	if cycle := cycleIn(on); cycle != nil {
		t.Errorf("components depend on each other in a cycle: %s", strings.Join(cycle, " -> "))
	}
}

// TestACycleOfComponentsIsFound is the check's control: a cycle is
// found where there is one, through a component between, and none where
// two depend on a third.
func TestACycleOfComponentsIsFound(t *testing.T) {
	if cycle := cycleIn(map[string][]string{"a": {"b"}, "b": {"c"}, "c": {"a"}}); len(cycle) != 4 {
		t.Errorf("a cycle of three was not found: %v", cycle)
	}
	if cycle := cycleIn(map[string][]string{"a": {"c"}, "b": {"c"}, "c": nil}); cycle != nil {
		t.Errorf("found a cycle where two depend on a third: %v", cycle)
	}
	// What a catalog file says is read as Backstage reads a reference: a
	// kind in any case, a namespace where one is written and the default
	// where none is, and a name in any case.
	for name, c := range map[string]struct {
		catalog string
		cycle   bool
	}{
		"through a kind written in capitals and a namespace written": {`
kind: Component
metadata: {name: a}
spec: {dependsOn: ["Component:B"]}
---
kind: Component
metadata: {name: b}
spec: {dependsOn: ["component:other/c"]}
---
kind: Component
metadata: {name: c, namespace: other}
spec: {dependsOn: ["COMPONENT:default/a"]}
`, true},
		"of two of one name in two namespaces, which are two": {`
kind: Component
metadata: {name: x}
spec: {dependsOn: ["component:y"]}
---
kind: Component
metadata: {name: y, namespace: other}
spec: {dependsOn: ["component:default/x"]}
`, false},
		"through a resource, which is no component": {`
kind: Component
metadata: {name: a}
spec: {dependsOn: ["resource:a"]}
`, false},
	} {
		on := map[string][]string{}
		if err := componentsIn([]byte(c.catalog), on); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if cycle := cycleIn(on); (cycle != nil) != c.cycle {
			t.Errorf("%s: the cycle found is %v, want one to be found: %v (read as %v)", name, cycle, c.cycle, on)
		}
	}
}

// TestEveryEntityOfThisRepositoryHasARefOfItsOwn holds N1: an entity is
// one by its ref, its kind, namespace and name, as Backstage compares
// them, without regard to case; so a Component and a System may share a
// name, and two entities of one kind and namespace may not.
func TestEveryEntityOfThisRepositoryHasARefOfItsOwn(t *testing.T) {
	documents := catalogDocuments(t, repositoryRoot(t))
	twice, err := heldTwice(documents...)
	if err != nil {
		t.Fatal(err)
	}
	for _, each := range twice {
		t.Errorf("two entities of this repository have one ref: %s", each)
	}
}

// TestAnEntityRefHeldTwiceIsFound is the check's control: one ref written
// twice is found, however its kind and name are cased; one name of two
// kinds, or of two namespaces, is two refs, and passes.
func TestAnEntityRefHeldTwiceIsFound(t *testing.T) {
	for name, catalog := range map[string]string{
		"two kinds":      "kind: Component\nmetadata: {name: portal}\n---\nkind: System\nmetadata: {name: portal}\n",
		"two namespaces": "kind: Component\nmetadata: {name: x, namespace: a}\n---\nkind: Component\nmetadata: {name: x, namespace: b}\n",
	} {
		if twice, err := heldTwice([]byte(catalog)); err != nil || len(twice) != 0 {
			t.Errorf("%s: two refs were taken for one: %v %v", name, twice, err)
		}
	}
	for name, catalog := range map[string]string{
		"one ref twice": "kind: Component\nmetadata: {name: x, namespace: a}\n---\nkind: Component\nmetadata: {name: x, namespace: a}\n",
		"cased apart":   "kind: Component\nmetadata: {name: Portal}\n---\nkind: component\nmetadata: {name: portal}\n",
	} {
		if twice, err := heldTwice([]byte(catalog)); err != nil || len(twice) != 1 {
			t.Errorf("%s: found %v %v", name, twice, err)
		}
	}
}

// heldTwice says each ref more than one entity of the files has.
func heldTwice(files ...[]byte) ([]string, error) {
	count := map[string]int{}
	for _, content := range files {
		entities, err := entitiesIn(content)
		if err != nil {
			return nil, err
		}
		for _, described := range entities {
			ref := entity.New(strings.ToLower(described.Kind), strings.ToLower(described.Metadata.Namespace), strings.ToLower(described.Metadata.Name)).String()
			count[ref]++
		}
	}
	var out []string
	for _, ref := range slices.Sorted(maps.Keys(count)) {
		if count[ref] > 1 {
			out = append(out, ref)
		}
	}
	return out, nil
}

// TestThisRepositorysRootLocationListsEveryCatalogFile holds the Location
// Backstage imports this repository by to the catalog files there are:
// each other one, once, and nothing else. Frontseat finds catalog files
// by their name and follows no Location, so nothing else would notice a
// file it leaves out.
func TestThisRepositorysRootLocationListsEveryCatalogFile(t *testing.T) {
	root := repositoryRoot(t)
	content, err := os.ReadFile(filepath.Join(root, "catalog-info.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	targets, err := locationTargets(content)
	if err != nil {
		t.Fatal(err)
	}
	if wrong := unlisted(catalogFiles(t, root), targets); len(wrong) > 0 {
		t.Errorf("the root Location does not list the catalog files there are: %s", strings.Join(wrong, "; "))
	}
}

// TestAnUnlistedCatalogFileIsFound is the check's control: a file left
// out, one listed that is not there, one listed twice, the root listing
// itself and a Location that says its type are each found, and a
// complete listing passes.
func TestAnUnlistedCatalogFileIsFound(t *testing.T) {
	files := []string{"catalog-info.yaml", "a/catalog-info.yaml", "b/catalog-info.yaml"}
	if wrong := unlisted(files, []string{"./a/catalog-info.yaml", "./b/catalog-info.yaml"}); len(wrong) != 0 {
		t.Errorf("a complete listing was refused: %v", wrong)
	}
	for name, targets := range map[string][]string{
		"one left out":     {"./a/catalog-info.yaml"},
		"one not there":    {"./a/catalog-info.yaml", "./b/catalog-info.yaml", "./c/catalog-info.yaml"},
		"one listed twice": {"./a/catalog-info.yaml", "./b/catalog-info.yaml", "./a/catalog-info.yaml"},
		"the root listed":  {"./catalog-info.yaml", "./a/catalog-info.yaml", "./b/catalog-info.yaml"},
		"one not relative": {"a/catalog-info.yaml", "./b/catalog-info.yaml"},
	} {
		if wrong := unlisted(files, targets); len(wrong) == 0 {
			t.Errorf("%s: the listing %v was taken", name, targets)
		}
	}
	if _, err := locationTargets([]byte("kind: System\nmetadata: {name: s}\n")); err == nil {
		t.Error("a root file with no Location was taken")
	}
	if _, err := locationTargets([]byte("kind: Location\nmetadata: {name: l}\nspec: {targets: [./a/catalog-info.yaml]}\n")); err != nil {
		t.Errorf("a Location that says no type was refused: %v", err)
	}
	if _, err := locationTargets([]byte("kind: Location\nmetadata: {name: l}\nspec: {type: url, targets: [./a/catalog-info.yaml]}\n")); err == nil {
		t.Error("a Location that says its type was taken")
	}
}

// TestEveryEntityThisRepositorysCatalogNamesIsInIt holds what one entity
// of this repository's catalog names of another, its domain, its system,
// the component it is part of, the APIs it provides and consumes, what it
// depends on and what depends on it, to entities the catalog holds: the
// graph keeps a reference to an entity that is not there as
// one outside the workspace, so a misspelt one would pass unseen. Owners
// are users and groups, which this repository describes none of.
func TestEveryEntityThisRepositorysCatalogNamesIsInIt(t *testing.T) {
	documents := catalogDocuments(t, repositoryRoot(t))
	missing, err := namedButAbsent(documents...)
	if err != nil {
		t.Fatal(err)
	}
	for _, each := range missing {
		t.Errorf("the catalog names an entity it does not hold: %s", each)
	}
}

// TestAnEntityNamedButAbsentIsFound is the check's control.
func TestAnEntityNamedButAbsentIsFound(t *testing.T) {
	held := `
kind: Component
metadata: {name: a}
spec: {system: s, providesApis: [p], consumesApis: ["api:other/q"], dependsOn: [resource:r]}
---
kind: Component
metadata: {name: b}
spec: {subcomponentOf: a, dependencyOf: [component:a]}
---
kind: System
metadata: {name: s}
spec: {domain: d}
---
kind: Domain
metadata: {name: d}
---
kind: API
metadata: {name: p}
---
kind: API
metadata: {name: q, namespace: other}
---
kind: Resource
metadata: {name: r}
spec: {system: s}
`
	if missing, err := namedButAbsent([]byte(held)); err != nil || len(missing) != 0 {
		t.Errorf("a catalog that holds all it names was refused: %v %v", missing, err)
	}
	for name, catalog := range map[string]string{
		"a system":            strings.Replace(held, "system: s, ", "system: t, ", 1),
		"a provided API":      strings.Replace(held, "providesApis: [p]", "providesApis: [x]", 1),
		"a consumed API":      strings.Replace(held, "api:other/q", "api:q", 1),
		"a resource":          strings.Replace(held, "dependsOn: [resource:r]", "dependsOn: [resource:x]", 1),
		"a resource's system": strings.Replace(held, "spec: {system: s}", "spec: {system: x}", 1),
		"a parent component":  strings.Replace(held, "subcomponentOf: a,", "subcomponentOf: x,", 1),
		"a dependent":         strings.Replace(held, "dependencyOf: [component:a]", "dependencyOf: [component:x]", 1),
		"a domain":            strings.Replace(held, "spec: {domain: d}", "spec: {domain: x}", 1),
	} {
		if missing, err := namedButAbsent([]byte(catalog)); err != nil || len(missing) != 1 {
			t.Errorf("%s named and absent: found %v %v", name, missing, err)
		}
	}
}

// TestEveryAPIsDefinitionIsThere holds each API of this repository's
// catalog to a definition that is a path, from its catalog file's
// directory, to what is in the repository: frontseat's adapter reads no
// $text, so Backstage shows the path as it is written, and a path that
// names nothing would pass unseen.
func TestEveryAPIsDefinitionIsThere(t *testing.T) {
	root := repositoryRoot(t)
	wrong, err := definitionsAbsent(os.DirFS(root), catalogFiles(t, root))
	if err != nil {
		t.Fatal(err)
	}
	for _, each := range wrong {
		t.Errorf("an API's definition is not there: %s", each)
	}
}

// TestAnAPIsAbsentDefinitionIsFound is the check's control: a definition
// that is there, a file or a directory, passes, and one that is not, one
// out of the repository, one written as a substitution and one left out
// are each found.
func TestAnAPIsAbsentDefinitionIsFound(t *testing.T) {
	repository := fstest.MapFS{
		"api/proto/a.proto": {Data: []byte("syntax = \"proto3\";\n")},
		"api/openapi.yaml":  {Data: []byte("openapi: 3.1.0\n")},
	}
	held := func(definition string) fstest.MapFS {
		files := maps.Clone(repository)
		files["api/catalog-info.yaml"] = &fstest.MapFile{Data: []byte("kind: Component\nmetadata: {name: c}\n---\nkind: API\nmetadata: {name: a}\nspec: {type: grpc" + definition + "}\n")}
		return files
	}
	for _, definition := range []string{", definition: proto", ", definition: openapi.yaml", ", definition: ./proto/a.proto"} {
		if wrong, err := definitionsAbsent(held(definition), []string{"api/catalog-info.yaml"}); err != nil || len(wrong) != 0 {
			t.Errorf("%q: a definition that is there was refused: %v %v", definition, wrong, err)
		}
	}
	for name, definition := range map[string]string{
		"a path that is not there":     ", definition: protos",
		"a path out of the repository": ", definition: ../../outside",
		"a substitution":               ", definition: {$text: ./openapi.yaml}",
		"none":                         "",
	} {
		if wrong, err := definitionsAbsent(held(definition), []string{"api/catalog-info.yaml"}); err != nil || len(wrong) != 1 {
			t.Errorf("%s: found %v %v, want it found", name, wrong, err)
		}
	}
}

// definitionsAbsent says of each API the catalog files describe whose
// definition is not a path, from its file's directory, to a file or a
// directory of the repository.
func definitionsAbsent(repository fs.FS, files []string) ([]string, error) {
	var wrong []string
	for _, file := range files {
		content, err := fs.ReadFile(repository, file)
		if err != nil {
			return nil, err
		}
		entities, err := entitiesIn(content)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		for _, entity := range entities {
			if !strings.EqualFold(entity.Kind, "API") {
				continue
			}
			definition := entity.Spec.Definition
			if definition.Kind != yaml.ScalarNode || definition.Tag != "!!str" || definition.Value == "" {
				wrong = append(wrong, fmt.Sprintf("%s: the API %s has no definition written as a path", file, entity.Metadata.Name))
				continue
			}
			at := path.Join(path.Dir(file), definition.Value)
			if !fs.ValidPath(at) {
				wrong = append(wrong, fmt.Sprintf("%s: the API %s's definition %s is not in the repository", file, entity.Metadata.Name, definition.Value))
				continue
			}
			if _, err := fs.Stat(repository, at); err != nil {
				wrong = append(wrong, fmt.Sprintf("%s: the API %s's definition %s is not there", file, entity.Metadata.Name, definition.Value))
			}
		}
	}
	return wrong, nil
}

// catalogFiles are the catalog files of the repository that are not
// fixtures, each by its path from the root, in order: what
// .frontseatignore hides from frontseat is left out here too, as are
// dependencies and hidden directories.
func catalogFiles(t *testing.T, root string) []string {
	t.Helper()
	ignored, err := os.ReadFile(filepath.Join(root, ".frontseatignore"))
	if err != nil {
		t.Fatal(err)
	}
	atRoot, err := hidden(string(ignored))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			if path == root {
				return nil
			}
			if anyDepth[name] || strings.HasPrefix(name, ".") || (filepath.Dir(path) == root && atRoot[name]) {
				return filepath.SkipDir
			}
			return nil
		}
		if name != "catalog-info.yaml" {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(files)
	return files
}

// catalogDocuments are the contents of every catalog file of the
// repository that is not a fixture.
func catalogDocuments(t *testing.T, root string) [][]byte {
	t.Helper()
	var documents [][]byte
	for _, file := range catalogFiles(t, root) {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, content)
	}
	return documents
}

// anyDepth are the directories the walk for catalog files skips wherever
// they are: fixtures and what a package manager installs.
var anyDepth = map[string]bool{"testdata": true, "node_modules": true}

// hidden reads an ignore file into the directories at the root it hides,
// each written as /name/. Any other pattern must hide a directory the
// walk skips at any depth, or the walk would not leave out all it hides.
func hidden(ignore string) (map[string]bool, error) {
	atRoot := map[string]bool{}
	for _, line := range strings.Split(ignore, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if name, anchored := strings.CutPrefix(line, "/"); anchored {
			if name, directory := strings.CutSuffix(name, "/"); directory && name != "" && !strings.ContainsAny(name, "/*?[!") {
				atRoot[name] = true
				continue
			}
		}
		if last := path.Base(strings.TrimSuffix(line, "/")); strings.HasSuffix(line, "/") && !strings.HasPrefix(line, "!") && anyDepth[last] {
			continue
		}
		return nil, fmt.Errorf("the walk for catalog files does not know how to leave out what %q hides", line)
	}
	return atRoot, nil
}

// TestWhatTheIgnoreFileHidesIsLeftOutOfTheCatalogFiles is the control of
// hidden: a directory at the root and a fixture directory are taken, and
// a pattern the walk would not follow is refused.
func TestWhatTheIgnoreFileHidesIsLeftOutOfTheCatalogFiles(t *testing.T) {
	atRoot, err := hidden("# a comment\n\nplugins/*/testdata/\n/target/\n/dist/\n/site/\n")
	if err != nil || !maps.Equal(atRoot, map[string]bool{"target": true, "dist": true, "site": true}) {
		t.Errorf("read %v %v, want target, dist and site", atRoot, err)
	}
	for _, unknown := range []string{"/generated/catalog-info.yaml", "build/", "*.yaml", "!/dist/", "/a/b/", "/"} {
		if _, err := hidden(unknown + "\n"); err == nil {
			t.Errorf("%q was taken", unknown)
		}
	}
	root := t.TempDir()
	for _, file := range []string{"catalog-info.yaml", "a/catalog-info.yaml", "dist/catalog-info.yaml", "site/x/catalog-info.yaml", "a/dist/catalog-info.yaml", "a/testdata/catalog-info.yaml"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(file)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, file), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".frontseatignore"), []byte("/dist/\n/site/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := catalogFiles(t, root), []string{"a/catalog-info.yaml", "a/dist/catalog-info.yaml", "catalog-info.yaml"}; !slices.Equal(got, want) {
		t.Errorf("found %v, want %v", got, want)
	}
}

// locationTargets are the targets of the one Location a file holds,
// which says no type.
func locationTargets(content []byte) ([]string, error) {
	entities, err := entitiesIn(content)
	if err != nil {
		return nil, err
	}
	var found [][]string
	for _, entity := range entities {
		if entity.Kind != "Location" {
			continue
		}
		// Backstage resolves a relative target against the file only where
		// the Location's type is that of the location the file was read
		// by (toAbsoluteUrl in its catalog backend); a type left out is
		// inherited, so the file imports as a url: and as a file: alike.
		if entity.Spec.Type != "" {
			return nil, fmt.Errorf("the Location says its type is %q: left out, it is that of whatever the file is read by", entity.Spec.Type)
		}
		targets := entity.Spec.Targets
		if entity.Spec.Target != "" {
			targets = append([]string{entity.Spec.Target}, targets...)
		}
		found = append(found, targets)
	}
	if len(found) != 1 {
		return nil, fmt.Errorf("the file holds %d Locations, not one", len(found))
	}
	return found[0], nil
}

// unlisted says what is wrong of a root Location's targets, given the
// catalog files there are: each but the root's own is to be listed once,
// written from the root's directory.
func unlisted(files, targets []string) []string {
	var wrong []string
	listed := map[string]int{}
	for _, target := range targets {
		file, relative := strings.CutPrefix(target, "./")
		if !relative {
			wrong = append(wrong, fmt.Sprintf("%s is not written from the root's directory", target))
		}
		listed[file]++
	}
	for _, file := range files {
		switch {
		case file == "catalog-info.yaml" && listed[file] > 0:
			wrong = append(wrong, "the root lists itself")
		case file == "catalog-info.yaml":
		case listed[file] == 0:
			wrong = append(wrong, fmt.Sprintf("%s is not listed", file))
		case listed[file] > 1:
			wrong = append(wrong, fmt.Sprintf("%s is listed %d times", file, listed[file]))
		}
		delete(listed, file)
	}
	for _, file := range slices.Sorted(maps.Keys(listed)) {
		wrong = append(wrong, fmt.Sprintf("%s is listed and is no catalog file", file))
	}
	return wrong
}

// namedButAbsent are the references of the catalog files' entities to a
// domain, a system, an API, a component or a resource the files do not
// describe,
// each read as Backstage reads it: a bare one of the field's own kind and
// of the entity's namespace.
func namedButAbsent(files ...[]byte) ([]string, error) {
	held := map[string]bool{}
	type named struct{ by, ref string }
	var names []named
	for _, content := range files {
		entities, err := entitiesIn(content)
		if err != nil {
			return nil, err
		}
		for _, described := range entities {
			self := entity.New(strings.ToLower(described.Kind), described.Metadata.Namespace, described.Metadata.Name)
			held[self.String()] = true
			read := func(kind string, written ...string) error {
				for _, each := range written {
					if each == "" {
						continue
					}
					if !strings.Contains(each, ":") {
						each = kind + ":" + each
					}
					ref, err := entity.ParseIn(each, self.Namespace)
					if err != nil {
						return err
					}
					if slices.Contains([]string{"domain", "system", "api", "component", "resource"}, ref.Kind) {
						names = append(names, named{self.String(), ref.String()})
					}
				}
				return nil
			}
			for _, err := range []error{
				read("system", described.Spec.System),
				read("domain", described.Spec.Domain),
				read("component", described.Spec.SubcomponentOf),
				read("api", described.Spec.ProvidesApis...),
				read("api", described.Spec.ConsumesApis...),
				read("component", described.Spec.DependsOn...),
				read("component", described.Spec.DependencyOf...),
			} {
				if err != nil {
					return nil, err
				}
			}
		}
	}
	var missing []string
	for _, each := range names {
		if !held[each.ref] {
			missing = append(missing, fmt.Sprintf("%s names %s", each.by, each.ref))
		}
	}
	return missing, nil
}

// componentsOf reads the components of every catalog file of the
// repository that is not a fixture, and the components each depends on.
func componentsOf(t *testing.T, root string) map[string][]string {
	t.Helper()
	on := map[string][]string{}
	for _, file := range catalogFiles(t, root) {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			t.Fatal(err)
		}
		if err := componentsIn(content, on); err != nil {
			t.Fatal(err)
		}
	}
	return on
}

// componentsIn reads the components of one catalog file into on, each
// by its reference, with those it depends on, read as the catalog reads a
// reference: a namespace left out is that of the entity whose file it is.
func componentsIn(content []byte, on map[string][]string) error {
	entities, err := entitiesIn(content)
	if err != nil {
		return err
	}
	for _, described := range entities {
		if !strings.EqualFold(described.Kind, "Component") {
			continue
		}
		self := entity.New(entity.KindComponent, described.Metadata.Namespace, described.Metadata.Name)
		var needs []string
		for _, written := range described.Spec.DependsOn {
			ref, err := entity.ParseIn(written, self.Namespace)
			if err != nil {
				return err
			}
			if ref.Kind == entity.KindComponent {
				needs = append(needs, ref.String())
			}
		}
		on[self.String()] = needs
	}
	return nil
}

// catalogEntity is an entity of a catalog file, as far as these checks
// read one: its kind and name, and what its spec says of where it belongs,
// what it names and, for an API or a Location, what it points at.
type catalogEntity struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec struct {
		Type           string    `yaml:"type"`
		System         string    `yaml:"system"`
		Domain         string    `yaml:"domain"`
		SubcomponentOf string    `yaml:"subcomponentOf"`
		ProvidesApis   []string  `yaml:"providesApis"`
		ConsumesApis   []string  `yaml:"consumesApis"`
		DependsOn      []string  `yaml:"dependsOn"`
		DependencyOf   []string  `yaml:"dependencyOf"`
		Definition     yaml.Node `yaml:"definition"`
		Target         string    `yaml:"target"`
		Targets        []string  `yaml:"targets"`
	} `yaml:"spec"`
}

// entitiesIn reads every entity of one catalog file, in its order.
func entitiesIn(content []byte) ([]catalogEntity, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	var entities []catalogEntity
	for {
		var entity catalogEntity
		if err := decoder.Decode(&entity); errors.Is(err, io.EOF) {
			return entities, nil
		} else if err != nil {
			return nil, err
		}
		entities = append(entities, entity)
	}
}

// cycleIn returns one cycle of the graph, its first component repeated
// at its end, or nil where there is none.
func cycleIn(on map[string][]string) []string {
	const (
		unseen = iota
		open
		done
	)
	state := map[string]int{}
	var path []string
	var visit func(string) []string
	visit = func(name string) []string {
		switch state[name] {
		case open:
			return append(slices.Clone(path[slices.Index(path, name):]), name)
		case done:
			return nil
		}
		state[name] = open
		path = append(path, name)
		for _, next := range on[name] {
			if cycle := visit(next); cycle != nil {
				return cycle
			}
		}
		path = path[:len(path)-1]
		state[name] = done
		return nil
	}
	names := make([]string, 0, len(on))
	for name := range on {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if cycle := visit(name); cycle != nil {
			return cycle
		}
	}
	return nil
}

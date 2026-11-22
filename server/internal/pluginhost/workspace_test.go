package pluginhost_test

import (
	"strings"
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/pluginhosttest"
)

// packageAt is a project of a package beneath a workspace root, as a
// Cargo plugin would answer one, naming the files above it that cargo reads.
func packageAt(root, into string, workspaceFiles ...string) *pluginv1.AnalyzeResponse {
	return &pluginv1.AnalyzeResponse{Analyses: []*pluginv1.Analysis{{Root: root, Projects: []*pluginv1.Project{{
		Root: root, Kind: "cargo-package", Name: "hello", Manifest: root + "/Cargo.toml",
		WorkspaceFiles: workspaceFiles,
		Actions:        []*pluginv1.Action{{Name: "compile", Command: &pluginv1.Command{Arguments: []string{"cargo", "build"}}, Inputs: append([]*pluginv1.Input{pluginhost.SourceInput("**")}, pluginhosttest.AllDependencies(into)), Tools: []string{"rust"}, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}}}},
	}}}}}
}

// A project may name files above it that its tool reads for the whole
// workspace: literal paths of the repository, not of the project.
func TestWorkspaceFilesAreLiteralFilesAboveTheProject(t *testing.T) {
	if err := pluginhost.CheckAnalysis("cargo", []string{"hello"}, packageAt("hello", "{root}", "Cargo.lock", ".cargo/config.toml")); err != nil {
		t.Fatalf("sound workspace files were refused: %v", err)
	}
	for name, refused := range map[string][]string{
		"beneath the project":    {"hello/Cargo.toml"},
		"the project itself":     {"hello"},
		"a pattern":              {"*.lock"},
		"leaving the repository": {"../Cargo.lock"},
		"a full path":            {"/Cargo.lock"},
		"nothing":                {""},
		"twice":                  {"Cargo.lock", "./Cargo.lock"},
	} {
		if err := pluginhost.CheckAnalysis("cargo", []string{"hello"}, packageAt("hello", "{root}", refused...)); err == nil || !strings.Contains(err.Error(), "workspace") {
			t.Errorf("%s was accepted as a workspace file, or refused without saying so: %v", name, err)
		}
	}
	// A project at the repository's root has nothing above it.
	if err := pluginhost.CheckAnalysis("cargo", []string{""}, packageAt("", "", "Cargo.lock")); err == nil {
		t.Error("a root project was allowed a workspace file above it")
	}
}

// What is handed may be placed at its own root in the repository, for a
// tool that finds a dependency by the path its manifest names.
func TestAHandedDependencyMayBePlacedAtItsOwnRoot(t *testing.T) {
	if err := pluginhost.CheckAnalysis("cargo", []string{"hello"}, packageAt("hello", "{root}")); err != nil {
		t.Fatalf("{root} was refused: %v", err)
	}
	for _, refused := range []string{"{root}/x", "../{root}", "{root}{name}"} {
		if err := pluginhost.CheckAnalysis("cargo", []string{"hello"}, packageAt("hello", refused)); err == nil {
			t.Errorf("%q was accepted as a placement", refused)
		}
	}
	into, err := pluginhost.Placed("{root}", "greet", "", "", "crates/hello", "crates/greet")
	if err != nil || into != "../greet" {
		t.Errorf("placed at %q (%v), want ../greet", into, err)
	}
	into, err = pluginhost.Placed(".frontseat/go/{name}", "example.com/dep", "", "", "app", "lib")
	if err != nil || into != ".frontseat/go/example.com/dep" {
		t.Errorf("placed at %q (%v), want the named directory", into, err)
	}
	// A path is found only within one repository.
	if _, err := pluginhost.Placed("{root}", "greet", "a", "b", "hello", "greet"); err == nil {
		t.Error("a dependency of another repository was placed by its root")
	}
	if _, err := pluginhost.Placed("{root}", "hello", "", "", "hello", "hello"); err == nil {
		t.Error("a project was placed over itself")
	}
}

// A file an Arrange writes may sit directly above its project, for the
// workspace, but never outside the repository, never in place of the
// project, and never in a directory beside it, which may be another's.
func TestAnArrangedFileMayBeAboveItsProject(t *testing.T) {
	arranged := func(paths ...string) *pluginv1.ArrangeResponse {
		var files []*pluginv1.File
		for _, p := range paths {
			files = append(files, &pluginv1.File{Path: p, Content: []byte("x")})
		}
		return &pluginv1.ArrangeResponse{Files: files}
	}
	if err := pluginhost.CheckArrangement("cargo", "crates/hello", arranged("Cargo.toml", "crates/hello/.cargo/config.toml")); err != nil {
		t.Fatalf("a workspace manifest above the project was refused: %v", err)
	}
	for _, refused := range []string{"../Cargo.toml", "crates/hello", "/Cargo.toml", "", "crates/greet/Cargo.toml", "other/Cargo.toml", "crates/hello-sibling/x"} {
		if err := pluginhost.CheckArrangement("cargo", "crates/hello", arranged(refused)); err == nil {
			t.Errorf("%q was accepted as an arranged file", refused)
		}
	}
}

// A reconciliation edits only the files it was asked about, at plain
// keys, with plain strings, each place once.
func TestAReconciliationIsHeldToTheFilesAsked(t *testing.T) {
	request := &pluginv1.ReconcileRequest{Root: "app", Entities: []*pluginv1.Resolved{{Manifest: "app/catalog-info.yaml"}}}
	edit := func(path string, document int32, keys []string, values ...string) *pluginv1.ReconcileResponse {
		return &pluginv1.ReconcileResponse{Edits: []*pluginv1.Edit{{Path: path, Document: document, Keys: keys, Values: values}}}
	}
	if err := pluginhost.CheckReconciliation("backstage", request, edit("app/catalog-info.yaml", 1, []string{"spec", "dependsOn"}, "component:default/a")); err != nil {
		t.Fatalf("a sound edit was refused: %v", err)
	}
	if err := pluginhost.CheckReconciliation("backstage", request, edit("app/catalog-info.yaml", 0, []string{"spec", "dependsOn"})); err != nil {
		t.Fatalf("an edit that takes a key out was refused: %v", err)
	}
	for name, refused := range map[string]*pluginv1.ReconcileResponse{
		"another file":         edit("other/catalog-info.yaml", 0, []string{"spec"}, "x"),
		"negative document":    edit("app/catalog-info.yaml", -1, []string{"spec"}, "x"),
		"no keys":              edit("app/catalog-info.yaml", 0, nil, "x"),
		"an empty key":         edit("app/catalog-info.yaml", 0, []string{"spec", ""}, "x"),
		"a control in a key":   edit("app/catalog-info.yaml", 0, []string{"spec\n"}, "x"),
		"a value of two lines": edit("app/catalog-info.yaml", 0, []string{"spec"}, "a\nb"),
		"one place twice":      {Edits: []*pluginv1.Edit{{Path: "app/catalog-info.yaml", Keys: []string{"spec"}, Values: []string{"a"}}, {Path: "app/catalog-info.yaml", Keys: []string{"spec"}, Values: []string{"b"}}}},
	} {
		if err := pluginhost.CheckReconciliation("backstage", request, refused); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

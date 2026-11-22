package evaluate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/engine/catalog"
	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	"github.com/frontseat-dev/frontseat/server/internal/engine/graph"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/source/record"
)

// Populate branches the fixed Go/Backstage integration fixture does not emit.
// This is an ownership unit fixture, not proof of generated/service execution.
func ownedFixture(t *testing.T) Evaluation {
	t.Helper()
	ref := entity.New("component", "default", "root")
	other := entity.New("component", "default", "other")
	label := model.Label{Path: "pkg", Plugin: "go", Name: "build"}
	selection := []pattern.Selection{{{"**/*.go"}, {"!vendor/**"}}}
	ctx := &pluginv1.Context{Tools: map[string]string{"go": "pinned"}, Options: map[string]*typev1.OptionValue{"tags": {Value: &typev1.OptionValue_StringListValue{StringListValue: &typev1.OptionValue_StringList{Values: []string{"tag"}}}}}}
	generated := plan.GeneratedSource{Directory: "pkg", Patterns: []string{"*.pb.go"}, Producer: label}
	action := plan.Action{
		Label: label, Repository: "", ProjectRoot: "pkg",
		Declared: &pluginv1.Action{Name: "build", Command: &pluginv1.Command{Arguments: []string{"tool"}, Environment: map[string]string{"A": "original"}}, Kind: &pluginv1.Action_SharedFetch{SharedFetch: &pluginv1.SharedFetch{Files: []*pluginv1.File{{Path: "config", Content: []byte("payload")}}}}}, Context: ctx,
		Sources: []pattern.Group{{Directory: "pkg", Patterns: []string{"*.go"}, Generated: selection}}, IdleWithout: []plan.Read{{From: label, Output: "out"}}, Environment: map[string]string{"GIVEN": "original"}, References: []plan.Reference{{Reference: model.Reference{Path: "pkg", Rest: "build", Written: ":build"}}}, Reads: []plan.Read{{From: label, Output: "out"}},
		Needs: []model.Label{label}, ArtifactOnly: []model.Label{label}, Artifacts: []plan.ArtifactInput{{Source: generated, Patterns: []string{"*.go"}, Generated: selection}},
		GeneratedSources: []plan.GeneratedSource{generated}, WorkspaceFiles: []string{"go.work"}, Handed: []plan.Handed{{Patterns: []string{"*.go"}, Generated: selection, Artifacts: []plan.Read{{From: label, Output: "install", Optional: true}}}}, Arranged: []plan.Arranged{{Path: "go.work", Content: []byte("workspace")}},
		InStep: &plan.InStep{File: "catalog-info.yaml", Fixed: []byte("rewrite"), Entities: []plan.Described{{Entity: ref, Says: []entity.Ref{other}, Should: []entity.Ref{ref}}}},
		Beside: &plan.Beside{Sandbox: []byte("sandbox"), Environment: map[string]string{"URL": "original"}, TestNeeds: []model.Label{label}, Programs: map[string]plan.Action{
			"server": {Label: label, Context: ctx, Declared: &pluginv1.Action{Command: &pluginv1.Command{Arguments: []string{"server"}}, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}}}, Sources: []pattern.Group{{Patterns: []string{"config"}}}, Handed: []plan.Handed{{Artifacts: []plan.Read{{From: label, Output: "program"}}}}, Arranged: []plan.Arranged{{Path: "config", Content: []byte("service")}}},
		}},
	}
	projects := []graph.Project{{Root: "pkg", Plugin: "go", Name: "root", Platforms: []string{"linux/amd64"}, Options: []config.Option{{Name: "tags", Value: ctx.Options["tags"]}}, Provides: []graph.Provide{{Coordinate: model.Coordinate{Ecosystem: "go", Name: "root"}, Sources: []string{"*.go"}, Generated: selection, Artifacts: []graph.Produced{{Action: "build", Output: "install", Optional: true}}}}, Requires: []graph.Requirement{{Coordinate: model.Coordinate{Ecosystem: "go", Name: "other"}}}}}
	provider, err := graph.Resolve(projects)
	if err != nil {
		t.Fatal(err)
	}
	descriptors := []catalog.Descriptor{{Entity: catalog.Entity{Ref: ref, Manifest: "catalog-info.yaml", Roots: []string{"pkg"}}, Relations: []catalog.DeclaredRelation{{Type: "partOf", Target: other}}, Context: ctx}}
	catalogGraph, err := catalog.Assemble(nil, descriptors)
	if err != nil {
		t.Fatal(err)
	}
	return Evaluation{Actions: []plan.Action{action}, Projects: projects, Generated: []plan.GeneratedSource{generated}, producerReads: []observedGeneration{{repository: "", plugin: "generator", reads: []pluginhost.Read{{Import: "read", Argument: "manifest", Found: true}}}}, Entities: []record.Located{{Ref: ref, Root: "pkg"}}, Catalog: catalogGraph, descriptors: descriptors, Graph: provider, Releases: []config.Release{{Entity: ref, Settings: map[string]string{"initial": "0.1.0"}}}, Listed: map[string][]string{"": {"go"}}, DependsOn: map[entity.Ref][]entity.Ref{ref: {other}}, shared: map[model.Label]sharedAt{label: {index: 0, root: "pkg"}}}
}

func ownedFingerprint(t *testing.T, e Evaluation) []byte {
	t.Helper()
	semantic, err := retainedSemantic(e)
	if err != nil {
		t.Fatal(err)
	}
	provider, found := e.Graph.ProvidedBy(model.Coordinate{Ecosystem: "go", Name: "root"})
	if !found {
		t.Fatal("provider fixture missing")
	}
	var reads [][]pluginhost.Read
	for _, r := range e.producerReads {
		reads = append(reads, r.reads)
	}
	shared := map[string]string{}
	for label, v := range e.shared {
		shared[label.String()] = fmt.Sprintf("%d:%s", v.index, v.root)
	}
	wire, err := json.Marshal(struct {
		Semantic    []byte
		Provider    graph.Project
		Descriptors []catalog.Descriptor
		Reads       [][]pluginhost.Read
		Shared      map[string]string
	}{semantic, provider, e.descriptors, reads, shared})
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func TestRetainedOwnedNestedPayloadsDoNotAlias(t *testing.T) {
	mutations := map[string]func(*Evaluation){
		"command map":                func(e *Evaluation) { e.Actions[0].Declared.Command.Environment["A"] = "changed" },
		"declared file bytes":        func(e *Evaluation) { e.Actions[0].Declared.GetSharedFetch().Files[0].Content[0] = 'X' },
		"context nested option":      func(e *Evaluation) { e.Actions[0].Context.Options["tags"].GetStringListValue().Values[0] = "changed" },
		"references":                 func(e *Evaluation) { e.Actions[0].References[0].Rest = "changed" },
		"reads":                      func(e *Evaluation) { e.Actions[0].Reads[0].Output = "changed" },
		"source patterns":            func(e *Evaluation) { e.Actions[0].Sources[0].Patterns[0] = "changed" },
		"nested generated input":     func(e *Evaluation) { e.Actions[0].Sources[0].Generated[0][1][0] = "changed" },
		"idle selection":             func(e *Evaluation) { e.Actions[0].IdleWithout[0].Output = "changed" },
		"runtime environment":        func(e *Evaluation) { e.Actions[0].Environment["GIVEN"] = "changed" },
		"artifact source":            func(e *Evaluation) { e.Actions[0].Artifacts[0].Source.Patterns[0] = "changed" },
		"artifact selection":         func(e *Evaluation) { e.Actions[0].Artifacts[0].Generated[0][0][0] = "changed" },
		"artifact patterns":          func(e *Evaluation) { e.Actions[0].Artifacts[0].Patterns[0] = "changed" },
		"action generated authority": func(e *Evaluation) { e.Actions[0].GeneratedSources[0].Patterns[0] = "changed" },
		"workspace files":            func(e *Evaluation) { e.Actions[0].WorkspaceFiles[0] = "changed" },
		"handed patterns":            func(e *Evaluation) { e.Actions[0].Handed[0].Patterns[0] = "changed" },
		"handed generated scopes":    func(e *Evaluation) { e.Actions[0].Handed[0].Generated[0][1][0] = "changed" },
		"handed artifacts":           func(e *Evaluation) { e.Actions[0].Handed[0].Artifacts[0].Optional = false },
		"arranged bytes":             func(e *Evaluation) { e.Actions[0].Arranged[0].Content[0] = 'X' },
		"rewrite bytes":              func(e *Evaluation) { e.Actions[0].InStep.Fixed[0] = 'X' },
		"rewrite says":               func(e *Evaluation) { e.Actions[0].InStep.Entities[0].Says[0].Name = "changed" },
		"rewrite should":             func(e *Evaluation) { e.Actions[0].InStep.Entities[0].Should[0].Name = "changed" },
		"sandbox bytes":              func(e *Evaluation) { e.Actions[0].Beside.Sandbox[0] = 'X' },
		"service exports":            func(e *Evaluation) { e.Actions[0].Beside.Environment["URL"] = "changed" },
		"service test inputs":        func(e *Evaluation) { e.Actions[0].Beside.TestNeeds[0].Name = "changed" },
		"service program map":        func(e *Evaluation) { delete(e.Actions[0].Beside.Programs, "server") },
		"service program command":    func(e *Evaluation) { e.Actions[0].Beside.Programs["server"].Declared.Command.Arguments[0] = "changed" },
		"service program context":    func(e *Evaluation) { e.Actions[0].Beside.Programs["server"].Context.Tools["go"] = "changed" },
		"service program inputs":     func(e *Evaluation) { e.Actions[0].Beside.Programs["server"].Sources[0].Patterns[0] = "changed" },
		"service program artifacts":  func(e *Evaluation) { e.Actions[0].Beside.Programs["server"].Handed[0].Artifacts[0].Output = "changed" },
		"service program files":      func(e *Evaluation) { e.Actions[0].Beside.Programs["server"].Arranged[0].Content[0] = 'X' },
		"provider artifacts":         func(e *Evaluation) { e.Projects[0].Provides[0].Artifacts[0].Output = "changed" },
		"provider graph artifacts": func(e *Evaluation) {
			p, _ := e.Graph.ProvidedBy(model.Coordinate{Ecosystem: "go", Name: "root"})
			p.Provides[0].Artifacts[0].Output = "changed"
		},
		"provider generated scopes": func(e *Evaluation) { e.Projects[0].Provides[0].Generated[0][1][0] = "changed" },
		"provider graph scopes": func(e *Evaluation) {
			p, _ := e.Graph.ProvidedBy(model.Coordinate{Ecosystem: "go", Name: "root"})
			p.Provides[0].Generated[0][1][0] = "changed"
		},
		"project platforms": func(e *Evaluation) { e.Projects[0].Platforms[0] = "changed" },
		"provider graph platforms": func(e *Evaluation) {
			p, _ := e.Graph.ProvidedBy(model.Coordinate{Ecosystem: "go", Name: "root"})
			p.Platforms[0] = "changed"
		},
		"provider graph option": func(e *Evaluation) {
			p, _ := e.Graph.ProvidedBy(model.Coordinate{Ecosystem: "go", Name: "root"})
			p.Options[0].Value.GetStringListValue().Values[0] = "changed"
		},
		"generated authority":         func(e *Evaluation) { e.Generated[0].Patterns[0] = "changed" },
		"private producer reads":      func(e *Evaluation) { e.producerReads[0].reads[0].Argument = "changed" },
		"catalog entity accessor":     func(e *Evaluation) { e.Catalog.Entities()[0].Roots[0] = "changed" },
		"private descriptor roots":    func(e *Evaluation) { e.descriptors[0].Roots[0] = "changed" },
		"private descriptor relation": func(e *Evaluation) { e.descriptors[0].Relations[0].Type = "changed" },
		"private descriptor context":  func(e *Evaluation) { e.descriptors[0].Context.Tools["go"] = "changed" },
		"release settings":            func(e *Evaluation) { e.Releases[0].Settings["initial"] = "changed" },
		"dependency values": func(e *Evaluation) {
			for ref := range e.DependsOn {
				e.DependsOn[ref][0].Name = "changed"
			}
		},
		"private shared map": func(e *Evaluation) { clear(e.shared) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			original := ownedFixture(t)
			before := ownedFingerprint(t, original)
			copy := original.Clone()
			if !bytes.Equal(before, ownedFingerprint(t, copy)) {
				t.Fatal("clone changed value before mutation")
			}
			mutate(&copy)
			if bytes.Equal(before, ownedFingerprint(t, copy)) {
				t.Fatal("mutation control did not change the observed copy")
			}
			if !bytes.Equal(before, ownedFingerprint(t, original)) {
				t.Fatal("mutation reached retained original")
			}
		})
	}
}

// When a new mutable field is introduced, the ownership test inventory must
// be reconsidered. Reflection is test-only; it is not part of the copier.
func TestRetainedOwnedMutableFieldInventory(t *testing.T) {
	cases := []struct {
		value  any
		fields []string
	}{
		{Evaluation{}, []string{"Actions", "Projects", "Generated", "producerReads", "Entities", "Catalog", "descriptors", "Graph", "Releases", "Listed", "DependsOn", "shared"}},
		{plan.Action{}, []string{"Declared", "Context", "Sources", "References", "Reads", "IdleWithout", "Needs", "ArtifactOnly", "Artifacts", "GeneratedSources", "WorkspaceFiles", "Handed", "Arranged", "Environment", "InStep", "Beside"}},
		{pattern.Group{}, []string{"Patterns", "Generated"}},
		{graph.Project{}, []string{"Options", "Platforms", "Provides", "Requires"}},
		{graph.Graph{}, []string{"projects", "provider"}},
		{graph.Provide{}, []string{"Sources", "Generated", "Artifacts"}},
		{graph.Produced{}, nil},
		{graph.Requirement{}, nil},
		{catalog.Graph{}, []string{"nodes", "declared", "ordered"}},
		{catalog.Node{}, []string{"Entity", "Relations"}},
		{catalog.Entity{}, []string{"Roots"}},
		{catalog.Descriptor{}, []string{"Entity", "Relations", "Context"}},
		{catalog.DeclaredRelation{}, nil},
		{catalog.Relation{}, nil},
		{plan.ArtifactInput{}, []string{"Source", "Patterns", "Generated"}},
		{plan.GeneratedSource{}, []string{"Patterns"}},
		{plan.Handed{}, []string{"Patterns", "Generated", "Artifacts"}},
		{plan.Arranged{}, []string{"Content"}},
		{plan.InStep{}, []string{"Entities", "Fixed"}},
		{plan.Described{}, []string{"Says", "Should"}},
		{plan.Beside{}, []string{"Sandbox", "Environment", "Programs", "TestNeeds"}},
		{plan.Reference{}, nil},
		{plan.Read{}, nil},
		{config.Release{}, []string{"Settings"}},
		{config.Option{}, []string{"Value"}},
		{record.Located{}, nil},
		{pluginhost.Read{}, nil},
		{observedGeneration{}, []string{"reads"}},
		{sharedAt{}, nil},
		{model.Reference{}, nil},
		{model.Label{}, nil},
		{model.Coordinate{}, nil},
		{entity.Ref{}, nil},
	}
	var mutable func(reflect.Type) bool
	mutable = func(typ reflect.Type) bool {
		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface, reflect.Func, reflect.Chan:
			return true
		case reflect.Struct:
			for i := 0; i < typ.NumField(); i++ {
				if mutable(typ.Field(i).Type) {
					return true
				}
			}
		case reflect.Array:
			return mutable(typ.Elem())
		}
		return false
	}
	for _, c := range cases {
		typ := reflect.TypeOf(c.value)
		var got []string
		for i := 0; i < typ.NumField(); i++ {
			if mutable(typ.Field(i).Type) {
				got = append(got, typ.Field(i).Name)
			}
		}
		if !slices.Equal(got, c.fields) {
			t.Errorf("%s mutable fields changed: %v; review ownership inventory %v", typ, got, c.fields)
		}
	}
}

func TestRetainedOwnedConcurrentMutationOfEarlierReturn(t *testing.T) {
	root := retainedFixture(t, 3)
	p, files := newRetainedEngine(), &source.Keeper{}
	profile := typev1.Profile_PROFILE_DEVELOPMENT
	first := retainedOracle(t, p, files, root, profile, nil)
	want, err := retainedSemantic(first.evaluation)
	if err != nil {
		t.Fatal(err)
	}
	started, stop := make(chan struct{}), make(chan struct{})
	var wait sync.WaitGroup
	wait.Go(func() {
		close(started)
		for {
			select {
			case <-stop:
				return
			default:
			}
			first.actions[0].Declared.Name = "caller mutation"
			first.evaluation.Projects[0].Provides[0].Sources[0] = "caller mutation"
			first.evaluation.Listed[""][0] = "caller mutation"
			for _, a := range first.actions {
				if a.Context != nil {
					a.Context.Tools["go"] = "caller mutation"
				}
			}
		}
	})
	defer func() { close(stop); wait.Wait() }()
	<-started
	for range 12 {
		got, err := p.run(t.Context(), retainedTake(t, files, root), profile, nil)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := retainedSemantic(got.evaluation)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(want, actual) {
			t.Fatal("earlier caller changed a concurrently copied later result")
		}
	}
}

type ownedTransient struct {
	Source
	path    string
	failure error
	once    atomic.Bool
}

func (s *ownedTransient) Digest(ctx context.Context, repo, path string) (string, bool, error) {
	if path == s.path && s.once.CompareAndSwap(false, true) {
		return "", false, s.failure
	}
	return s.Source.Digest(ctx, repo, path)
}

func TestRetainedOwnedTransientPreflightErrorsAreNotCached(t *testing.T) {
	for _, path := range []string{"frontseat.yaml", "m0000/main.go"} {
		for _, failure := range []error{errors.New("transient source refusal"), context.Canceled, context.DeadlineExceeded} {
			root, p, files := warmRetained(t, 1)
			profile := typev1.Profile_PROFILE_DEVELOPMENT
			input := &ownedTransient{Source: retainedTake(t, files, root), path: path, failure: failure}
			if _, err := p.run(t.Context(), input, profile, nil); err != nil {
				t.Fatalf("live context retained speculative error for %s: %v", path, err)
			}
			if !input.once.Load() || p.evaluations != 2 {
				t.Fatal("control did not exercise authoritative fallback")
			}
			retainedOracle(t, p, files, root, profile, nil)
			if p.evaluations != 2 {
				t.Fatal("transient refusal poisoned later reuse")
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := p.run(ctx, retainedTake(t, files, root), profile, nil); !errors.Is(err, context.Canceled) {
				t.Fatalf("actual cancellation: %v", err)
			}
			if p.evaluations != 2 {
				t.Fatal("actual cancellation entered ordinary fallback")
			}
		}
	}
}

func TestRetainedOwnedPluginAndStructuralDiagnosticsUseFreshControl(t *testing.T) {
	for _, structural := range []bool{false, true} {
		root, p, files := warmRetained(t, 2)
		profile := typev1.Profile_PROFILE_DEVELOPMENT
		retainedWrite(t, root, "m0000/go.mod", "not a valid module\n")
		retainedWrite(t, root, "m0001/go.mod", "also not a valid module\n")
		if structural {
			retainedWrite(t, root, "frontseat.yaml", "unknown: refused\n")
		}
		input := hintsOrderSource{Source: retainedTake(t, files, root), hint: func(_, path string) bool { return path == "m0001/go.mod" }}
		_, got := p.run(t.Context(), input, profile, nil)
		_, want := retainedFresh(t.Context(), retainedTake(t, &source.Keeper{}, root), profile, nil, &Answers{})
		if got == nil || want == nil || got.Error() != want.Error() {
			t.Fatalf("structural=%v retained=%v fresh=%v", structural, got, want)
		}
		// What was kept is of the source before, and answers nothing of
		// this one: asked again, it is refused as it was.
		if _, again := p.run(t.Context(), input, profile, nil); again == nil || again.Error() != want.Error() {
			t.Fatalf("a refused source was answered from what was kept: %v", again)
		}
	}
}

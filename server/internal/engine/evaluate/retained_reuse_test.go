package evaluate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/testsupport/testmodule"
	"github.com/frontseat-dev/frontseat/server/testsupport/testpins"
)

// retainedLoaded is the Go and Backstage plugins these tests evaluate with,
// compiled once.
var retainedLoaded = sync.OnceValues(func() ([]*pluginhost.Plugin, error) {
	host, err := pluginhost.New(context.Background())
	if err != nil {
		return nil, err
	}
	var plugins []*pluginhost.Plugin
	for _, name := range []string{"go", "backstage"} {
		data, err := testmodule.Plugin(name)
		if err != nil {
			return nil, err
		}
		p, err := host.Load(context.Background(), data)
		if err != nil {
			return nil, err
		}
		plugins = append(plugins, p)
	}
	return plugins, nil
})

// retainedPlugins is what retainedLoaded loads; a plugin that does not
// load fails the test.
func retainedPlugins(t testing.TB) []*pluginhost.Plugin {
	t.Helper()
	plugins, err := retainedLoaded()
	if err != nil {
		t.Fatal(err)
	}
	return plugins
}

func retainedWrite(t testing.TB, root, name, value string) {
	t.Helper()
	at := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(at), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(at, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}

func retainedFixture(t testing.TB, modules int) string {
	t.Helper()
	root := t.TempDir()
	testpins.Go(t, root)
	for i := range modules {
		name := fmt.Sprintf("m%04d", i)
		mod := "module example.com/" + name + "\n\ngo 1.27\n"
		if i%10 != 0 {
			mod += fmt.Sprintf("\nrequire example.com/m%04d v0.0.0\n", i-i%10)
		}
		retainedWrite(t, root, name+"/go.mod", mod)
		retainedWrite(t, root, name+"/main.go", "package main\nfunc main() {}\n")
	}
	return root
}

// The reference deliberately uses a fresh evaluator answer cache when requested.
func retainedFresh(ctx context.Context, src Source, profile pluginv1.Profile, targets []string, answers *Answers) (retainedResult, error) {
	plugins, err := retainedLoaded()
	if err != nil {
		return retainedResult{}, err
	}
	e, err := Evaluate(ctx, src, plugins, answers, profile, nil)
	if err != nil {
		return retainedResult{}, err
	}
	actions, _, err := plan.Of(e.Actions, e.Entities, targets, nil)
	return retainedResult{e, actions}, err
}

// Semantic comparison is these tests' own, not a persistence format.
// The private Graph is a deterministic index of Projects; Catalog is exposed
// through its complete resolved Nodes. Execution telemetry is not a graph value.
func retainedSemantic(e Evaluation) ([]byte, error) {
	type edge struct {
		From string
		To   []string
	}
	var edges []edge
	for from, targets := range e.DependsOn {
		var to []string
		for _, target := range targets {
			to = append(to, target.String())
		}
		edges = append(edges, edge{from.String(), to})
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].From < edges[j].From })
	nodes := e.Catalog.Nodes(e.DependsOn)
	e.Catalog, e.Graph, e.DependsOn = nil, nil, nil
	e.Asked, e.Again = 0, 0
	return json.Marshal(struct {
		Evaluation Evaluation
		Nodes      any
		Edges      []edge
	}{e, nodes, edges})
}

// retainedSemantics is what retainedSemantic makes of each of two
// evaluations, the one a test got and the one it wants, for it to compare.
func retainedSemantics(t testing.TB, got, want Evaluation) (a, b []byte) {
	t.Helper()
	a, err := retainedSemantic(got)
	if err != nil {
		t.Fatal(err)
	}
	b, err = retainedSemantic(want)
	if err != nil {
		t.Fatal(err)
	}
	return a, b
}

func retainedTake(t testing.TB, files *source.Keeper, root string) *source.Snapshot {
	t.Helper()
	s, err := files.Take(context.Background(), source.Tree{Root: root, Paths: map[string]string{"": "."}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRetainedUnchangedEvaluationIsReused(t *testing.T) {
	root := retainedFixture(t, 2)
	p, files := newRetainedEngine(), &source.Keeper{}
	for range 2 {
		if _, err := p.run(t.Context(), retainedTake(t, files, root), pluginv1.Profile_PROFILE_DEVELOPMENT, nil); err != nil {
			t.Fatal(err)
		}
	}
	if p.evaluations != 1 || p.plans != 1 {
		t.Fatalf("unchanged: evaluated %d, planned %d; want one of each", p.evaluations, p.plans)
	}
}

type retainedRunner interface {
	run(context.Context, Source, pluginv1.Profile, []string) (retainedResult, error)
}

func retainedOracle(t *testing.T, p retainedRunner, files *source.Keeper, root string, profile pluginv1.Profile, targets []string) retainedResult {
	t.Helper()
	return retainedCompare(t, p, files, root, profile, targets, false)
}

// warmRetained is a fixture of modules, and a retained engine that has
// evaluated it once for development, as a fresh evaluation does.
func warmRetained(t *testing.T, modules int) (string, *retainedEngine, *source.Keeper) {
	t.Helper()
	root := retainedFixture(t, modules)
	p, files := newRetainedEngine(), &source.Keeper{}
	retainedOracle(t, p, files, root, pluginv1.Profile_PROFILE_DEVELOPMENT, nil)
	return root, p, files
}

func retainedCompare(t *testing.T, p retainedRunner, files *source.Keeper, root string, profile pluginv1.Profile, targets []string, refusal bool) retainedResult {
	t.Helper()
	got, gotErr := p.run(t.Context(), retainedTake(t, files, root), profile, targets)
	want, wantErr := retainedFresh(t.Context(), retainedTake(t, &source.Keeper{}, root), profile, targets, &Answers{})
	if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
		t.Fatalf("retained error %v; fresh error %v", gotErr, wantErr)
	}
	if refusal != (wantErr != nil) {
		t.Fatalf("expected refusal=%v, fresh result error=%v", refusal, wantErr)
	}
	if wantErr != nil {
		return retainedResult{}
	}
	a, b := retainedSemantics(t, got.evaluation, want.evaluation)
	if !bytes.Equal(a, b) {
		t.Fatalf("retained evaluation differs from fresh\nretained: %s\nfresh: %s", a, b)
	}
	a, err := json.Marshal(got.actions)
	if err != nil {
		t.Fatal(err)
	}
	b, err = json.Marshal(want.actions)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("retained plan differs from fresh")
	}
	return got
}

func TestRetainedChangesAgreeWithFreshEvaluation(t *testing.T) {
	root, p, files := warmRetained(t, 2)
	profile := pluginv1.Profile_PROFILE_DEVELOPMENT
	t.Run("unobserved file", func(t *testing.T) {
		before := p.evaluations
		retainedWrite(t, root, "notes.txt", "not an analysis input\n")
		retainedOracle(t, p, files, root, profile, nil)
		if p.evaluations != before {
			t.Fatal("unobserved content reran evaluation")
		}
	})
	t.Run("comment keeps semantic value", func(t *testing.T) {
		beforeEval, beforePlan := p.evaluations, p.plans
		retainedWrite(t, root, "m0000/go.mod", "module example.com/m0000\n\ngo 1.27\n// comment\n")
		retainedOracle(t, p, files, root, profile, nil)
		wantEval := beforeEval
		if p.evaluations != wantEval || p.plans != beforePlan {
			t.Fatalf("semantic cutoff: evaluated %d -> %d, planned %d -> %d", beforeEval, p.evaluations, beforePlan, p.plans)
		}
	})
	for _, step := range []struct{ name, path, content string }{
		{"test discovered", "m0000/main_test.go", "package main\nimport \"testing\"\nfunc TestHello(t *testing.T) {}\n"},
		{"missing sum appears", "m0000/go.sum", ""},
		{"new provider", "m0002/go.mod", "module example.com/m0002\n\ngo 1.27\n"},
		{"provider source", "m0002/main.go", "package main\nfunc main() {}\n"},
		{"dependency changes", "m0001/go.mod", "module example.com/m0001\n\ngo 1.27\nrequire example.com/m0002 v0.0.0\n"},
	} {
		t.Run(step.name, func(t *testing.T) {
			retainedWrite(t, root, step.path, step.content)
			retainedOracle(t, p, files, root, profile, nil)
		})
	}
	t.Run("deletion", func(t *testing.T) {
		if err := os.Remove(filepath.Join(root, "m0000/main_test.go")); err != nil {
			t.Fatal(err)
		}
		retainedOracle(t, p, files, root, profile, nil)
	})
	t.Run("profile changes", func(t *testing.T) {
		retainedOracle(t, p, files, root, pluginv1.Profile_PROFILE_PRODUCTION, nil)
		retainedOracle(t, p, files, root, profile, nil)
	})
	t.Run("selection changes", func(t *testing.T) {
		before := p.evaluations
		selected := retainedOracle(t, p, files, root, profile, []string{"m0000/"})
		if p.evaluations != before || len(selected.actions) == 0 {
			t.Fatal("selection did not reuse analysis or selected nothing")
		}
	})
	t.Run("duplicate outside selection refused then repaired", func(t *testing.T) {
		retainedWrite(t, root, "other/go.mod", "module example.com/m0000\n\ngo 1.27\n")
		retainedWrite(t, root, "other/main.go", "package main\nfunc main() {}\n")
		_, err := p.run(t.Context(), retainedTake(t, files, root), profile, []string{"m0000/"})
		if err == nil {
			t.Fatal("duplicate provider was accepted")
		}
		retainedCompare(t, p, files, root, profile, []string{"m0000/"}, true)
		retainedWrite(t, root, "other/go.mod", "module example.com/other\n\ngo 1.27\n")
		if len(retainedOracle(t, p, files, root, profile, []string{"m0000/"}).actions) == 0 {
			t.Fatal("repair did not succeed")
		}
	})
	t.Run("malformed then repaired", func(t *testing.T) {
		retainedWrite(t, root, "other/go.mod", "not a go module\n")
		_, err := p.run(t.Context(), retainedTake(t, files, root), profile, nil)
		if err == nil {
			t.Fatal("malformed manifest accepted")
		}
		retainedCompare(t, p, files, root, profile, nil, true)
		retainedWrite(t, root, "other/go.mod", "module example.com/other\n\ngo 1.27\n")
		retainedOracle(t, p, files, root, profile, nil)
	})
}

func TestRetainedChangedSnapshotIsRefusedThenFreshOneSucceeds(t *testing.T) {
	root, p, files := warmRetained(t, 1)
	old := retainedTake(t, files, root)
	retainedWrite(t, root, "m0000/go.mod", "module example.com/changed\n\ngo 1.27\n")
	_, err := p.run(t.Context(), old, pluginv1.Profile_PROFILE_DEVELOPMENT, nil)
	var changed *source.Changed
	if !errors.As(err, &changed) {
		t.Fatalf("changed snapshot: %v", err)
	}
	retainedOracle(t, p, files, root, pluginv1.Profile_PROFILE_DEVELOPMENT, nil)
}

type retainedBlocking struct {
	Source
	once    sync.Once
	started chan struct{}
}

func (s *retainedBlocking) Read(ctx context.Context, repository, name string) ([]byte, bool, error) {
	s.once.Do(func() { close(s.started); <-ctx.Done() })
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return s.Source.Read(ctx, repository, name)
}

func TestRetainedCancellationDoesNotFailAnotherRequest(t *testing.T) {
	root := retainedFixture(t, 1)
	p := newRetainedEngine()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	blocked := &retainedBlocking{Source: retainedTake(t, &source.Keeper{}, root), started: make(chan struct{})}
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { _, err := p.run(ctx, blocked, pluginv1.Profile_PROFILE_DEVELOPMENT, nil); first <- err }()
	select {
	case <-blocked.started:
	case <-time.After(10 * time.Second):
		t.Fatal("first request never reached source")
	}
	next := retainedTake(t, &source.Keeper{}, root)
	go func() { _, err := p.run(t.Context(), next, pluginv1.Profile_PROFILE_DEVELOPMENT, nil); second <- err }()
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("first: %v", err)
	}
	if err := <-second; err != nil {
		t.Fatalf("second: %v", err)
	}
	retainedOracle(t, p, &source.Keeper{}, root, pluginv1.Profile_PROFILE_DEVELOPMENT, nil)
}

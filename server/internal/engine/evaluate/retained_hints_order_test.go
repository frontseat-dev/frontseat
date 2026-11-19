package evaluate

import (
	"context"
	"errors"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type hintsOrderSource struct {
	Source
	hint   func(string, string) bool
	digest func(context.Context, string, string) (string, bool, error)
}

func (s hintsOrderSource) MayHaveChanged(repo, path string) bool { return s.hint(repo, path) }
func (s hintsOrderSource) Digest(ctx context.Context, repo, path string) (string, bool, error) {
	if s.digest != nil {
		return s.digest(ctx, repo, path)
	}
	return s.Source.Digest(ctx, repo, path)
}

func TestRetainedHintsOrderIsAStableCompletePermutation(t *testing.T) {
	read := func(method, kind, path string, found bool) retainedQuery {
		return retainedQuery{method: method, value: &answer{reads: []pluginhost.Read{{Import: kind, Argument: path, Found: found}}}}
	}
	trace := &retainedTrace{queries: []retainedQuery{
		read("Analyze", "read", "a", true), read("Analyze", "read", "b", true),
		read("Arrange", "read", "must-not-hint-arrange", true),
		read("Analyze", "read", "must-not-hint-missing", false), read("Analyze", "glob", "must-not-hint-glob", true),
		read("Analyze", "read", "d", true), read("Analyze", "read", "e", true), read("Analyze", "read", "a", true),
		read("Reconcile", "read", "must-not-hint-reconcile", true),
	}}
	var called []string
	src := hintsOrderSource{hint: func(repo, path string) bool {
		called = append(called, path)
		return path == "b" || path == "d" || path == "e"
	}}
	if got := trace.checkOrder(src); !slices.Equal(got, []int{1, 0, 2, 5, 6, 3, 4, 7, 8}) {
		t.Fatalf("hinted order %v", got)
	}
	if !slices.Equal(called, []string{"a", "b", "d", "e", "a"}) {
		t.Fatalf("hints consulted unsupported observations: %v", called)
	}
	// Hiding the optional interface must preserve capture order completely.
	if got := trace.checkOrder(struct{ Source }{Source: src}); !slices.Equal(got, []int{0, 1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Fatalf("unhinted order %v", got)
	}
}

func TestRetainedHintsEveryRefusalStillRunsUnderEitherPriority(t *testing.T) {
	root := retainedFixture(t, 1)
	first, last := errors.New("first source refusal"), errors.New("last source refusal")
	for _, priority := range []string{"first", "last"} {
		trace := &retainedTrace{structural: &trackedReads{repositories: []string{""}}, queries: []retainedQuery{
			{method: "Analyze", value: &answer{reads: []pluginhost.Read{{Import: "read", Argument: "first", Found: true}}}},
			{method: "Analyze", value: &answer{reads: []pluginhost.Read{{Import: "read", Argument: "last", Found: true}}}},
		}}
		src := hintsOrderSource{Source: retainedTake(t, &source.Keeper{}, root), hint: func(repo, path string) bool { return path == priority }, digest: func(ctx context.Context, repo, path string) (string, bool, error) {
			if path == "first" {
				return "", false, first
			}
			return "", false, last
		}}
		reasked := 0
		same, err := trace.same(t.Context(), src, &Answers{}, &reasked, &Work{})
		want := first
		if priority == "last" {
			want = last
		}
		if same || !errors.Is(err, want) || reasked != 0 {
			t.Fatalf("priority=%s same=%v error=%v reasked=%d", priority, same, err, reasked)
		}
	}
	// This deliberately demonstrates that refusal selection can change with
	// hint state. It proves refusal, not ordinary-evaluator diagnostic order.
}

func TestRetainedHintsMissingGeneratedReadStillRefused(t *testing.T) {
	root := retainedFixture(t, 1)
	path := "m0000/generated/absent.go"
	trace := &retainedTrace{structural: &trackedReads{repositories: []string{""}}, queries: []retainedQuery{{
		method: "Analyze", guarded: true, generated: []plan.GeneratedSource{{Directory: "m0000/generated", Patterns: []string{"*.go"}}},
		value: &answer{reads: []pluginhost.Read{{Import: "read", Argument: path, Found: false}}},
	}}}
	src := hintsOrderSource{Source: retainedTake(t, &source.Keeper{}, root), hint: func(string, string) bool { t.Fatal("absence must not be used as a present-file hint"); return false }}
	reasked := 0
	if same, err := trace.same(t.Context(), src, &Answers{}, &reasked, &Work{}); same || err == nil || !strings.Contains(err.Error(), "analysis cannot read generated source") {
		t.Fatalf("guarded absence: same=%v err=%v", same, err)
	}
	trace.queries[0].guarded = false
	if same, err := trace.same(t.Context(), src, &Answers{}, &reasked, &Work{}); !same || err != nil {
		t.Fatalf("unguarded absence control: same=%v err=%v", same, err)
	}
}

func TestRetainedHintsLiteralNamesAndMissingHintInterface(t *testing.T) {
	for _, mode := range []string{"all missed", "all dirty", "interface absent"} {
		t.Run(mode, func(t *testing.T) {
			root := retainedFixture(t, 2)
			if err := os.Rename(filepath.Join(root, "m0000"), filepath.Join(root, "pkg[1]")); err != nil {
				t.Fatal(err)
			}
			p, files := newRetainedEngine(), &source.Keeper{}
			runner := hintsOrderRunner{engine: p, mode: mode}
			profile := pluginv1.Profile_PROFILE_DEVELOPMENT
			retainedOracle(t, runner, files, root, profile, nil)
			retainedWrite(t, root, "pkg[1]/main.go", "package library\nfunc Exported() {}\n")
			retainedOracle(t, runner, files, root, profile, nil)
			if p.evaluations != 2 {
				t.Fatal("literal named project's change was hidden")
			}
			retainedWrite(t, root, "pkg[1]/go.mod", "module example.com/m0001\n\ngo 1.27\n")
			retainedCompare(t, runner, files, root, profile, nil, true)
			retainedWrite(t, root, "pkg[1]/go.mod", "module example.com/m0000\n\ngo 1.27\n")
			retainedOracle(t, runner, files, root, profile, nil)
		})
	}
}

type hintsOrderRunner struct {
	engine *retainedEngine
	mode   string
}

func (p hintsOrderRunner) run(ctx context.Context, src Source, profile pluginv1.Profile, targets []string) (retainedResult, error) {
	if p.mode == "interface absent" {
		return p.engine.run(ctx, struct{ Source }{src}, profile, targets)
	}
	return p.engine.run(ctx, hintsOrderSource{Source: src, hint: func(string, string) bool { return p.mode == "all dirty" }}, profile, targets)
}

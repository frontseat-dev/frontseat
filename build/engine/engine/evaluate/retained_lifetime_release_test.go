package evaluate

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

func lifetimeOracle(t testing.TB, p *retainedEngine, files *source.Keeper, root string) selectedPlan {
	t.Helper()
	profile := typev1.Profile_PROFILE_DEVELOPMENT
	targets := []string{"m0001/"}
	return planMatches(t, p, files, root, profile, targets, "eviction changed the execution payload")
}

// assertEvaluationDropped holds that the evaluation and its plan are no
// longer kept, as where a limit was passed: the answers the evaluation
// rested on may stay, so that no plugin is asked again for them.
func assertEvaluationDropped(t testing.TB, p *retainedEngine) {
	t.Helper()
	if p.valid || p.planValid || p.trace != nil || p.result.evaluation.Projects != nil || p.result.actions != nil || p.asked != nil {
		t.Fatal("the evaluation, its plan or its trace is still kept")
	}
}

func assertForgotten(t testing.TB, p *retainedEngine) {
	t.Helper()
	if p.valid || p.planValid || p.trace != nil || p.result.evaluation.Projects != nil || p.result.actions != nil || p.asked != nil || p.targets != nil || p.answers.kept != nil || p.answers.repositories != nil {
		t.Fatal("owner still retains evaluation, plan, trace or answer-cache state")
	}
	if got := p.retainedUsage(); got != (retentionUsage{}) {
		t.Fatalf("usage after release: %+v", got)
	}
}

func TestRetainedLifetimeReleaseDropsReuseAndReconstructs(t *testing.T) {
	root := retainedFixture(t, 20)
	p, files := newRetainedEngine(), &source.Keeper{}
	first := lifetimeOracle(t, p, files, root)
	before := projectionBytes(t, first)
	lifetimeOracle(t, p, files, root)
	if p.evaluations != 1 || p.retainedUsage().WireBytes == 0 {
		t.Fatal("warm control did not retain reuse")
	}
	if err := p.forget(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertForgotten(t, p)
	if !bytes.Equal(before, projectionBytes(t, first)) {
		t.Fatal("release damaged the caller's result")
	}
	first.Needed[0].Declared.Name = "caller mutation"
	second := lifetimeOracle(t, p, files, root)
	if p.evaluations != 2 || !bytes.Equal(before, projectionBytes(t, second)) {
		t.Fatal("next request did not reconstruct correctly")
	}
	if err := p.forget(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := p.forget(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertForgotten(t, p)
}

func TestRetainedLifetimeAdmissionPreservesOversizedResults(t *testing.T) {
	root := retainedFixture(t, 20)
	control := newRetainedEngine()
	lifetimeOracle(t, control, &source.Keeper{}, root)
	usage := control.retainedUsage()
	if usage.Records < 2 || usage.WireBytes < 2 {
		t.Fatalf("unpopulated control: %+v", usage)
	}
	for _, test := range []struct {
		name   string
		limits RetentionLimits
		keep   bool
	}{
		{"at limits", RetentionLimits{usage.Records, usage.WireBytes}, true},
		{"one record over", RetentionLimits{usage.Records - 1, usage.WireBytes}, false},
		{"one byte over", RetentionLimits{usage.Records, usage.WireBytes - 1}, false},
		{"disabled", RetentionLimits{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, err := retainedLimited(test.limits)
			if err != nil {
				t.Fatal(err)
			}
			files := &source.Keeper{}
			lifetimeOracle(t, p, files, root)
			if p.valid != test.keep {
				t.Fatalf("retained=%v want %v; usage %+v", p.valid, test.keep, p.retainedUsage())
			}
			if !test.keep {
				assertEvaluationDropped(t, p)
			}
			lifetimeOracle(t, p, files, root)
			want := 2
			if test.keep {
				want = 1
			}
			if p.evaluations != want {
				t.Fatalf("evaluations=%d want %d", p.evaluations, want)
			}
		})
	}
}

func lifetimeEdit(t testing.TB, root string, iteration int) {
	t.Helper()
	retainedWrite(t, root, "frontseat.yaml", fmt.Sprintf("plugins:\n  go: {tags: [version%d]}\n  backstage: {}\n", iteration))
}

func TestRetainedLifetimeKeyChurnStaysWithinAdmissionLimits(t *testing.T) {
	root := retainedFixture(t, 20)
	control := newRetainedEngine()
	lifetimeOracle(t, control, &source.Keeper{}, root)
	usage := control.retainedUsage()
	limits := RetentionLimits{usage.Records * 2, usage.WireBytes * 2}
	p, err := retainedLimited(limits)
	if err != nil {
		t.Fatal(err)
	}
	files := &source.Keeper{}
	for i := range 12 {
		lifetimeEdit(t, root, i)
		lifetimeOracle(t, p, files, root)
		got := p.retainedUsage()
		if got.Records > limits.Records || got.WireBytes > limits.WireBytes {
			t.Fatalf("edit %d usage=%+v limits=%+v", i, got, limits)
		}
	}
	if p.evictions == 0 {
		t.Fatal("churn never exercised eviction")
	}
}

func TestRetainedLifetimeLimitsAreValidated(t *testing.T) {
	for _, limits := range []RetentionLimits{{-1, 1}, {1, -1}} {
		if _, err := retainedLimited(limits); err == nil {
			t.Fatalf("accepted negative limits %+v", limits)
		}
	}
	if _, err := retainedLimited(RetentionLimits{1, 1}); err != nil {
		t.Fatal(err)
	}
}

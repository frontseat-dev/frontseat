package evaluate

import (
	"bytes"
	"context"
	"errors"
	"testing"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
)

// A request that fails takes nothing of what is kept with it but what the
// failure shows to be no longer so. A target that names nothing, and a
// caller that gave up, leave the evaluation as it was: the next request
// evaluates nothing again, and is answered as a fresh evaluation would
// answer it. The control is the first request, which does evaluate.
func TestARequestThatFailsKeepsWhatWasEvaluated(t *testing.T) {
	root := retainedFixture(t, 2)
	s, files := buildingSession(t), &source.Keeper{}
	targets := []string{"m0001/"}
	build := func(ctx context.Context, targets []string) (BuildPlan, error) {
		return s.Build(ctx, retainedTake(t, files, root), typev1.Profile_PROFILE_DEVELOPMENT, nil, targets, nil)
	}
	first, err := build(t.Context(), targets)
	if err != nil || first.Work.Retained || s.core.evaluations != 1 {
		t.Fatalf("the first request evaluated %d times, retained=%v, %v: this test proves nothing", s.core.evaluations, first.Work.Retained, err)
	}
	fresh := projectionFresh(t, retainedTake(t, &source.Keeper{}, root), typev1.Profile_PROFILE_DEVELOPMENT)
	want := projectionBytes(t, freshPlan(t, fresh, targets))
	held := func(after string) {
		t.Helper()
		got, err := build(t.Context(), targets)
		if err != nil {
			t.Fatalf("after %s: %v", after, err)
		}
		if !got.Work.Retained || !got.Work.PlanRetained || s.core.evaluations != 1 || s.core.plans != 1 {
			t.Errorf("after %s the workspace was evaluated %d times and planned %d times; retained=%v, plan retained=%v", after, s.core.evaluations, s.core.plans, got.Work.Retained, got.Work.PlanRetained)
		}
		if !bytes.Equal(projectionBytes(t, selectedPlan{got.Needed, got.Asked, got.Entities}), want) {
			t.Errorf("after %s the plan is not what a fresh evaluation plans", after)
		}
	}
	held("nothing")

	var mistake error
	if _, mistake = build(t.Context(), []string{"//m0001:go.compil"}); mistake == nil {
		t.Fatal("a target that names nothing was planned")
	}
	held("a target that names nothing")

	gone, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := build(gone, targets); !errors.Is(err, context.Canceled) {
		t.Fatalf("a caller that gave up was answered: %v", err)
	}
	held("a caller that gave up")
}

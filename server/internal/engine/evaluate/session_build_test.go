package evaluate

import (
	"bytes"
	"fmt"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"testing"
)

func TestRetainedBuildFiltersArePartOfSelection(t *testing.T) {
	root := retainedFixture(t, 2)
	s, files := buildingSession(t), &source.Keeper{}
	for i, only := range [][]string{nil, {"compile"}, {"format"}, {"compile"}, {"unknown"}, {"lint"}, nil} {
		targets := []string{"m0001/"}
		profile := pluginv1.Profile_PROFILE_DEVELOPMENT
		if i == 3 {
			profile = pluginv1.Profile_PROFILE_PRODUCTION
		}
		got, gotErr := s.Build(t.Context(), retainedTake(t, files, root), profile, nil, targets, only)
		fresh := projectionFresh(t, retainedTake(t, &source.Keeper{}, root), profile)
		needed, asked, wantErr := plan.Of(fresh.Actions, fresh.Entities, targets, only)
		if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
			t.Fatalf("filter %v refusal differs: %v / %v", only, gotErr, wantErr)
		}
		if wantErr == nil && !bytes.Equal(projectionBytes(t, selectedPlan{got.Needed, got.Asked, got.Entities}), projectionBytes(t, selectedPlan{needed, asked, fresh.Entities})) {
			t.Fatalf("filter %v reused another selection", only)
		}
	}
}

func TestRetainedBuildReportsOnlyThisOperationsWork(t *testing.T) {
	root := retainedFixture(t, 2)
	s, files := buildingSession(t), &source.Keeper{}
	build := func() BuildPlan {
		t.Helper()
		return wholeBuild(t, s, files, root)
	}
	first := build()
	if first.Work.Asked == 0 || first.Work.Retained || first.Work.PlanRetained {
		t.Fatalf("cold control: %+v", first.Work)
	}
	second := build()
	if second.Work != (Work{Retained: true, PlanRetained: true}) {
		t.Fatalf("unchanged operation: %+v", second.Work)
	}
	retainedWrite(t, root, "m0000/go.mod", "module example.com/m0000\n\ngo 1.27\n// equivalent answer\n")
	equal := build()
	if equal.Work != (Work{Asked: 1, Retained: true, PlanRetained: true}) {
		t.Fatalf("equal changed answer: %+v", equal.Work)
	}
	retainedWrite(t, root, "m0000/main_test.go", "package main\nimport \"testing\"\nfunc TestNew(t *testing.T) {}\n")
	changed := build()
	if changed.Work.Asked == 0 || changed.Work.Retained || changed.Work.PlanRetained {
		t.Fatalf("changed answer: %+v", changed.Work)
	}
	after := build()
	if after.Work != (Work{Retained: true, PlanRetained: true}) {
		t.Fatalf("changed costs leaked into later operation: %+v", after.Work)
	}
}

package evaluate

import (
	"context"
	"fmt"
	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"os"
	"runtime"
	"testing"
)

// Current uses normal evaluation and only the operation's normal mapping or
// plan.Of. Projected additionally copies the retained plan's mutable payloads.
// Both return the complete execution plan, not the smaller run-event summary.
func projectionRun(ctx context.Context, src Source, mode, operation string, answers *Answers, retained *retainedEngine) (out testProjection, err error) {
	profile := typev1.Profile_PROFILE_DEVELOPMENT
	var targets []string
	if operation == "selected" {
		targets = []string{"m0501/"}
	}
	if mode == "projected" {
		if operation == "projects" {
			out.projects, err = retained.projectList(ctx, src, profile)
		} else {
			out.plan, err = retained.selectedPlan(ctx, src, profile, targets)
		}
		return out, err
	}
	plugins, err := retainedLoaded()
	if err != nil {
		return out, err
	}
	e, err := Evaluate(ctx, src, plugins, answers, profile, nil)
	if err != nil {
		return out, err
	}
	if operation == "projects" {
		out.projects = e.ProjectList()
	} else {
		out.plan.Needed, out.plan.Asked, err = plan.Of(e.Actions, e.Entities, targets, nil)
		out.plan.Entities = e.Entities
	}
	return out, err
}

// Eight alternating rounds, warm caches, timed snapshots and complete returns.
// Edits, setup and the separate Source-call probe are outside the timer.
func BenchmarkRetainedProjection(b *testing.B) {
	if _, err := retainedLoaded(); err != nil {
		b.Fatal(err)
	}
	modes := []string{"current", "projected"}
	for _, operation := range []string{"projects", "selected", "all"} {
		for round := range 8 {
			for _, scenario := range []string{"unchanged", "source", "manifest", "test", "rotate"} {
				for index := range modes {
					mode := modes[(index+round)%len(modes)]
					b.Run(fmt.Sprintf("%s/%s/%s/round%d", operation, scenario, mode, round), func(b *testing.B) {
						root := benchFixture(b, scenario)
						files, answers := &source.Keeper{}, &Answers{}
						retained := newRetainedEngine()
						var result testProjection
						run := func(snapshot Source) {
							var err error
							result, err = projectionRun(b.Context(), snapshot, mode, operation, answers, retained)
							if err != nil {
								b.Fatal(err)
							}
						}
						call := benchWarm(b, files, root, run)
						retained.evaluations, retained.plans, retained.reasked, *fullCopies(retained) = 0, 0, 0, 0
						iteration := benchEdits(b, root, scenario, call)
						p := retained
						if mode == "projected" {
							wantPlans := benchEvaluated(b, scenario, p)
							if operation == "projects" {
								wantPlans = 0
							}
							if retained.plans != wantPlans || *fullCopies(retained) != 0 {
								b.Fatalf("plans=%d want=%d fullCopies=%d", retained.plans, wantPlans, *fullCopies(retained))
							}
							b.ReportMetric(float64(retained.reasked)/float64(b.N), "reasked/op")
						}
						if mode != "current" {
							benchWork(b, p)
						}
						if operation == "projects" {
							if len(result.projects.GetProjects()) != 1000 {
								b.Fatal("wrong project count")
							}
							b.ReportMetric(float64(len(result.projects.GetProjects())), "projects/op")
						} else {
							if len(result.plan.Needed) == 0 || len(result.plan.Asked) == 0 {
								b.Fatal("empty plan")
							}
							b.ReportMetric(float64(len(result.plan.Needed)), "actions/op")
						}
						benchProbe(b, files, root, scenario, iteration, run)
					})
				}
			}
		}
	}

}

// Run each operation/mode pair in a separate process, retaining its latest
// result. Two GCs precede each total-process live-heap sample.
func TestRetainedProjectionHeap(t *testing.T) {
	mode, operation := os.Getenv("FRONTSEAT_RETAINED_HEAP_MODE"), os.Getenv("FRONTSEAT_RETAINED_HEAP_OPERATION")
	if operation == "" {
		t.Skip("separate projection memory probe")
	}
	if mode != "current" && mode != "projected" {
		t.Fatal("unknown memory mode")
	}
	if operation != "projects" && operation != "selected" && operation != "all" {
		t.Fatal("unknown memory operation")
	}
	if _, err := retainedLoaded(); err != nil {
		t.Fatal(err)
	}
	root := retainedFixture(t, 1000)
	files, answers, retained := &source.Keeper{}, &Answers{}, newRetainedEngine()
	var result testProjection
	report := heapReport(t, retained, fmt.Sprintf("operation=%s mode=%s", operation, mode))
	report(-1)
	heapCheckpoints(t, files, root, report, func(snapshot Source) (err error) {
		result, err = projectionRun(t.Context(), snapshot, mode, operation, answers, retained)
		return err
	})
	runtime.KeepAlive(result)
	runtime.KeepAlive(files)
	runtime.KeepAlive(answers)
	runtime.KeepAlive(retained)
}

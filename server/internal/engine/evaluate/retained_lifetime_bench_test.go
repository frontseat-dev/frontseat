package evaluate

import (
	"fmt"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"os"
	"runtime"
	"testing"
)

func lifetimeEngine(t testing.TB, mode string, limits RetentionLimits) *retainedEngine {
	t.Helper()
	if mode == "unbounded" {
		return newRetainedEngine()
	}
	if mode != "limited" {
		t.Fatal("unknown retention mode")
	}
	p, err := retainedLimited(limits)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Compare keeping without limit with keeping under limits, using the same operation
// implementation and owned selected plan. Eight rounds alternate mode order.
// Snapshots, policy accounting, eviction and returned payloads are timed; edits
// and warmup are not. The released case includes forget and reconstruction.
func BenchmarkRetainedLifetime(b *testing.B) {
	if _, err := retainedLoaded(); err != nil {
		b.Fatal(err)
	}
	limits := RetentionLimits{Records: 8192, WireBytes: 8 << 20}
	modes := []string{"unbounded", "limited"}
	for round := range 8 {
		for _, scenario := range []string{"unchanged", "churn", "released"} {
			for index := range modes {
				mode := modes[(index+round)%2]
				b.Run(fmt.Sprintf("%s/%s/round%d", scenario, mode, round), func(b *testing.B) {
					root := retainedFixture(b, 1000)
					if scenario == "churn" {
						lifetimeEdit(b, root, -1)
					}
					files := &source.Keeper{}
					p := lifetimeEngine(b, mode, limits)
					var result selectedPlan
					call := func() {
						if scenario == "released" {
							if err := p.forget(b.Context()); err != nil {
								b.Fatal(err)
							}
						}
						var err error
						result, err = p.selectedPlan(b.Context(), retainedTake(b, files, root), pluginv1.Profile_PROFILE_DEVELOPMENT, []string{"m0501/"})
						if err != nil {
							b.Fatal(err)
						}
					}
					call()
					call()
					if !p.valid || p.evictions != 0 {
						b.Fatal("fixture does not fit the limits")
					}
					p.evaluations, p.plans, p.evictions = 0, 0, 0
					iteration := 0
					b.ReportAllocs()
					for b.Loop() {
						b.StopTimer()
						if scenario == "churn" {
							lifetimeEdit(b, root, iteration)
						}
						iteration++
						b.StartTimer()
						call()
					}
					if len(result.Needed) != 3 || len(result.Asked) != 3 || *fullCopies(p) != 0 {
						b.Fatal("wrong selected result")
					}
					wantEvaluations := b.N
					if scenario == "unchanged" {
						wantEvaluations = 0
					}
					if p.evaluations != wantEvaluations || p.plans != wantEvaluations {
						b.Fatal("evaluation/plan counters differ")
					}
					if mode == "limited" && scenario == "churn" && p.evictions == 0 {
						b.Fatal("churn did not cross a retention limit")
					}
					if (mode == "unbounded" || scenario != "churn") && p.evictions != 0 {
						b.Fatal("unexpected policy release")
					}
					usage := p.retainedUsage()
					if mode == "limited" && (usage.Records > limits.Records || usage.WireBytes > limits.WireBytes) {
						b.Fatal("retained usage exceeded limit")
					}
					b.ReportMetric(float64(p.evaluations)/float64(b.N), "evaluations/op")
					b.ReportMetric(float64(p.evictions)/float64(b.N), "evictions/op")
					b.ReportMetric(float64(usage.Records), "records/end")
					b.ReportMetric(float64(usage.WireBytes), "wire-B/end")
				})
			}
		}
	}
}

// A different request key on every edit exercises accumulation which repeated
// equal manifest replies do not. Run modes in separate processes. Source cache,
// plugins and the latest owned result remain live after explicit session release.
func TestRetainedLifetimeHeap(t *testing.T) {
	mode := os.Getenv("FRONTSEAT_RETAINED_RETENTION")
	if mode == "" {
		t.Skip("separate retention heap probe")
	}
	if _, err := retainedLoaded(); err != nil {
		t.Fatal(err)
	}
	limits := RetentionLimits{Records: 1024, WireBytes: 1 << 20}
	p, files := lifetimeEngine(t, mode, limits), &source.Keeper{}
	root := retainedFixture(t, 100)
	var result selectedPlan
	report := func(checkpoint string) {
		runtime.GC()
		runtime.GC()
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		usage := p.retainedUsage()
		t.Logf("mode=%s checkpoint=%s heap=%d records=%d wire=%d evictions=%d", mode, checkpoint, stats.HeapAlloc, usage.Records, usage.WireBytes, p.evictions)
	}
	report("before")
	for i := range 101 {
		lifetimeEdit(t, root, i)
		var err error
		result, err = p.selectedPlan(t.Context(), retainedTake(t, files, root), pluginv1.Profile_PROFILE_DEVELOPMENT, []string{"m0001/"})
		if err != nil {
			t.Fatal(err)
		}
		usage := p.retainedUsage()
		if mode == "limited" && (usage.Records > limits.Records || usage.WireBytes > limits.WireBytes) {
			t.Fatalf("over limit after edit %d", i)
		}
		if i == 0 || i == 10 || i == 100 {
			report(fmt.Sprint(i))
		}
	}
	if mode == "limited" && p.evictions == 0 {
		t.Fatal("churn did not exercise policy releases")
	}
	if err := p.forget(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertForgotten(t, p)
	report("released")
	if len(result.Needed) != 3 {
		t.Fatal("release damaged latest caller result")
	}
	runtime.KeepAlive(result)
	runtime.KeepAlive(files)
	runtime.KeepAlive(p)
}

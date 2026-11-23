package evaluate

import (
	"context"
	"fmt"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
)

// Eight rounds alternate mode order. All modes use warm plugin/answer caches and
// include snapshot acquisition. Edits and fixture setup are outside the timer.
// No action execution or API/daemon integration is measured here.
func BenchmarkRetainedOwned(b *testing.B) {
	if _, err := retainedLoaded(); err != nil {
		b.Fatal(err)
	}
	modes := []string{"current", "retained"}
	for round := range 8 {
		for _, scenario := range []string{"unchanged", "source", "manifest", "test", "rotate"} {
			for index := range modes {
				mode := modes[(index+round)%len(modes)]
				b.Run(fmt.Sprintf("%s/%s/round%d", scenario, mode, round), func(b *testing.B) {
					root := benchFixture(b, scenario)
					files, answers := &source.Keeper{}, &Answers{}
					retained := newRetainedEngine()
					run := func(snapshot Source) {
						var err error
						switch mode {
						case "current":
							_, err = retainedFresh(b.Context(), snapshot, typev1.Profile_PROFILE_DEVELOPMENT, nil, answers)
						case "retained":
							_, err = retained.run(b.Context(), snapshot, typev1.Profile_PROFILE_DEVELOPMENT, nil)
						}
						if err != nil {
							b.Fatal(err)
						}
					}
					call := benchWarm(b, files, root, run)
					retained.evaluations, retained.plans, retained.reasked = 0, 0, 0
					iteration := benchEdits(b, root, scenario, call)
					p := retained
					if mode == "retained" {
						benchEvaluated(b, scenario, p)
						b.ReportMetric(float64(retained.reasked)/float64(b.N), "reasked/op")
					}
					if mode != "current" {
						benchWork(b, p)
					}
					benchProbe(b, files, root, scenario, iteration, run)
				})
			}
		}
	}
}

// benchFixture is a repository of a thousand modules made ready for
// scenario: where the scenario edits tests, its tests are sharded and
// edited once, so that the timed edits change what was already there.
func benchFixture(b testing.TB, scenario string) string {
	b.Helper()
	root := retainedFixture(b, 1000)
	if scenario == "test" || scenario == "rotate" {
		retainedWrite(b, root, "frontseat.yaml", "plugins:\n  go: {test-shards: 2}\n  backstage: {}\n")
		if scenario == "rotate" {
			for i := range 10 {
				writeTestEdit(b, root, i*100+1, -1)
			}
		} else {
			fineEdit(b, root, scenario, -1)
		}
	}
	return root
}

// benchWarm runs an operation twice over snapshots of root, so that
// plugins and what is kept are warm, and returns the operation.
func benchWarm(b *testing.B, files *source.Keeper, root string, run func(Source)) func() {
	b.Helper()
	call := func() { run(retainedTake(b, files, root)) }
	call()
	call()
	return call
}

// benchEdits times call after each edit of scenario, the edit outside the
// timer, and returns how many edits were made.
func benchEdits(b *testing.B, root, scenario string, call func()) int {
	b.Helper()
	iteration := 0
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		fineEdit(b, root, scenario, iteration)
		iteration++
		b.StartTimer()
		call()
	}
	return iteration
}

// benchEvaluated fails the benchmark unless the retained engine evaluated
// again for each edit of tests and for no other edit, and returns how many
// times it should have evaluated.
func benchEvaluated(b *testing.B, scenario string, p *retainedEngine) int {
	b.Helper()
	wantEvaluations := 0
	if scenario == "test" || scenario == "rotate" {
		wantEvaluations = b.N
	}
	if p.evaluations != wantEvaluations {
		b.Fatalf("%s evaluated %d times; want %d", scenario, p.evaluations, wantEvaluations)
	}
	return wantEvaluations
}

// benchWork reports how often the retained engine evaluated and planned
// for each operation.
func benchWork(b *testing.B, p *retainedEngine) {
	b.ReportMetric(float64(p.evaluations)/float64(b.N), "evaluations/op")
	b.ReportMetric(float64(p.plans)/float64(b.N), "plans/op")
}

// benchProbe makes the next edit and runs one operation more, after the
// timer, over a source that counts what it is asked, so that its atomics
// do not distort the benchmark; it reports the counts.
func benchProbe(b *testing.B, files *source.Keeper, root, scenario string, iteration int, run func(Source)) {
	b.Helper()
	b.StopTimer()
	fineEdit(b, root, scenario, iteration)
	counted := &hintsCountedSource{Source: retainedTake(b, files, root)}
	run(counted)
	b.ReportMetric(float64(counted.digests.Load()), "digests/probe")
	b.ReportMetric(float64(counted.reads.Load()), "reads/probe")
	b.ReportMetric(float64(counted.globs.Load()), "globs/probe")
	b.ReportMetric(float64(counted.hints.Load()), "hints/probe")
}

// heapReport logs, under label, the process's live heap after two
// collections and how many queries the retained engine's trace holds.
func heapReport(t *testing.T, retained *retainedEngine, label string) func(checkpoint int) {
	return func(checkpoint int) {
		runtime.GC()
		runtime.GC()
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		queries := 0
		if retained.trace != nil {
			queries = len(retained.trace.queries)
		}
		t.Logf("%s checkpoint=%d heap=%d queries=%d", label, checkpoint, stats.HeapAlloc, queries)
	}
}

// heapCheckpoints runs step over a snapshot of root a hundred and one
// times, a manifest edited before each but the first, and reports the
// heap after the first, the tenth and the hundredth.
func heapCheckpoints(t *testing.T, files *source.Keeper, root string, report func(int), step func(Source) error) {
	t.Helper()
	for i := range 101 {
		if i > 0 {
			fineEdit(t, root, "manifest", i)
		}
		if err := step(retainedTake(t, files, root)); err != nil {
			t.Fatal(err)
		}
		if i == 0 || i == 10 || i == 100 {
			report(i)
		}
	}
}

func fineEdit(t testing.TB, root, scenario string, iteration int) {
	t.Helper()
	switch scenario {
	case "source":
		retainedWrite(t, root, "m0501/main.go", fmt.Sprintf("package main\nfunc main() { println(%d) }\n", iteration))
	case "manifest":
		retainedWrite(t, root, "m0500/go.mod", fmt.Sprintf("module example.com/m0500\n\ngo 1.27\n// edit %d\n", iteration))
	case "test":
		writeTestEdit(t, root, 501, iteration)
	case "rotate":
		writeTestEdit(t, root, (iteration%10)*100+1, iteration)
	}
}

func writeTestEdit(t testing.TB, root string, module, iteration int) {
	t.Helper()
	retainedWrite(t, root, fmt.Sprintf("m%04d/main_test.go", module), fmt.Sprintf("package main\nimport \"testing\"\nfunc TestStable(t *testing.T) {}\nfunc TestCase%d(t *testing.T) {}\n", iteration+1))
}

type hintsCountedSource struct {
	Source
	reads, digests, globs, hints atomic.Int64
}

func (s *hintsCountedSource) Read(ctx context.Context, repository, path string) ([]byte, bool, error) {
	s.reads.Add(1)
	return s.Source.Read(ctx, repository, path)
}
func (s *hintsCountedSource) Digest(ctx context.Context, repository, path string) (string, bool, error) {
	s.digests.Add(1)
	return s.Source.Digest(ctx, repository, path)
}
func (s *hintsCountedSource) Within(ctx context.Context, repository, directory string, patterns []string) ([]string, error) {
	s.globs.Add(1)
	return s.Source.Within(ctx, repository, directory, patterns)
}

// Run each mode in its own process. Report total process heap after two GCs,
// with the last returned result retained in all modes (including current).
func TestRetainedOwnedHeap(t *testing.T) {
	mode := os.Getenv("FRONTSEAT_RETAINED_HEAP_MODE")
	if mode == "" {
		t.Skip("separate memory probe")
	}
	if mode != "current" && mode != "retained" {
		t.Fatal("unknown memory mode")
	}
	if _, err := retainedLoaded(); err != nil {
		t.Fatal(err)
	}
	root := retainedFixture(t, 1000)
	files, answers := &source.Keeper{}, &Answers{}
	retained := newRetainedEngine()
	var result retainedResult
	report := heapReport(t, retained, fmt.Sprintf("mode=%s", mode))
	report(-1)
	heapCheckpoints(t, files, root, report, func(snapshot Source) (err error) {
		switch mode {
		case "current":
			result, err = retainedFresh(t.Context(), snapshot, typev1.Profile_PROFILE_DEVELOPMENT, nil, answers)
		case "retained":
			result, err = retained.run(t.Context(), snapshot, typev1.Profile_PROFILE_DEVELOPMENT, nil)
		}
		return err
	})
	runtime.KeepAlive(result)
	runtime.KeepAlive(files)
	runtime.KeepAlive(answers)
	runtime.KeepAlive(retained)
}

// Preserve the optional optimization while counting outside timed samples.
func (s *hintsCountedSource) MayHaveChanged(repository, path string) bool {
	s.hints.Add(1)
	if hints, ok := s.Source.(interface{ MayHaveChanged(string, string) bool }); ok {
		return hints.MayHaveChanged(repository, path)
	}
	return false
}

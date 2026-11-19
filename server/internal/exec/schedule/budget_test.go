package schedule

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbudget"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// instant is a runner whose actions pass at once, so that what is
// measured is the scheduling.
type instant struct{}

func (instant) Run(context.Context, plan.Action, []Handed) Ending {
	return Ending{Outcome: Passed}
}
func (instant) Skip(plan.Action, model.Label, Outcome) {}

// BenchmarkAThousandActions is a run of a thousand actions scheduled:
// a hundred that need nothing, and nine hundred that each need one of
// those.
func BenchmarkAThousandActions(b *testing.B) {
	benchmarkActions(b, context.Background())
}

// BenchmarkAThousandTracedActions includes creation and completion of each task
// span and all dependency links. Exporting is the SDK processor's separate job.
func BenchmarkAThousandTracedActions(b *testing.B) {
	provider := sdktrace.NewTracerProvider()
	b.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	ctx, parent := provider.Tracer("benchmark").Start(context.Background(), "execute")
	b.Cleanup(func() { parent.End() })
	benchmarkActions(b, ctx)
}

func benchmarkActions(b *testing.B, ctx context.Context) {
	var actions []plan.Action
	for i := range 100 {
		actions = append(actions, action("fetch"+strconv.Itoa(i)))
	}
	for i := range 900 {
		actions = append(actions, action("build"+strconv.Itoa(i), "fetch"+strconv.Itoa(i%100)))
	}
	for b.Loop() {
		All(ctx, actions, 8, instant{})
	}
}

// Measured 2026-11-05 on an Apple M-series laptop, three runs of
// `go test -run TestBudget -v ./internal/exec/schedule/`: 1.0 ms and
// 6057 to 6059 allocations for the thousand, which is a microsecond and
// six allocations an action. The time allowed is ten times that, the
// allocations a tenth more.
func TestBudgetOfSchedulingAThousandActions(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 10 * time.Millisecond, Allocs: 6700}, BenchmarkAThousandActions)
}

// Measured 2026-11-06 on Apple M4 Max with
// `mise exec -- go test -run '^$' -bench BenchmarkAThousand -benchmem -count=3 ./internal/exec/schedule`:
// traced scheduling took 2.12–2.95 ms and 26824–26846 allocations for a
// thousand actions and 900 links. The budget leaves time for slower hosts
// and about a tenth more allocations, without changing the disabled budget.
func TestBudgetOfSchedulingAThousandTracedActions(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 30 * time.Millisecond, Allocs: 30000}, BenchmarkAThousandTracedActions)
}

package telemetry_test

import (
	"context"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/core/telemetry"
	"github.com/frontseat-dev/frontseat/testsupport/testbudget"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

func BenchmarkDisabledSpan(b *testing.B) {
	ctx := context.Background()
	for b.Loop() {
		_, span := telemetry.Start(ctx, "work")
		telemetry.End(span, nil)
	}
}

func BenchmarkPipelineTelemetry(b *testing.B) {
	traces := sdktrace.NewTracerProvider()
	metrics := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewManualReader()))
	b.Cleanup(func() { _ = traces.Shutdown(context.Background()); _ = metrics.Shutdown(context.Background()) })
	signals, err := telemetry.New(traces, metrics, nil)
	if err != nil {
		b.Fatal(err)
	}
	ctx := signals.Context(context.Background())
	for b.Loop() {
		ctx, span := telemetry.Pipeline(ctx, "build", "run")
		telemetry.Result(span, semconv.CICDPipelineResultSuccess, "", "")
		telemetry.PipelineDuration(ctx, "build", time.Second, semconv.CICDPipelineResultSuccess, "")
		span.End()
	}
}

// Measured on Apple M4 Max with:
// mise exec -- go test -run '^$' -bench 'Benchmark(DisabledSpan|PipelineTelemetry)' -benchmem -count=3 ./core/telemetry
// Disabled: 5.612–5.714 ns, 0 allocations. Pipeline span plus the standard
// duration metric: 1047–1125 ns, 17 allocations (in-process SDK, no exporter).
// The time budget allows machine variation; allocation budgets remain close.
func TestBudgetOfTelemetry(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		testbudget.Hold(t, testbudget.Budget{Time: 100 * time.Nanosecond, Allocs: 0}, BenchmarkDisabledSpan)
	})
	t.Run("pipeline", func(t *testing.T) {
		testbudget.Hold(t, testbudget.Budget{Time: 10 * time.Microsecond, Allocs: 20}, BenchmarkPipelineTelemetry)
	})
}

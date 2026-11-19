package telemetry_test

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/frontseat-dev/frontseat/server/telemetry"
)

// A run nobody traces pays nothing for the spans along its way: they
// are on paths held to a budget.
func TestWhereNothingIsRecordedASpanCostsNothing(t *testing.T) {
	ctx := context.Background()

	allocations := testing.AllocsPerRun(100, func() {
		within, span := telemetry.Start(ctx, "work")
		if within != ctx {
			t.Fatal("the context was replaced, and nothing was to be recorded")
		}
		telemetry.End(span, nil)
	})

	if allocations != 0 {
		t.Errorf("a span that records nothing made %v allocations, want none", allocations)
	}
	if rooted, _ := telemetry.Root(ctx, nil, "run"); rooted != ctx {
		t.Error("a run with no provider was given a span")
	}
}

func TestASpanIsBeneathTheOneItsContextCarriesAndSaysWhatWentWrong(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = provider.Shutdown(context.Background()) }()

	ctx, run := telemetry.Root(context.Background(), provider, "run")
	_, work := telemetry.Start(ctx, "work")
	telemetry.End(work, errors.New("it broke"))
	telemetry.End(run, nil)

	ended := recorder.Ended()
	if len(ended) != 2 || ended[0].Name() != "work" || ended[1].Name() != "run" {
		t.Fatalf("%d spans ended, want work and then run", len(ended))
	}
	if ended[0].Parent().SpanID() != ended[1].SpanContext().SpanID() {
		t.Error("work is not beneath run")
	}
	if ended[0].Status().Code != codes.Error || ended[0].Status().Description != "it broke" {
		t.Errorf("work ended %v %q, want an error that says it broke", ended[0].Status().Code, ended[0].Status().Description)
	}
	if ended[1].Status().Code == codes.Error {
		t.Error("run ended as an error, and nothing went wrong in it")
	}
}

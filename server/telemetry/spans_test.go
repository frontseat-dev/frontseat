package telemetry_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

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

// recordedRun is a run that is traced: what records the spans that end,
// and the run's span with the context that carries it.
func recordedRun(t *testing.T) (*tracetest.SpanRecorder, context.Context, trace.Span) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	ctx, run := telemetry.Root(context.Background(), provider, "run")
	t.Cleanup(func() { _ = provider.Shutdown(ctx) })
	return recorder, ctx, run
}

func TestASpanIsBeneathTheOneItsContextCarriesAndSaysWhatWentWrong(t *testing.T) {
	recorder, ctx, run := recordedRun(t)
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

// What went wrong is said as one of a few kinds, the same whatever Go
// type the error is of: a span's error.type is a dimension, and a type's
// name, which is nearly always that of a wrapped error, says nothing.
func TestWhatWentWrongInASpanIsOneOfAFewKinds(t *testing.T) {
	recorder, ctx, run := recordedRun(t)
	wrong := []struct {
		err  error
		kind string
	}{
		{errors.New("it broke"), "error"},
		{fmt.Errorf("the action: %w", errors.New("it broke")), "error"},
		{fmt.Errorf("the action: %w", context.Canceled), "cancelled"},
		{fmt.Errorf("the action: %w", context.DeadlineExceeded), "timeout"},
	}
	for _, each := range wrong {
		_, work := telemetry.Start(ctx, "work")
		telemetry.End(work, each.err)
	}
	telemetry.End(run, nil)

	ended := recorder.Ended()
	if len(ended) != len(wrong)+1 {
		t.Fatalf("%d spans ended, want %d", len(ended), len(wrong)+1)
	}
	for i, each := range wrong {
		var kind string
		for _, attribute := range ended[i].Attributes() {
			if attribute.Key == "error.type" {
				kind = attribute.Value.AsString()
			}
		}
		if kind != each.kind {
			t.Errorf("%v is said as %q, want %q", each.err, kind, each.kind)
		}
	}
	for _, attribute := range ended[len(wrong)].Attributes() {
		if attribute.Key == "error.type" {
			t.Errorf("run, in which nothing went wrong, says %q went wrong", attribute.Value.AsString())
		}
	}
}

package schedule

import (
	"context"
	"errors"

	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/core/telemetry"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

// traceTasks observes one scheduling attempt. Action spans cover waiting and
// resolution, including a cache answer or a decision to skip; worker execution
// is beneath them. Plan.With supplies the order, so links are present at span
// creation, even if a failed prerequisite prevents another from ever running.
// The map belongs to this call only: retries and concurrent runs cannot reuse it.
func traceTasks(ctx context.Context, actions []plan.Action) map[string]context.Context {
	tracer := telemetry.Tracer(ctx)
	if tracer == nil {
		return nil
	}
	tasks := make(map[string]context.Context, len(actions))
	for _, action := range plan.With(actions, actions) {
		links := make([]trace.Link, 0, len(action.Needs))
		for _, need := range action.Needs {
			if linked := trace.SpanContextFromContext(tasks[need.String()]); linked.IsValid() {
				links = append(links, trace.Link{SpanContext: linked})
			}
		}
		task, span := tracer.Start(ctx, "action", trace.WithAttributes(semconv.CICDPipelineTaskName(action.Label.String())), trace.WithLinks(links...))
		if span.SpanContext().IsValid() {
			span.SetAttributes(semconv.CICDPipelineTaskRunID(span.SpanContext().SpanID().String()))
		}
		tasks[action.Label.String()] = task
	}
	return tasks
}

// endTask records the scheduler's terminal outcome once. A successful or skipped
// action stays so if another action later cancels the attempt. An action that
// was interrupted or never reached is accounted for without claiming execution.
func endTask(ctx context.Context, ending Ending) {
	result := semconv.CICDPipelineTaskRunResultSuccess
	errorType, message := "", ""
	if ending.Err != nil {
		message = ending.Err.Error()
	}
	switch ending.Outcome {
	case Passed:
	case Failed:
		result = semconv.CICDPipelineTaskRunResultFailure
		errorType = "failure"
	case Skipped:
		result = semconv.CICDPipelineTaskRunResultSkip
	default:
		switch {
		case ending.Err != nil && !errors.Is(ending.Err, context.Canceled) && !errors.Is(ending.Err, context.DeadlineExceeded):
			// The error may be what cancelled the attempt (for example,
			// content evicted by the grid). Preserve that cause.
			result = semconv.CICDPipelineTaskRunResultError
			errorType = "error"
		case errors.Is(ending.Err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
			result = semconv.CICDPipelineTaskRunResultTimeout
			errorType = "timeout"
		case errors.Is(ending.Err, context.Canceled) || ctx.Err() != nil:
			result = semconv.CICDPipelineTaskRunResultCancellation
		default:
			result = semconv.CICDPipelineTaskRunResultError
			errorType = "error"
		}
	}
	span := trace.SpanFromContext(ctx)
	telemetry.Result(span, result, errorType, message)
	span.End()
}

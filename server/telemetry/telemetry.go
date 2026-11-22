// Package telemetry connects a command's spans, logs and measurements through
// its context. Providers belong to a process, never to a global variable.
// Transport adapters carry W3C trace context; telemetry never enters an action's
// inputs or environment, so observing a build cannot change its cache key.
package telemetry

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/semconv/v1.43.0/cicdconv"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const scope = "github.com/frontseat-dev/frontseat"

type contextKey struct{}

// Signals are a process's explicitly supplied OpenTelemetry providers.
// Construct them once with New or Setup, and pass their context to commands.
type Signals struct {
	Traces    trace.TracerProvider
	Metrics   metric.MeterProvider
	Logs      log.LoggerProvider
	duration  cicdconv.PipelineRunDuration
	logger    log.Logger
	transport *clientTransport
}

// New uses the supplied providers. Nil providers disable their signal.
func New(traces trace.TracerProvider, metrics metric.MeterProvider, logs log.LoggerProvider) (*Signals, error) {
	s := &Signals{Traces: traces, Metrics: metrics, Logs: logs}
	if metrics != nil {
		var err error
		s.duration, err = cicdconv.NewPipelineRunDuration(metrics.Meter(scope, metric.WithSchemaURL(semconv.SchemaURL)))
		if err != nil {
			return nil, err
		}
	}
	if logs != nil {
		s.logger = logs.Logger(scope, log.WithSchemaURL(semconv.SchemaURL))
	}
	s.transport = newClientTransport(s)
	return s, nil
}

// Context associates providers with work, without starting a span.
func (s *Signals) Context(ctx context.Context) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, s)
}

// FromContext returns the providers of this work, or nil when none were supplied.
func FromContext(ctx context.Context) *Signals {
	s, _ := ctx.Value(contextKey{}).(*Signals)
	return s
}

// Root begins an independent trace with an explicitly supplied provider.
func Root(ctx context.Context, provider trace.TracerProvider, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	if provider == nil {
		return ctx, noop.Span{}
	}
	return provider.Tracer(scope, trace.WithSchemaURL(semconv.SchemaURL)).Start(ctx, name, trace.WithNewRoot(), trace.WithAttributes(attrs...))
}

// Start continues the trace carried by ctx, including a remote parent.
func Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	if tracer := Tracer(ctx); tracer != nil {
		return tracer.Start(ctx, name, trace.WithAttributes(attrs...))
	}
	return ctx, noop.Span{}
}

// Tracer returns the explicitly supplied tracer, or the recording parent's
// tracer for callers that supply their own SDK. Nil means tracing is disabled.
func Tracer(ctx context.Context) trace.Tracer {
	var provider trace.TracerProvider
	if s := FromContext(ctx); s != nil {
		provider = s.Traces
	} else {
		above := trace.SpanFromContext(ctx)
		if above.IsRecording() {
			provider = above.TracerProvider()
		}
	}
	if provider == nil {
		return nil
	}
	return provider.Tracer(scope, trace.WithSchemaURL(semconv.SchemaURL))
}

// Pipeline starts a CI/CD pipeline run below the command that requested it.
// The daemon resource remains the service; run identity is scoped to this span,
// never attached to the shared provider or to metric dimensions.
func Pipeline(ctx context.Context, name, id string) (context.Context, trace.Span) {
	action := semconv.CICDPipelineActionNameRun
	if name == "build" {
		action = semconv.CICDPipelineActionNameBuild
	}
	tracer := Tracer(ctx)
	if tracer == nil {
		return ctx, noop.Span{}
	}
	return tracer.Start(ctx, action.Value.AsString()+" "+name, trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(semconv.CICDPipelineName(name), semconv.CICDPipelineRunID(id), action))
}

// Result records a pipeline or task result with a bounded error classification;
// cancellation is a result, not a system failure.
func Result(span trace.Span, result attribute.KeyValue, errorType, message string) {
	span.SetAttributes(result)
	if errorType != "" {
		span.SetAttributes(semconv.ErrorTypeKey.String(errorType))
		span.SetStatus(codes.Error, message)
	}
}

// PipelineDuration measures the executing state, from the daemon starting work
// through its last task. Transport instrumentation measures requests separately.
// IDs and action labels must not be metric dimensions. The SDK supplies exemplars.
func PipelineDuration(ctx context.Context, name string, elapsed time.Duration, result attribute.KeyValue, errorType string) {
	if s := FromContext(ctx); s != nil && s.Metrics != nil {
		attrs := []attribute.KeyValue{result}
		if errorType != "" {
			attrs = append(attrs, semconv.ErrorTypeKey.String(errorType))
		}
		s.duration.Record(ctx, elapsed.Seconds(), name, cicdconv.PipelineRunStateExecuting, attrs...)
	}
}

// End records an operation error and ends its span. A successful span keeps the
// default Unset status. Callers log a diagnostic once where they handle it.
func End(span trace.Span, err error) {
	if err != nil {
		span.SetAttributes(semconv.ErrorTypeKey.String(kindOf(err)))
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// kindOf is what went wrong as a span's error.type says it: one of a few
// kinds, found through whatever wraps the error, so that it is a
// dimension to count by. The error's own words are the span's status.
func kindOf(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	}
	return "error"
}

// Outcome records the command process's exit code, including unsuccessful exits
// for which the API transport itself succeeded.
func Outcome(span trace.Span, code int, err error) error {
	span.SetAttributes(semconv.ProcessExitCode(code))
	if err == nil && code != 0 {
		return errors.New("command exited unsuccessfully")
	}
	return err
}

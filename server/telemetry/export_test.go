package telemetry_test

import (
	"bytes"
	"context"
	"github.com/frontseat-dev/frontseat/server/testsupport/testtelemetry"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/telemetry"
)

func TestDisabledAndUnsupportedExportConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, key, value string
		wantError        bool
	}{
		{"disabled", "OTEL_SDK_DISABLED", "TRUE", false},
		{"unsupported protocol", "OTEL_EXPORTER_OTLP_PROTOCOL", "invented", true},
		{"unsupported exporter", "OTEL_TRACES_EXPORTER", "invented", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
			t.Setenv(tc.key, tc.value)
			signals, stop, err := telemetry.Setup(context.Background(), "test")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_ = stop(ctx)
			if (err != nil) != tc.wantError {
				t.Fatalf("Setup error = %v, want error %v", err, tc.wantError)
			}
			if !tc.wantError && signals != nil {
				t.Fatal("disabled SDK created providers")
			}
		})
	}
}

// Real exports prove that providers share service identity and correlate logs
// and metric exemplars with the run, with both standard OTLP transports.
func TestOTLPSignalsUseTheSameContextAndStandardFields(t *testing.T) {
	for _, protocol := range []string{"grpc", "http/protobuf"} {
		t.Run(protocol, func(t *testing.T) {
			var collector *testtelemetry.Collector
			if protocol == "grpc" {
				collector = testtelemetry.Start(t)
			} else {
				collector = testtelemetry.StartHTTP(t)
			}
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.Endpoint)
			t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", protocol)
			t.Setenv("OTEL_SERVICE_NAME", "configured-name")
			signals, stop, err := telemetry.Setup(context.Background(), "default-name")
			if err != nil {
				t.Fatal(err)
			}
			ctx, span := telemetry.Pipeline(signals.Context(context.Background()), "build", "run-123")
			traceID := span.SpanContext().TraceID()
			spanID := span.SpanContext().SpanID()
			telemetry.Output(ctx, "stderr", []byte{0xff, 'x'}, true)
			telemetry.Result(span, semconv.CICDPipelineResultFailure, "BUILD_FAILURE", "compile failed")
			telemetry.PipelineDuration(ctx, "build", time.Second, semconv.CICDPipelineResultFailure, "BUILD_FAILURE")
			span.End()
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := stop(shutdown); err != nil {
				t.Fatal(err)
			}
			data := collector.Snapshot()
			if len(data.Traces) != 1 || len(data.Logs) != 1 || len(data.Metrics) != 1 {
				t.Fatalf("missing signals: %d traces, %d logs, %d metrics", len(data.Traces), len(data.Logs), len(data.Metrics))
			}
			for _, resource := range []*resourcepb.Resource{data.Traces[0].Resource, data.Logs[0].Resource, data.Metrics[0].Resource} {
				if value(resource.Attributes, "service.name") != "configured-name" {
					t.Error("OTEL_SERVICE_NAME was not respected")
				}
				if value(resource.Attributes, "cicd.pipeline.run.id") != "" {
					t.Error("per-run identity leaked into shared resource")
				}
			}
			run := data.Traces[0].ScopeSpans[0].Spans[0]
			if run.Kind != tracepb.Span_SPAN_KIND_SERVER || value(run.Attributes, "cicd.pipeline.result") != "failure" || value(run.Attributes, "cicd.pipeline.run.id") != "run-123" || value(run.Attributes, "error.type") != "BUILD_FAILURE" {
				t.Errorf("incorrect pipeline span: %v", run)
			}
			entry := data.Logs[0].ScopeLogs[0].LogRecords[0]
			if !bytes.Equal(entry.TraceId, traceID[:]) || !bytes.Equal(entry.SpanId, spanID[:]) || !bytes.Equal(entry.Body.GetBytesValue(), []byte{0xff, 'x'}) {
				t.Errorf("output lost correlation or bytes: %v", entry)
			}
			if value(entry.Attributes, "log.iostream") != "stderr" || entry.TimeUnixNano != 0 || entry.ObservedTimeUnixNano == 0 || entry.SeverityNumber != 0 {
				t.Errorf("raw output has invented metadata: %v", entry)
			}
			metrics := data.Metrics[0].ScopeMetrics[0].Metrics
			if len(metrics) != 1 || metrics[0].Name != "cicd.pipeline.run.duration" || metrics[0].Unit != "s" {
				t.Fatalf("unexpected metrics: %v", metrics)
			}
			point := metrics[0].GetHistogram().DataPoints[0]
			if len(point.Attributes) != 4 || value(point.Attributes, "cicd.pipeline.name") != "build" || value(point.Attributes, "cicd.pipeline.run.state") != "executing" || value(point.Attributes, "cicd.pipeline.result") != "failure" || value(point.Attributes, "error.type") != "BUILD_FAILURE" {
				t.Errorf("wrong dimensions: %v", point.Attributes)
			}
			if point.Count != 1 || point.GetSum() != 1 || len(point.Exemplars) != 1 || !bytes.Equal(point.Exemplars[0].TraceId, traceID[:]) {
				t.Errorf("duration or exemplar incorrect: %v", point)
			}
		})
	}
}

func value(attrs []*commonpb.KeyValue, key string) string {
	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value.GetStringValue()
		}
	}
	return ""
}

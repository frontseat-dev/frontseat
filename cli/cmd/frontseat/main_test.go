package main

import (
	"context"
	"encoding/hex"
	"testing"

	"github.com/frontseat-dev/frontseat/server/testsupport/testtelemetry"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// A short-lived command flushes before main exits, even when argument parsing
// fails. Separate invocations have separate root traces and correlated logs.
func TestEachCommandFlushesItsOwnTrace(t *testing.T) {
	collector := testtelemetry.StartHTTP(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.Endpoint)
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	for range 2 {
		_, err := observedCommand(context.Background(), []string{"not-a-command"})
		if err == nil {
			t.Fatal("invalid command succeeded")
		}
	}
	traces := map[string]bool{}
	for _, r := range collector.Snapshot().Traces {
		for _, scope := range r.ScopeSpans {
			for _, span := range scope.Spans {
				if span.Name != "frontseat unknown" || len(span.ParentSpanId) != 0 || span.Status.GetCode() != tracepb.Status_STATUS_CODE_ERROR {
					t.Errorf("wrong command span: %v", span)
				}
				exit := int64(-1)
				for _, a := range span.Attributes {
					if a.Key == "process.exit.code" {
						exit = a.Value.GetIntValue()
					}
				}
				if exit != 2 {
					t.Errorf("exit = %d, want 2", exit)
				}
				id := hex.EncodeToString(span.TraceId)
				if traces[id] {
					t.Error("commands shared a trace")
				}
				traces[id] = true
			}
		}
	}
	if len(traces) != 2 {
		t.Fatalf("got %d command traces", len(traces))
	}
	logs := 0
	for _, r := range collector.Snapshot().Logs {
		for _, scope := range r.ScopeLogs {
			for _, entry := range scope.LogRecords {
				if !traces[hex.EncodeToString(entry.TraceId)] || len(entry.SpanId) == 0 {
					t.Error("command log lost correlation")
				}
				logs++
			}
		}
	}
	if logs != 2 {
		t.Errorf("got %d logs, want one diagnostic per command", logs)
	}
}

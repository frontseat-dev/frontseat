package testtelemetry

import (
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// TraceSpansFor returns owned spans from traces containing a span with the
// specified string attribute. Missing exports return no spans, never a match.
func (c *Collector) TraceSpansFor(key, value string) []*tracepb.Span {
	var all []*tracepb.Span
	matched := map[string]bool{}
	for _, resource := range c.Snapshot().Traces {
		for _, scope := range resource.ScopeSpans {
			for _, span := range scope.Spans {
				all = append(all, span)
				for _, attr := range span.Attributes {
					text, ok := attr.GetValue().GetValue().(*commonpb.AnyValue_StringValue)
					if attr.Key == key && ok && text.StringValue == value {
						matched[string(span.TraceId)] = true
					}
				}
			}
		}
	}
	var out []*tracepb.Span
	for _, span := range all {
		if matched[string(span.TraceId)] {
			out = append(out, span)
		}
	}
	return out
}

// RequireBool distinguishes absent/wrongly typed telemetry from false.
func RequireBool(t testing.TB, span *tracepb.Span, key string) bool {
	t.Helper()
	for _, attr := range span.GetAttributes() {
		if attr.GetKey() == key {
			value, ok := attr.GetValue().GetValue().(*commonpb.AnyValue_BoolValue)
			if !ok {
				t.Fatalf("span attribute %q must be a bool", key)
			}
			return value.BoolValue
		}
	}
	t.Fatalf("span is missing required bool attribute %s", key)
	return false
}

// RequireInt distinguishes absent/wrongly typed telemetry from zero.
func RequireInt(t testing.TB, span *tracepb.Span, key string) int64 {
	t.Helper()
	for _, attr := range span.GetAttributes() {
		if attr.GetKey() == key {
			value, ok := attr.GetValue().GetValue().(*commonpb.AnyValue_IntValue)
			if !ok {
				t.Fatalf("span attribute %q must be an integer", key)
			}
			return value.IntValue
		}
	}
	t.Fatalf("span is missing required integer attribute %s", key)
	return 0
}

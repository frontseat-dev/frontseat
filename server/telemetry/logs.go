package telemetry

import (
	"context"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// Message emits a structured diagnostic with the context's trace and span IDs.
func Message(ctx context.Context, severity log.Severity, body string, attrs ...attribute.KeyValue) {
	s := FromContext(ctx)
	if s == nil || s.logger == nil {
		return
	}
	var record log.Record
	record.SetTimestamp(time.Now())
	record.SetSeverity(severity)
	record.SetBody(attribute.StringValue(body))
	record.AddAttributes(attrs...)
	s.logger.Emit(ctx, record)
}

// Output exports action output in bounded records, preserving every byte and
// distinguishing stdout from stderr. The worker did not timestamp these bytes:
// only their observation time is known. Cached output is explicitly a replay.
func Output(ctx context.Context, stream string, content []byte, cached bool) {
	s := FromContext(ctx)
	if s == nil || s.logger == nil {
		return
	}
	const chunk = 16 * 1024
	for offset := 0; offset < len(content); {
		end := min(offset+chunk, len(content))
		part := content[offset:end]
		var record log.Record
		record.SetObservedTimestamp(time.Now())
		if utf8.Valid(part) {
			record.SetBody(attribute.StringValue(string(part)))
		} else {
			record.SetBody(attribute.ByteSliceValue(part))
		}
		record.AddAttributes(semconv.LogIostreamKey.String(stream), attribute.Bool("frontseat.log.replayed", cached), attribute.Int("frontseat.log.offset", offset))
		s.logger.Emit(ctx, record)
		offset = end
	}
}

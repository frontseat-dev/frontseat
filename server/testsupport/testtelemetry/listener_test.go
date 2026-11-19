package testtelemetry

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"testing"

	traceapi "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestCollectorServesItsGivenListenerAndSelectsOwnedTraceSpans(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c := StartHTTPOn(t, listener)
	if c.Endpoint != "http://"+listener.Addr().String() {
		t.Fatal("collector did not serve the supplied listener")
	}
	root := &tracepb.Span{TraceId: []byte("first-trace-id001"), Name: "root", Attributes: []*commonpb.KeyValue{
		{Key: "run", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "one"}}},
		{Key: "retained", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: false}}},
		{Key: "asked", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 0}}},
	}}
	child := &tracepb.Span{TraceId: bytes.Clone(root.TraceId), Name: "child"}
	other := &tracepb.Span{TraceId: []byte("other-trace-id002"), Name: "unrelated"}
	request := &traceapi.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{root, child, other}}}}}}
	body, err := proto.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(c.Endpoint+"/v1/traces", "application/x-protobuf", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		details, _ := io.ReadAll(response.Body)
		t.Fatalf("OTLP refused: %s %s", response.Status, details)
	}
	if got := c.TraceSpansFor("run", "missing"); len(got) != 0 {
		t.Fatal("absent run matched a trace")
	}
	if got := c.TraceSpansFor("absent", ""); len(got) != 0 {
		t.Fatal("absent attribute matched an empty string")
	}
	spans := c.TraceSpansFor("run", "one")
	if len(spans) != 2 || spans[0].Name != "root" || spans[1].Name != "child" {
		t.Fatalf("wrong trace: %v", spans)
	}
	if RequireBool(t, spans[0], "retained") || RequireInt(t, spans[0], "asked") != 0 {
		t.Fatal("valid zero-valued attributes changed")
	}
	spans[0].Name = "mutated"
	spans[0].TraceId[0] = 'x'
	if got := c.TraceSpansFor("run", "one"); len(got) != 2 || got[0].Name != "root" {
		t.Fatal("caller changed retained exports")
	}
}

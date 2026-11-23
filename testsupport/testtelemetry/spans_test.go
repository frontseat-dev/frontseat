package testtelemetry

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	traceapi "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestCollectorTypedObserverRefusals(t *testing.T) {
	if mode := os.Getenv("FRONTSEAT_TELEMETRY_ATTRIBUTE_REFUSAL"); mode != "" {
		span := &tracepb.Span{}
		if strings.HasSuffix(mode, "wrong") {
			span.Attributes = []*commonpb.KeyValue{{Key: "value", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "0"}}}}
		}
		if strings.HasPrefix(mode, "bool") {
			RequireBool(t, span, "value")
		} else {
			RequireInt(t, span, "value")
		}
		return
	}
	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{
		{Key: "bool", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: false}}},
		{Key: "int", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 0}}},
	}}
	if RequireBool(t, span, "bool") || RequireInt(t, span, "int") != 0 {
		t.Fatal("valid false/zero control was altered")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"bool-missing", "bool-wrong", "int-missing", "int-wrong"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestCollectorTypedObserverRefusals$", "-test.count=1")
			cmd.Env = append(os.Environ(), "FRONTSEAT_TELEMETRY_ATTRIBUTE_REFUSAL="+mode)
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("refusal child did not finish: %s", out)
			}
			if err == nil {
				t.Fatalf("observer accepted %s: %s", mode, out)
			}
			want := "span is missing required"
			if strings.HasSuffix(mode, "wrong") {
				want = "must be"
			}
			if !bytes.Contains(out, []byte(want)) {
				t.Fatalf("wrong refusal for %s: %s", mode, out)
			}
		})
	}
}

func TestCollectorTraceSelectionAndNestedOwnership(t *testing.T) {
	c := StartHTTP(t)
	span := func(id, run string) *tracepb.Span {
		return &tracepb.Span{TraceId: []byte(id), Name: "evaluate", Attributes: []*commonpb.KeyValue{{Key: "run", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: run}}}}}
	}
	first := span("trace-001", "first")
	first.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x07})
	second := span("trace-002", "second")
	wrongType := span("trace-003", "")
	wrongType.Attributes[0].Value.Value = &commonpb.AnyValue_IntValue{IntValue: 0}
	child := &tracepb.Span{TraceId: bytes.Clone(first.TraceId), Name: "plan"}
	request := &traceapi.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{first, second, wrongType, child}}}}}}
	collectorExport(t, c.Endpoint, request)
	if got := c.TraceSpansFor("run", ""); len(got) != 0 {
		t.Fatal("non-string run attribute matched empty string")
	}
	if got := c.TraceSpansFor("missing", ""); len(got) != 0 {
		t.Fatal("missing run attribute matched empty string")
	}
	if got := c.TraceSpansFor("run", "absent"); len(got) != 0 {
		t.Fatal("unexported run matched another trace")
	}
	got := c.TraceSpansFor("run", "first")
	if len(got) != 2 || got[0].Name != "evaluate" || got[1].Name != "plan" {
		t.Fatalf("wrong trace selection: %v", got)
	}
	before := proto.CloneOf(got[0])
	got[0].Attributes[0].Value.GetValue().(*commonpb.AnyValue_StringValue).StringValue = "changed"
	got[0].ProtoReflect().GetUnknown()[2] = 9
	got[0].TraceId[0] = 'X'
	if proto.Equal(before, got[0]) {
		t.Fatal("mutation control did not change returned span")
	}
	again := c.TraceSpansFor("run", "first")
	if len(again) != 2 || !proto.Equal(before, again[0]) {
		t.Fatal("returned span aliases retained attributes, unknown fields or trace ID")
	}
	if got := c.TraceSpansFor("run", "second"); len(got) != 1 || !bytes.Equal(got[0].TraceId, second.TraceId) {
		t.Fatal("second run borrowed first run's trace")
	}
}

func collectorExport(t *testing.T, endpoint string, req *traceapi.ExportTraceServiceRequest) {
	t.Helper()
	body, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(endpoint+"/v1/traces", "application/x-protobuf", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("export returned %s", response.Status)
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
}

type collectorCleanup struct {
	testing.TB
	cleanup func()
	once    sync.Once
}

func (t *collectorCleanup) Cleanup(f func()) {
	t.cleanup = func() { t.once.Do(f) }
	t.TB.Cleanup(t.cleanup)
}

type collectorListener struct {
	net.Listener
	closing chan struct{}
	reading chan struct{}
	once    sync.Once
}

// Close closes the listener, then says so: a dial after closing is
// refused, never one that lands before the close.
func (l *collectorListener) Close() error {
	err := l.Listener.Close()
	l.once.Do(func() { close(l.closing) })
	return err
}
func (l *collectorListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &collectorConn{Conn: c, reading: l.reading}, nil
}

type collectorConn struct {
	net.Conn
	header  []byte
	reading chan struct{}
	once    sync.Once
}

func (c *collectorConn) Read(p []byte) (int, error) {
	if bytes.Contains(c.header, []byte("\r\n\r\n")) {
		c.once.Do(func() { close(c.reading) })
	}
	n, err := c.Conn.Read(p)
	if !bytes.Contains(c.header, []byte("\r\n\r\n")) {
		c.header = append(c.header, p[:n]...)
	}
	return n, err
}

func TestCollectorListenerCleanupJoinsAcceptedExport(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := &collectorListener{Listener: l, closing: make(chan struct{}), reading: make(chan struct{})}
	cleanups := &collectorCleanup{TB: t}
	c := StartHTTPOn(cleanups, listener)
	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	request := &traceapi.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{Name: "accepted", TraceId: []byte("trace-0001")}}}}}}}
	body, err := proto.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(conn, "POST /v1/traces HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/x-protobuf\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", len(body)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(body[:1]); err != nil {
		t.Fatal(err)
	}
	select {
	case <-listener.reading:
	case <-time.After(3 * time.Second):
		t.Fatal("export did not begin reading its body")
	}
	done := make(chan struct{})
	go func() { cleanups.cleanup(); close(done) }()
	select {
	case <-listener.closing:
	case <-time.After(3 * time.Second):
		t.Fatal("cleanup did not close supplied listener")
	}
	select {
	case <-done:
		t.Fatal("cleanup returned while accepted export still read its body")
	default:
	}
	if extra, err := net.DialTimeout("tcp", l.Addr().String(), time.Second); err == nil {
		extra.Close()
		t.Fatal("listener still admits clients during cleanup")
	}
	if _, err := conn.Write(body[1:]); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("accepted export did not finish: %v %s", err, response.Status)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cleanup did not join finished export")
	}
	if got := c.Snapshot(); len(got.Traces) != 1 || got.Traces[0].ScopeSpans[0].Spans[0].Name != "accepted" {
		t.Fatal("accepted export disappeared during cleanup")
	}
}

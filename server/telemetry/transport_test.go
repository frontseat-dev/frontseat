package telemetry_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/frontseat-dev/frontseat/server/telemetry"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
)

func TestHTTPPreservesSampledAndUnsampledParentsAndRefusesMalformedOnes(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	signals, err := telemetry.New(provider, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(chan trace.SpanContext, 1)
	server := httptest.NewServer(signals.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := telemetry.Start(r.Context(), "work")
		seen <- trace.SpanContextFromContext(ctx)
		span.End()
		w.WriteHeader(http.StatusNoContent)
	}), "API"))
	defer server.Close()
	id, _ := trace.TraceIDFromHex("11111111111111111111111111111111")
	parent, _ := trace.SpanIDFromHex("2222222222222222")
	state, _ := trace.ParseTraceState("vendor=opaque")
	for _, flags := range []trace.TraceFlags{trace.FlagsSampled, 0} {
		before := len(recorder.Ended())
		ctx := trace.ContextWithRemoteSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: id, SpanID: parent, TraceFlags: flags, TraceState: state}))
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
		response, err := (&http.Client{Transport: telemetry.Transport{}}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		child := <-seen
		if child.TraceID() != id || child.SpanID() == parent || child.TraceFlags() != flags || child.TraceState().String() != "vendor=opaque" {
			t.Errorf("context changed: %v", child)
		}
		if flags == 0 && len(recorder.Ended()) != before {
			t.Error("unsampled parent caused recorded spans")
		}
	}
	request, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	request.Header.Set("traceparent", "not-a-trace")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if got := <-seen; !got.IsValid() || got.TraceID() == id {
		t.Error("malformed traceparent was trusted")
	}
}

func TestGridRPCsCarryTraceContextInHeaders(t *testing.T) {
	seen := make(chan string, 1)
	server := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		values := md.Get("traceparent")
		if len(values) == 1 {
			seen <- values[0]
		} else {
			seen <- ""
		}
		return handler(ctx, req)
	}))
	healthpb.RegisterHealthServer(server, health.NewServer())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	defer func() { server.Stop(); <-done }()
	provider := sdktrace.NewTracerProvider()
	defer func() { _ = provider.Shutdown(context.Background()) }()
	signals, err := telemetry.New(provider, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := telemetry.Start(signals.Context(context.Background()), "command")
	defer span.End()
	opts := append(telemetry.GRPCOptions(ctx), grpc.WithTransportCredentials(insecure.NewCredentials()))
	conn, err := grpc.NewClient(listener.Addr().String(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatal(err)
	}
	received := propagation.TraceContext{}.Extract(context.Background(), propagation.MapCarrier{"traceparent": <-seen})
	if got := trace.SpanContextFromContext(received); got.TraceID() != span.SpanContext().TraceID() || got.SpanID() == span.SpanContext().SpanID() {
		t.Error("grid RPC did not propagate its client span")
	}
}

// The transport must not modify a caller's request while injecting headers.
func TestHTTPInjectionDoesNotMutateTheRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	provider := sdktrace.NewTracerProvider()
	defer func() { _ = provider.Shutdown(context.Background()) }()
	ctx, span := provider.Tracer("test").Start(context.Background(), "command")
	defer span.End()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	response, err := (telemetry.Transport{}).RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if req.Header.Get("traceparent") != "" {
		t.Error("injection mutated the caller's headers")
	}
}

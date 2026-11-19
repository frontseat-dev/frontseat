// Package testtelemetry receives the three OpenTelemetry signals over OTLP,
// as a collector does. Tests inspect what a process actually exported.
package testtelemetry

import (
	"context"
	"net"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	logs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	traces "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	logpb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// Collector holds what clients sent, independently of their SDK or process.
type Collector struct {
	Endpoint string
	mu       sync.Mutex
	data     Data
}

// Data is a snapshot of the signals received so far.
type Data struct {
	Traces  []*tracepb.ResourceSpans
	Logs    []*logpb.ResourceLogs
	Metrics []*metricpb.ResourceMetrics
}

// Start serves OTLP/gRPC until the test ends.
func Start(t testing.TB) *Collector {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c := &Collector{Endpoint: "http://" + l.Addr().String()}
	s := grpc.NewServer()
	traces.RegisterTraceServiceServer(s, traceReceiver{c: c})
	logs.RegisterLogsServiceServer(s, logReceiver{c: c})
	metrics.RegisterMetricsServiceServer(s, metricReceiver{c: c})
	done := make(chan struct{})
	go func() { defer close(done); _ = s.Serve(l) }()
	t.Cleanup(func() { s.Stop(); <-done })
	return c
}

// StartHTTP serves OTLP/HTTP using the generated OTLP gateway and protobuf codec.
func StartHTTP(t testing.TB) *Collector {
	return startHTTP(t, nil)
}

// StartHTTPOn serves the same generated OTLP handlers on a caller-owned
// listener, including an SSH reverse-forwarded listener. Cleanup closes it.
func StartHTTPOn(t testing.TB, listener net.Listener) *Collector {
	t.Helper()
	if listener == nil {
		t.Fatal("missing OTLP listener")
	}
	return startHTTP(t, listener)
}

func startHTTP(t testing.TB, listener net.Listener) *Collector {
	t.Helper()
	c := &Collector{}
	mux := runtime.NewServeMux(runtime.WithMarshalerOption("application/x-protobuf", &runtime.ProtoMarshaller{}))
	ctx := context.Background()
	for _, err := range []error{
		traces.RegisterTraceServiceHandlerServer(ctx, mux, traceReceiver{c: c}),
		logs.RegisterLogsServiceHandlerServer(ctx, mux, logReceiver{c: c}),
		metrics.RegisterMetricsServiceHandlerServer(ctx, mux, metricReceiver{c: c}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewUnstartedServer(mux)
	if listener != nil {
		_ = srv.Listener.Close()
		srv.Listener = listener
	}
	srv.Start()
	t.Cleanup(srv.Close)
	c.Endpoint = srv.URL
	return c
}

// Snapshot returns copies, so exports may continue while a test reads them.
func (c *Collector) Snapshot() Data {
	c.mu.Lock()
	defer c.mu.Unlock()
	var d Data
	for _, v := range c.data.Traces {
		d.Traces = append(d.Traces, proto.Clone(v).(*tracepb.ResourceSpans))
	}
	for _, v := range c.data.Logs {
		d.Logs = append(d.Logs, proto.Clone(v).(*logpb.ResourceLogs))
	}
	for _, v := range c.data.Metrics {
		d.Metrics = append(d.Metrics, proto.Clone(v).(*metricpb.ResourceMetrics))
	}
	return d
}

type traceReceiver struct {
	traces.UnimplementedTraceServiceServer
	c *Collector
}

func (r traceReceiver) Export(_ context.Context, req *traces.ExportTraceServiceRequest) (*traces.ExportTraceServiceResponse, error) {
	r.c.mu.Lock()
	defer r.c.mu.Unlock()
	r.c.data.Traces = append(r.c.data.Traces, req.ResourceSpans...)
	return &traces.ExportTraceServiceResponse{}, nil
}

type logReceiver struct {
	logs.UnimplementedLogsServiceServer
	c *Collector
}

func (r logReceiver) Export(_ context.Context, req *logs.ExportLogsServiceRequest) (*logs.ExportLogsServiceResponse, error) {
	r.c.mu.Lock()
	defer r.c.mu.Unlock()
	r.c.data.Logs = append(r.c.data.Logs, req.ResourceLogs...)
	return &logs.ExportLogsServiceResponse{}, nil
}

type metricReceiver struct {
	metrics.UnimplementedMetricsServiceServer
	c *Collector
}

func (r metricReceiver) Export(_ context.Context, req *metrics.ExportMetricsServiceRequest) (*metrics.ExportMetricsServiceResponse, error) {
	r.c.mu.Lock()
	defer r.c.mu.Unlock()
	r.c.data.Metrics = append(r.c.data.Metrics, req.ResourceMetrics...)
	return &metrics.ExportMetricsServiceResponse{}, nil
}

package telemetry

import (
	"context"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
)

type clientTransport struct{ http.RoundTripper }

func newClientTransport(s *Signals) *clientTransport {
	opts := []otelhttp.Option{otelhttp.WithPropagators(propagation.TraceContext{})}
	if s.Traces != nil {
		opts = append(opts, otelhttp.WithTracerProvider(s.Traces))
	} else {
		opts = append(opts, otelhttp.WithTracerProvider(tracenoop.NewTracerProvider()))
	}
	if s.Metrics != nil {
		opts = append(opts, otelhttp.WithMeterProvider(s.Metrics))
	} else {
		opts = append(opts, otelhttp.WithMeterProvider(noop.NewMeterProvider()))
	}
	return &clientTransport{otelhttp.NewTransport(http.DefaultTransport, opts...)}
}

// Transport instruments requests using their command's providers. It also
// propagates explicitly supplied caller spans without requiring a global SDK.
type Transport struct{}

func (Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if s := FromContext(r.Context()); s != nil {
		return s.transport.RoundTrip(r)
	}
	// An API caller may supply its own SDK context, as agents do.
	sent := r.Clone(r.Context())
	propagation.TraceContext{}.Inject(sent.Context(), propagation.HeaderCarrier(sent.Header))
	return http.DefaultTransport.RoundTrip(sent)
}

// Handler continues incoming traces and gives API work its process's providers.
func (s *Signals) Handler(next http.Handler, name string) http.Handler {
	if s == nil {
		return next
	}
	tp := s.Traces
	if tp == nil {
		tp = tracenoop.NewTracerProvider()
	}
	mp := s.Metrics
	if mp == nil {
		mp = noop.NewMeterProvider()
	}
	return otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(s.Context(r.Context())))
	}), name, otelhttp.WithTracerProvider(tp), otelhttp.WithMeterProvider(mp), otelhttp.WithPropagators(propagation.TraceContext{}))
}

// GRPCOptions instrument grid RPCs using the same providers and W3C propagator
// as HTTP. No trace data is put in the REAPI Command or Action messages.
func GRPCOptions(ctx context.Context) []grpc.DialOption {
	s := FromContext(ctx)
	if s == nil {
		return nil
	}
	tp := s.Traces
	if tp == nil {
		tp = tracenoop.NewTracerProvider()
	}
	mp := s.Metrics
	if mp == nil {
		mp = noop.NewMeterProvider()
	}
	return []grpc.DialOption{grpc.WithStatsHandler(otelgrpc.NewClientHandler(otelgrpc.WithTracerProvider(tp), otelgrpc.WithMeterProvider(mp), otelgrpc.WithPropagators(propagation.TraceContext{})))}
}

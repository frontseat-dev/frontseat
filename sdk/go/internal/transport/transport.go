// Package transport is how the SDK reaches a server: one Connect client
// over HTTP, which carries the caller's trace to the server, and a
// tunnel to a port.
package transport

import (
	"net/http"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"go.opentelemetry.io/otel/propagation"

	"github.com/frontseat-dev/frontseat/sdk/go/bearer"
)

// Reach is how a server is reached: the HTTP client requests are made
// with, nil for the SDK's own, and the token presented with them, to
// Origin alone, where there is one.
type Reach struct {
	HTTP   *http.Client
	Token  string
	Origin string
}

// New connects API requests to an endpoint, presenting the token where
// the endpoint is of the origin, and nowhere else.
func New(endpoint string, reach Reach) *connect.Client {
	over := reach.HTTP
	if over == nil {
		over = &http.Client{Transport: traced{}}
	}
	return connect.NewClient(connecthttp.NewTransport(bearer.Client(over, reach.Token, reach.Origin), endpoint))
}

// traced makes a request with Go's own transport, carrying the trace of
// the request's context, as W3C trace context headers: a caller with a
// tracer of its own, as an agent has, is followed into the server. A
// caller that instruments its requests itself gives its own client
// (Reach.HTTP), and this is not used.
type traced struct{}

func (traced) RoundTrip(r *http.Request) (*http.Response, error) {
	carried := r.Clone(r.Context())
	propagation.TraceContext{}.Inject(carried.Context(), propagation.HeaderCarrier(carried.Header))
	return http.DefaultTransport.RoundTrip(carried)
}

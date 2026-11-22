// Package transport is how the SDK reaches a server: one Connect client
// over HTTP, which carries the caller's trace to the server, and a
// tunnel to a port.
package transport

import (
	"context"
	"net/http"
	"net/url"
	"slices"

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
// the endpoint is of the origin, and nowhere else, through the
// interceptors given. Where the token would
// go in clear to another machine, every call is refused before an HTTP
// client is asked, FAILED_PRECONDITION with bearer.ErrInClear as its
// cause, by an error that names neither the address nor the token: an
// HTTP client names the address of every request it fails.
func New(endpoint string, reach Reach, interceptors ...connect.ClientInterceptor) *connect.Client {
	over := reach.HTTP
	if over == nil {
		over = &http.Client{Transport: traced{}}
	}
	connected := connecthttp.NewTransport(bearer.Client(over, reach.Token, reach.Origin), endpoint)
	if reach.Token != "" && inClear(endpoint, reach.Origin) {
		return connect.NewClient(connected, slices.Concat(interceptors, []connect.ClientInterceptor{refusedInClear})...)
	}
	return connect.NewClient(connected, interceptors...)
}

// inClear is whether requests to an endpoint of an origin would carry
// its token in clear to another machine.
func inClear(endpoint, origin string) bool {
	at, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	of, err := url.Parse(origin)
	return err == nil && bearer.Of(at, of) && !bearer.Private(at)
}

// refusedInClear refuses every call: its token would go in clear.
func refusedInClear(connect.ClientFunc) connect.ClientFunc {
	return func(context.Context, connect.Spec) (connect.ClientStream, error) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, bearer.ErrInClear.Error()).WithCause(bearer.ErrInClear)
	}
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

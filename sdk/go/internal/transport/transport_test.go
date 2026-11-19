package transport_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/otel/trace"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	"github.com/frontseat-dev/frontseat/sdk/go/internal/transport"
)

// told is a server that says what each request carried of a trace.
func told(t *testing.T) (string, <-chan string) {
	t.Helper()
	carried := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		carried <- r.Header.Get("traceparent")
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	return server.URL, carried
}

// A request carries the trace of the context it is made in, so that a
// caller with a tracer of its own is followed into the server; one made
// in no trace carries none.
func TestARequestCarriesTheTraceOfItsContext(t *testing.T) {
	url, carried := told(t)
	runs := buildv1alpha1connect.NewBuildServiceClient(transport.New(url, transport.Reach{}))
	id, _ := trace.TraceIDFromHex("11111111111111111111111111111111")
	span, _ := trace.SpanIDFromHex("2222222222222222")
	traced := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: id, SpanID: span, TraceFlags: trace.FlagsSampled}))
	_, _ = runs.ListBuilds(traced, &buildv1.ListBuildsRequest{})
	if got, want := <-carried, "00-11111111111111111111111111111111-2222222222222222-01"; got != want {
		t.Errorf("a traced request carried %q, want %q", got, want)
	}
	_, _ = runs.ListBuilds(context.Background(), &buildv1.ListBuildsRequest{})
	if got := <-carried; got != "" {
		t.Errorf("a request in no trace carried %q", got)
	}
}

// countingTransport counts the requests made through it.
type countingTransport struct{ made atomic.Int32 }

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.made.Add(1)
	return http.DefaultTransport.RoundTrip(r)
}

// A caller's HTTP client is the one requests are made with, as a program
// instruments them; without it, none of the caller's is.
func TestRequestsAreMadeWithTheCallersHTTPClient(t *testing.T) {
	url, carried := told(t)
	counting := &countingTransport{}
	runs := buildv1alpha1connect.NewBuildServiceClient(transport.New(url, transport.Reach{HTTP: &http.Client{Transport: counting}}))
	_, _ = runs.ListBuilds(context.Background(), &buildv1.ListBuildsRequest{})
	<-carried
	if made := counting.made.Load(); made != 1 {
		t.Errorf("the caller's client made %d requests, want 1", made)
	}
	plain := buildv1alpha1connect.NewBuildServiceClient(transport.New(url, transport.Reach{}))
	_, _ = plain.ListBuilds(context.Background(), &buildv1.ListBuildsRequest{})
	<-carried
	if made := counting.made.Load(); made != 1 {
		t.Errorf("a client given no HTTP client made %d requests with the caller's, want none more", made-1)
	}
}

// A client given a token presents it with every request and every
// tunnel it opens, as a bearer's, through the caller's HTTP client too;
// one given none presents none.
func TestATokenIsPresentedWhereOneIsGiven(t *testing.T) {
	presented := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	counting := &countingTransport{}
	for _, reach := range []transport.Reach{
		{Token: "a-token", Origin: server.URL},
		{HTTP: &http.Client{Transport: counting}, Token: "a-token", Origin: server.URL},
	} {
		runs := buildv1alpha1connect.NewBuildServiceClient(transport.New(server.URL, reach))
		_, _ = runs.ListBuilds(context.Background(), &buildv1.ListBuildsRequest{})
		if got := <-presented; got != "Bearer a-token" {
			t.Errorf("a request of a client given a token presented %q", got)
		}
		_, _ = transport.Tunnel(context.Background(), "ws"+server.URL[len("http"):]+"/a/tunnel", reach)
		if got := <-presented; got != "Bearer a-token" {
			t.Errorf("a tunnel of a client given a token presented %q", got)
		}
	}
	if counting.made.Load() != 2 {
		t.Errorf("the caller's client made %d requests, want the call and the tunnel", counting.made.Load())
	}
	plain := buildv1alpha1connect.NewBuildServiceClient(transport.New(server.URL, transport.Reach{}))
	_, _ = plain.ListBuilds(context.Background(), &buildv1.ListBuildsRequest{})
	if got := <-presented; got != "" {
		t.Errorf("a client given no token presented %q", got)
	}
}

// A server that redirects leads a client given a token nowhere: the
// token is presented to the endpoint alone, and the redirect is the
// answer. The control is the endpoint, presented the token.
func TestATokenIsNotLedElsewhereByARedirect(t *testing.T) {
	redirected(t, func(origin string) error {
		runs := buildv1alpha1connect.NewBuildServiceClient(transport.New(origin, transport.Reach{Token: "a-token", Origin: origin}))
		_, err := runs.ListBuilds(context.Background(), &buildv1.ListBuildsRequest{})
		return err
	})
}

// redirected has a call made to a server that redirects elsewhere, and
// fails the test unless the call failed, the server was presented the
// token, and elsewhere was never asked.
func redirected(t *testing.T, call func(origin string) error) {
	t.Helper()
	elsewhere := make(chan string, 4)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere <- r.Header.Get("Authorization")
	}))
	t.Cleanup(other.Close)
	presented := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented <- r.Header.Get("Authorization")
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	if err := call(server.URL); err == nil {
		t.Error("a redirect was taken for an answer")
	}
	if got := <-presented; got != "Bearer a-token" {
		t.Errorf("the endpoint was presented %q", got)
	}
	select {
	case got := <-elsewhere:
		t.Errorf("the redirect was followed, presenting %q", got)
	default:
	}
}

// A token is presented to its origin alone: an address of another
// origin, as a server may answer with one, is reached without it, by a
// call and by a tunnel alike; one of the origin, beneath it, is
// presented it. The control is the origin itself.
func TestATokenIsPresentedToItsOriginAlone(t *testing.T) {
	presented := make(chan string, 8)
	recording := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNotFound)
	})
	origin := httptest.NewServer(recording)
	t.Cleanup(origin.Close)
	other := httptest.NewServer(recording)
	t.Cleanup(other.Close)
	reach := transport.Reach{Token: "a-token", Origin: origin.URL}
	for address, want := range map[string]string{
		origin.URL:                         "Bearer a-token",
		origin.URL + "/workspaces/x/build": "Bearer a-token",
		other.URL:                          "",
		other.URL + "/workspaces/x/build":  "",
	} {
		runs := buildv1alpha1connect.NewBuildServiceClient(transport.New(address, reach))
		_, _ = runs.ListBuilds(context.Background(), &buildv1.ListBuildsRequest{})
		if got := <-presented; got != want {
			t.Errorf("a call to %s presented %q, want %q", address, got, want)
		}
		_, _ = transport.Tunnel(context.Background(), address+"/shell", reach)
		if got := <-presented; got != want {
			t.Errorf("a tunnel to %s presented %q, want %q", address, got, want)
		}
	}
}

// A tunnel follows no redirect: the token is presented to its origin
// alone, and the redirect is no tunnel. The control is the origin,
// presented it.
func TestATunnelIsNotLedElsewhereByARedirect(t *testing.T) {
	redirected(t, func(origin string) error {
		conn, err := transport.Tunnel(context.Background(), origin+"/shell", transport.Reach{Token: "a-token", Origin: origin})
		if err == nil {
			conn.Close()
		}
		return err
	})
}

// An origin is its scheme, its host and its port alike: the same host
// and port over another scheme, or another port, is another origin, and
// no origin at all is presented nothing.
func TestAnOriginIsItsSchemeHostAndPort(t *testing.T) {
	for origin, address := range map[string]string{
		"https://127.0.0.1:1": "ws://127.0.0.1:1/shell",
		"ftp://127.0.0.1:1":   "http://127.0.0.1:1/shell",
		"http://127.0.0.1:2":  "http://127.0.0.1:1/shell",
		"":                    "http://127.0.0.1:1/shell",
		"not a url":           "http://127.0.0.1:1/shell",
	} {
		if transport.Presents(address, transport.Reach{Token: "a-token", Origin: origin}) {
			t.Errorf("%s was presented the token of %q", address, origin)
		}
	}
	if !transport.Presents("ws://127.0.0.1:1/shell", transport.Reach{Token: "a-token", Origin: "http://127.0.0.1:1"}) {
		t.Error("the control: the origin's own tunnel was not presented its token")
	}
}

// Package bearer presents a token to one origin, as a bearer's, and to
// no other: a request to another scheme, host or port goes without it,
// and no redirect is followed, which would take a request and its token
// where the server chose. The SDK's clients present a person's token so,
// and a server that presents a token of its own to another service does
// the same with this package.
package bearer

import (
	"net/http"
	"net/url"
	"strings"
)

// Client returns an HTTP client that presents a token with every request
// to an origin, its scheme, host and port, and with no other, and that
// follows no redirect. The client given is not changed; nil is Go's own.
// With no token, it is the client given, as it is: there is nothing to
// present, and it follows redirects as that client does.
// An origin that is no URL with a host is presented nothing.
func Client(over *http.Client, token, origin string) *http.Client {
	if over == nil {
		over = http.DefaultClient
	}
	if token == "" {
		return over
	}
	bearing := *over
	next := over.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	at, err := url.Parse(origin)
	if err != nil || at.Host == "" {
		at = nil
	}
	bearing.Transport = presenting{next: next, token: token, origin: at}
	bearing.CheckRedirect = Unfollowed
	return &bearing
}

// Unfollowed follows no redirect, as an HTTP client's CheckRedirect: the
// answer is the redirect itself.
func Unfollowed(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// presenting presents a token with each request to one origin.
type presenting struct {
	next   http.RoundTripper
	token  string
	origin *url.URL
}

func (p presenting) RoundTrip(r *http.Request) (*http.Response, error) {
	if p.origin == nil || !Of(r.URL, p.origin) {
		return p.next.RoundTrip(r)
	}
	carried := r.Clone(r.Context())
	Present(carried.Header, p.token)
	return p.next.RoundTrip(carried)
}

// Present sets a token in headers as a bearer's (Authorization: Bearer).
func Present(headers http.Header, token string) {
	headers.Set("Authorization", "Bearer "+token)
}

// Of is whether an address is of an origin: the same scheme, host and
// port, a WebSocket's address (ws, wss) being of the HTTP origin it is
// opened over (http, https). An address or an origin with no host is of
// none.
func Of(address, origin *url.URL) bool {
	if address == nil || origin == nil || address.Host == "" || origin.Host == "" {
		return false
	}
	return strings.EqualFold(httpScheme(address.Scheme), httpScheme(origin.Scheme)) && strings.EqualFold(address.Host, origin.Host)
}

// httpScheme is the HTTP scheme a WebSocket's is opened over, and any
// other scheme as it is.
func httpScheme(scheme string) string {
	switch strings.ToLower(scheme) {
	case "ws":
		return "http"
	case "wss":
		return "https"
	}
	return scheme
}

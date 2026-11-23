// Package backstage resolves entities through a Backstage catalog: what
// a control plane does with the catalog its operator gives it, to learn
// which repositories and directories hold the entities a workspace is
// asked for, and the components they depend on. It never reads a
// catalog file and never follows where an answer points: it asks the
// one catalog API it was given, at its origin alone.
//
// Every call is made so that the catalog's address and its answers lead
// nowhere else: the token goes to the catalog's scheme and host alone
// (sdk/go/bearer), no redirect is followed, the address is dialled only
// where it may be (never an address of no one host, this machine only
// where the operator said so), through no proxy of the environment and
// with no cookie, over TLS 1.2 or later; an answer is read to 4 MiB, a
// request waited for 20 s and a resolution 60 s, and a resolution takes
// at most 2,000 entities. What the catalog names is checked before it is
// kept: entity references by the entity package, and where an entity's
// source is as a workspace holds a repository. A refusal says by its
// reason what went wrong, and never the token, nor a value it refused.
package backstage

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/frontseat-dev/frontseat/core/secretfile"
	"github.com/frontseat-dev/frontseat/sdk/go/bearer"
)

// The reasons a resolution is refused by, as ErrorInfo of the workspace
// API's domain says them.
const (
	// NotFound is an entity the catalog does not hold, or a system or a
	// group that holds nothing.
	NotFound = "ENTITY_NOT_FOUND"
	// Ambiguous is a name that names several entities.
	Ambiguous = "ENTITY_AMBIGUOUS"
	// Invalid is a name that names no entity, or an entity whose source
	// the catalog says as nothing a workspace can hold.
	Invalid = "ENTITY_INVALID"
	// Unavailable is a catalog that did not answer as one does.
	Unavailable = "ENTITIES_UNAVAILABLE"
)

// Refused is a resolution refused, by its reason.
type Refused struct {
	// Reason is one of the reasons above.
	Reason string
	// Message says what was refused, in words that hold nothing the
	// catalog was not checked to say plainly.
	Message string
}

func (r *Refused) Error() string { return r.Message }

func refused(reason, format string, a ...any) error {
	return &Refused{Reason: reason, Message: fmt.Sprintf(format, a...)}
}

// Config is where a catalog is, as its operator gives it.
type Config struct {
	// Base is Backstage's address, https://HOST[:PORT][/PATH]; the catalog
	// API is beneath it, at /api/catalog. It is http only at this
	// machine, and only with Loopback.
	Base string
	// Token is presented to Base's origin as a bearer's; empty for none.
	Token string
	// Loopback lets the catalog be at this machine: dialled at a loopback
	// address, and asked over http there. For a local stack and tests.
	Loopback bool
}

// maxAnswer is the most an answer may hold.
const maxAnswer = 4 << 20

// limits are how long and how much a catalog is waited for and taken.
type limits struct {
	dial, handshake, request, resolution time.Duration
	// entities is the most a resolution takes; page how many a query
	// asks for at once.
	entities, page int
}

// Catalog is a Backstage catalog, asked over its API.
type Catalog struct {
	api      *url.URL
	origin   *url.URL
	token    string
	loopback bool
	limits   limits
	// trusted are the authorities its certificate is held to; nil for
	// the machine's.
	trusted *x509.CertPool
	client  *http.Client
}

// New returns the catalog at an address, or why the address is none a
// catalog is asked at. Nothing is asked until a resolution.
func New(c Config) (*Catalog, error) {
	at, err := url.Parse(c.Base)
	if err != nil || at.Opaque != "" || at.Host == "" || at.Hostname() == "" {
		return nil, errors.New("the Backstage address is no URL with a host: it is written https://HOST[:PORT][/PATH]")
	}
	if at.User != nil || at.RawQuery != "" || at.ForceQuery || at.Fragment != "" {
		return nil, errors.New("the Backstage address holds a user, a password, a query or a fragment: it is written https://HOST[:PORT][/PATH], and its token is given in a file")
	}
	switch {
	case at.Scheme == "https":
	case at.Scheme == "http" && c.Loopback && bearer.Private(at):
	case at.Scheme == "http":
		return nil, errors.New("the Backstage address is http, which is served only at this machine, with --backstage-loopback: give its https address")
	default:
		return nil, errors.New("the Backstage address is neither https nor http")
	}
	api := *at
	api.Path = strings.TrimSuffix(at.Path, "/") + "/api/catalog"
	api.RawPath = ""
	out := &Catalog{
		api: &api, origin: &url.URL{Scheme: at.Scheme, Host: at.Host}, token: c.Token, loopback: c.Loopback,
		limits: limits{dial: 5 * time.Second, handshake: 5 * time.Second, request: 20 * time.Second, resolution: 60 * time.Second, entities: 2000, page: 100},
	}
	out.client = out.made()
	return out, nil
}

// made is the HTTP client the catalog is asked with: its own transport,
// with nothing of the environment's, which dials only where it may, and
// presents the token to the catalog's origin alone and follows no
// redirect.
func (c *Catalog) made() *http.Client {
	dialer := &net.Dialer{Timeout: c.limits.dial, Control: c.dialled}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   c.limits.handshake,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: c.trusted},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: c.limits.request,
	}
	over := &http.Client{Transport: transport, Timeout: c.limits.request, CheckRedirect: bearer.Unfollowed}
	return bearer.NewClient(over, c.token, c.origin)
}

// errUndialled is an address the catalog is not dialled at.
var errUndialled = errors.New("an address the catalog may not be at")

// dialled holds the address a connection is about to be made to, once
// its name is resolved: never one of no one host (link-local,
// unspecified, multicast), and this machine only where the operator
// said so.
func (c *Catalog) dialled(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return errUndialled
	}
	ip := net.ParseIP(host)
	switch {
	case ip == nil, ip.IsUnspecified(), ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast(), ip.IsInterfaceLocalMulticast(), ip.IsMulticast():
		return errUndialled
	case ip.IsLoopback() && !c.loopback:
		return errUndialled
	}
	return nil
}

// get asks the catalog API for a path beneath it, with a query, and
// decodes its answer into out; found is false where it answered that it
// holds nothing there. What is asked is said in a refusal by what, never
// by the address.
func (c *Catalog) get(ctx context.Context, path string, query url.Values, what string, out any) (found bool, err error) {
	at := *c.api
	at.Path += path
	at.RawPath = ""
	at.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, at.String(), nil)
	if err != nil {
		return false, refused(Invalid, "%s cannot be asked of the catalog", what)
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		var cause string
		switch {
		case errors.Is(err, errUndialled):
			cause = "its address is one it may not be at (this machine is said with --backstage-loopback)"
		case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || isTimeout(err):
			cause = "it did not answer in time"
		default:
			cause = "it could not be reached"
		}
		return false, refused(Unavailable, "the catalog was asked for %s, and %s", what, cause)
	}
	defer response.Body.Close()
	switch status := response.StatusCode; {
	case status == http.StatusOK:
	case status == http.StatusNotFound:
		return false, nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		sent := "no token was presented: give the control plane one (--backstage-token)"
		if c.token != "" {
			sent = "a token was presented, which it does not take"
		}
		return false, refused(Unavailable, "the catalog refused the control plane when asked for %s (%d): %s", what, status, sent)
	case status >= 300 && status < 400:
		return false, refused(Unavailable, "the catalog answered %s with a redirect, which is not followed", what)
	default:
		return false, refused(Unavailable, "the catalog answered %d when asked for %s", status, what)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxAnswer+1))
	if err != nil {
		return false, refused(Unavailable, "the catalog's answer for %s was not read whole", what)
	}
	if len(raw) > maxAnswer {
		return false, refused(Unavailable, "the catalog answered more than 4 MiB for %s", what)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return false, refused(Unavailable, "the catalog's answer for %s is not what the catalog API answers", what)
	}
	return true, nil
}

// isTimeout is whether an error is a network operation that took too
// long.
func isTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

// maxToken is the longest token a file may hold.
const maxToken = 4 << 10

// ReadToken reads the token a server presents to Backstage from a file
// an operator gives it: one line of characters that are no space, at
// most 4 KiB, in a file and a directory that are the server's alone
// (secretfile). What the file holds is never said.
func ReadToken(file string) (string, error) {
	raw, err := secretfile.Read(file, "the Backstage token", maxToken+2)
	if err != nil {
		return "", err
	}
	token := strings.TrimSuffix(string(raw), "\n")
	if token == "" || len(token) > maxToken || strings.IndexFunc(token, func(r rune) bool { return r <= ' ' || r == 0x7f || r > 0x7e }) >= 0 {
		return "", fmt.Errorf("%s holds no Backstage token: one is a line of at most 4 KiB of printable characters that are no space", file)
	}
	return token, nil
}

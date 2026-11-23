// Package frontseat is the Go SDK of Frontseat: one Client, made for a
// server, from which everything is reached.
//
//	fs, err := frontseat.New("https://frontseat.example", frontseat.WithToken(token))
//	opened, err := fs.Open(ctx, frontseat.WorkspaceSpec{Entities: patterns})
//	shell, err := opened.Workspace.Shell(ctx)
//
// A Client is of a control plane, which keeps workspaces (Workspace,
// Open, Workspaces) and a person's credentials (Credentials), and
// carries its owner to a workspace's shell and to a port of its sandbox
// (Shell, Forward). A workspace is built from inside it: there, as on
// any machine, a build daemon serves the build API on the loopback, and
// a Client made for that daemon, with the daemon's token, reaches what
// it builds with Here:
//
//	daemon, err := frontseat.New("http://127.0.0.1:"+port, frontseat.WithToken(daemonToken))
//	build, err := daemon.Here().Build(ctx, frontseat.BuildRequest{Targets: []string{"gateway"}})
//	for event, err := range build.Events(ctx, 0) { ... }
//	ended, err := build.Wait(ctx)
//
// The workspace Here is builds (Build, Test), runs programs (Run),
// releases (PlanRelease, Release), and says what it is made of
// (Entities, Projects). A workspace a control plane keeps does none of
// that, and says so (ErrBuiltInside).
//
// What the SDK hands back is the API's own messages, generated from its
// contract into the packages beneath api/; they stay there for a
// program that wants the wire itself. Every list and every log is an
// iter.Seq2, which Collect gathers into a slice.
//
// Every call that a server refuses fails with an Error, which says the
// canonical code and, where the server refused it by name, the reason
// (ReasonWorkspaceNotFound and the rest), matched with errors.As or,
// against the sentinels (ErrWorkspaceNotFound), errors.Is. The examples
// run against a server of their own on this machine.
package frontseat

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"connectrpc.com/connect/v2"

	"github.com/frontseat-dev/frontseat/sdk/go/bearer"
	"github.com/frontseat-dev/frontseat/sdk/go/internal/transport"
)

// Option changes how a Client reaches its server.
type Option struct {
	apply func(*transport.Reach)
}

// WithToken has a Client present a token with every request it makes,
// and every tunnel it opens, as a bearer's (Authorization: Bearer): the
// token a control plane knows the caller by. It is presented to the
// server the Client is made for, and to no other origin: not to an
// address a server answers with, and not where a server redirects,
// which is never followed.
func WithToken(token string) Option {
	return Option{apply: func(r *transport.Reach) { r.Token = token }}
}

// WithHTTPClient has a Client make its requests, and open its tunnels,
// with an HTTP client of the caller's: one its program instruments, or
// one that trusts another certificate. Without it, requests are made
// with Go's own transport and carry the trace of the context they are
// made in. The client given is not changed. A Client that presents a
// token (WithToken) follows no redirect, whatever the caller's client
// says of redirects; one that presents none follows them as the
// caller's client does.
func WithHTTPClient(client *http.Client) Option {
	return Option{apply: func(r *transport.Reach) { r.HTTP = client }}
}

// Client is a client of one Frontseat server: a control plane, or a
// build daemon, inside a workspace's sandbox or on any machine.
type Client struct {
	server *url.URL
	reach  transport.Reach
	api    *connect.Client
}

// New returns a Client of the server at an address: a URL of http or
// https and a host, such as http://127.0.0.1:7700, with no user, no
// path but /, no query and no fragment; a trailing / is left out.
// Nothing is asked of the server until a call is made. An address that
// is none of that is refused, and so is a Client that would present its
// token (WithToken) in clear to another machine: over http to a host
// that is not this machine's (bearer.ErrInClear). A control plane on
// another machine is named with https.
func New(server string, options ...Option) (*Client, error) {
	at, err := address(server)
	if err != nil {
		return nil, err
	}
	var reach transport.Reach
	for _, option := range options {
		if option.apply != nil {
			option.apply(&reach)
		}
	}
	if reach.Token != "" && !bearer.Private(at) {
		return nil, bearer.ErrInClear
	}
	reach.Origin = at.String()
	return &Client{server: at, reach: reach, api: transport.New(reach.Origin, reach, errorsRead)}, nil
}

// address is the server an address names, or why it names none.
func address(server string) (*url.URL, error) {
	at, err := url.Parse(server)
	switch {
	case err != nil:
		return nil, fmt.Errorf("a server is named by a URL, http://host:port or https://host: %w", err)
	case at.Scheme != "http" && at.Scheme != "https":
		return nil, fmt.Errorf("a server is named by a URL of http or https, and %q is not one", server)
	case at.Host == "":
		return nil, fmt.Errorf("a server is named by a URL with a host, and %q has none", server)
	case at.User != nil:
		return nil, errors.New("a server is named with no user: a token is given with WithToken")
	case at.RawQuery != "" || at.ForceQuery || at.Fragment != "" || strings.Contains(server, "#"):
		return nil, fmt.Errorf("a server is named by its scheme, host and port alone, and %q says more", server)
	case at.Path != "" && at.Path != "/":
		return nil, fmt.Errorf("a server is named by its scheme, host and port alone, and %q names a path", server)
	}
	return &url.URL{Scheme: at.Scheme, Host: at.Host}, nil
}

// Server is the address the Client was made for, as New read it:
// http://127.0.0.1:7700.
func (c *Client) Server() string { return c.server.String() }

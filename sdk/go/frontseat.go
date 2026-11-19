// Package frontseat is the Go SDK of Frontseat: one Client, made for a
// server, from which everything is reached.
//
//	fs := frontseat.New("https://frontseat.example", frontseat.WithToken(token))
//	ws := fs.Workspace("payments")
//	build, err := ws.Build(ctx, frontseat.BuildRequest{Targets: []string{"gateway"}})
//	for event, err := range build.Events(ctx, 0) { ... }
//	ended, err := build.Wait(ctx)
//
// A Client is of a control plane, which keeps workspaces (Workspace,
// Open, Workspaces) and a person's credentials (Credentials). A
// Workspace builds (Build, Test), runs programs (Run), releases
// (PlanRelease, Release), and says what it is made of (Entities,
// Projects). Inside a workspace's sandbox, a Client of the daemon there
// reaches its workspace with Here.
//
// What the SDK hands back is the API's own messages, generated from its
// contract into the packages beneath api/; they stay there for a
// program that wants the wire itself, which Received and Listed read to
// their ends.
package frontseat

import (
	"net/http"

	"connectrpc.com/connect/v2"

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
// made in.
func WithHTTPClient(client *http.Client) Option {
	return Option{apply: func(r *transport.Reach) { r.HTTP = client }}
}

// Client is a client of one Frontseat server: a control plane, or,
// inside a workspace's sandbox, the daemon there.
type Client struct {
	server string
	reach  transport.Reach
	api    *connect.Client
}

// New returns a Client of the server at an address, a URL such as
// http://127.0.0.1:7700. Nothing is asked of the server until a call is
// made.
func New(server string, options ...Option) *Client {
	reach := transport.Reach{Origin: server}
	for _, option := range options {
		if option.apply != nil {
			option.apply(&reach)
		}
	}
	reach.Origin = server
	return &Client{server: server, reach: reach, api: transport.New(server, reach)}
}

// Server is the address the Client was made for.
func (c *Client) Server() string { return c.server }

// at is a connection to an address the server gave, presenting the
// Client's token where the address is of the server's origin alone.
func (c *Client) at(address string) *connect.Client {
	if address == c.server {
		return c.api
	}
	return transport.New(address, c.reach)
}

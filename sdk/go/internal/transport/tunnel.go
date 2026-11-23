package transport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/coder/websocket"
	"go.opentelemetry.io/otel/propagation"

	"github.com/frontseat-dev/frontseat/sdk/go/bearer"
)

// message is the most one message of a tunnel may hold; see the
// server's.
const message = 1 << 20

// Tunnel returns a connection to what a server serves at an address: a
// port of a sandbox, or a workspace's shell. It is a WebSocket of binary
// messages (coder/websocket), so it goes wherever the server's address
// leads, with or without TLS and through what stands in front, over the
// HTTP client reach names or the machine's own. The token is presented
// where the address is of reach's origin, and a tunnel that would carry
// it in clear to another machine is not opened (bearer.ErrInClear). The
// connection lasts as long as the context. Where the server refuses the
// tunnel as it refuses a call, the error is that refusal, a
// *connect.Error with its details.
func Tunnel(ctx context.Context, address string, reach Reach) (net.Conn, error) {
	headers := make(http.Header)
	propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(headers))
	over := reach.HTTP
	if Presents(address, reach) {
		if at, err := url.Parse(address); err != nil || !bearer.Private(at) {
			return nil, bearer.ErrInClear
		}
		bearer.Present(headers, reach.Token)
		// A redirect is not followed: it would take the token where the
		// server chose.
		if over == nil {
			over = http.DefaultClient
		}
		unredirected := *over
		unredirected.CheckRedirect = bearer.Unfollowed
		over = &unredirected
	}
	socket, response, err := websocket.Dial(ctx, address, &websocket.DialOptions{HTTPClient: over, HTTPHeader: headers})
	if err != nil {
		if response != nil {
			return nil, unopened(address, response)
		}
		return nil, err
	}
	conn := websocket.NetConn(ctx, socket, websocket.MessageBinary)
	socket.SetReadLimit(message)
	return conn, nil
}

// unopened is why a server opened no tunnel, from what it answered in
// its place. A server refuses a tunnel as it refuses a unary call of
// the Connect protocol: with the error's code and message as JSON, and
// its details, each a message's name and its bytes in base64. That is
// read as the *connect.Error it says; any other answer is said by its
// status. The WebSocket client keeps the first 1024 bytes of an answer,
// and one cut short there is no JSON, and is said by its status too.
func unopened(address string, response *http.Response) error {
	var refusal struct {
		Code    connect.Code `json:"code"`
		Message string       `json:"message"`
		Details []struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"details"`
	}
	if response.Body == nil || json.NewDecoder(response.Body).Decode(&refusal) != nil || refusal.Code == 0 {
		return fmt.Errorf("the server did not open the tunnel at %s: %s", address, response.Status)
	}
	refused := connect.NewError(refusal.Code, refusal.Message)
	for _, detail := range refusal.Details {
		if value, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(detail.Value, "=")); err == nil {
			refused = refused.WithDetail(&connect.ErrorDetail{Type: detail.Type, Value: value})
		}
	}
	return refused
}

// Presents is whether a tunnel to an address presents reach's token: it
// has one, and the address is of its origin.
func Presents(address string, reach Reach) bool {
	return reach.Token != "" && sameOrigin(address, reach.Origin)
}

// sameOrigin is whether an address, http or ws, is of an origin
// (bearer.SameOrigin).
func sameOrigin(address, origin string) bool {
	a, err := url.Parse(address)
	if err != nil {
		return false
	}
	return bearer.SameOrigin(a, parsed(origin))
}

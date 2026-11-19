package transport

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"

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
// where the address is of reach's origin. The connection lasts as long
// as the context.
func Tunnel(ctx context.Context, address string, reach Reach) (net.Conn, error) {
	headers := make(http.Header)
	propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(headers))
	over := reach.HTTP
	if Presents(address, reach) {
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
			return nil, fmt.Errorf("the server did not open the tunnel at %s: %s", address, response.Status)
		}
		return nil, err
	}
	conn := websocket.NetConn(ctx, socket, websocket.MessageBinary)
	socket.SetReadLimit(message)
	return conn, nil
}

// Presents is whether a tunnel to an address presents reach's token: it
// has one, and the address is of its origin.
func Presents(address string, reach Reach) bool {
	return reach.Token != "" && sameOrigin(address, reach.Origin)
}

// sameOrigin is whether an address, http or ws, is of an origin
// (bearer.Of).
func sameOrigin(address, origin string) bool {
	a, err := url.Parse(address)
	if err != nil {
		return false
	}
	o, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return bearer.Of(a, o)
}

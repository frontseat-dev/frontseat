// Package testsandbox asks a sandbox service, which is no part of the
// SDK, for a test alone: its calls, and the tunnels to its sandboxes'
// ports.
package testsandbox

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/coder/websocket"

	"github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1/sandboxv1alpha1connect"
	"github.com/frontseat-dev/frontseat/testsupport/testwire"
)

// Client is a client of the sandbox service at an endpoint, made with
// the HTTP client given, the first of over, or Go's own.
func Client(endpoint string, over ...*http.Client) sandboxv1alpha1connect.SandboxServiceClient {
	var chosen *http.Client
	if len(over) > 0 {
		chosen = over[0]
	}
	return sandboxv1alpha1connect.NewSandboxServiceClient(testwire.Conn(endpoint, chosen))
}

// Tunnel is a connection to a port of a sandbox, through the sandbox
// service at an endpoint. It lasts as long as the context.
func Tunnel(ctx context.Context, endpoint, sandbox string, port int) (net.Conn, error) {
	socket, _, err := websocket.Dial(ctx, fmt.Sprintf("%s/sandboxes/%s/tunnels/%d", endpoint, sandbox, port), nil)
	if err != nil {
		return nil, err
	}
	socket.SetReadLimit(1 << 20)
	return websocket.NetConn(ctx, socket, websocket.MessageBinary), nil
}

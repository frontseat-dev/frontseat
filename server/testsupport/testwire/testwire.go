// Package testwire reaches a server's API over the wire, as any client
// of the generated code does, for a test that asks what the SDK does not
// serve: a call's own fields, a page size, a filter.
package testwire

import (
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
)

// Conn is a connection to the API at an endpoint, made with an HTTP
// client of the test's, nil for Go's own, over which a generated client
// of any service is made.
func Conn(endpoint string, over *http.Client) *connect.Client {
	if over == nil {
		over = http.DefaultClient
	}
	return connect.NewClient(connecthttp.NewTransport(over, endpoint))
}

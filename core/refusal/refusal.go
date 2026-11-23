// Package refusal says what a service refuses by name: a canonical code,
// a message for a person, and google.rpc.ErrorInfo with the reason and
// the domain of the service that refused, which a client reads instead
// of the message, by the Go SDK's one reader of it (frontseat.Reason).
package refusal

import (
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

// The domains of the services that refuse by name.
const (
	// ControlPlane is the control plane's own, beside the services it
	// serves: a caller it does not know is refused in it, by every one.
	ControlPlane = "frontseat.dev"
	// Build is the build API's: builds, runs, releases and the graph.
	Build = "build.frontseat.dev"
	// Workspace is the workspace API's.
	Workspace = "workspace.frontseat.dev"
	// Credential is the credential API's.
	Credential = "credential.frontseat.dev"
	// Sandbox is the sandbox service's.
	Sandbox = "sandbox.frontseat.dev"
	// Source is the daemon's internal source API's, which the control
	// plane alone asks and acts on by its reasons.
	Source = "source.frontseat.dev"
)

// PageTokenInvalid is the reason of every List call that is given a page
// token it did not give.
const PageTokenInvalid = "PAGE_TOKEN_INVALID"

// RequestIDMost is the longest request id a Create takes, in bytes:
// each service refuses a longer one REQUEST_ID_INVALID, in its domain.
const RequestIDMost = 128

// New is a refusal of a code, with its reason, in a domain.
func New(domain string, code connect.Code, reason, message string) *connect.Error {
	refused := connect.NewError(code, message)
	if detail, err := connectproto.NewErrorDetail(&errdetails.ErrorInfo{Reason: reason, Domain: domain}); err == nil {
		refused = refused.WithDetail(detail)
	}
	return refused
}

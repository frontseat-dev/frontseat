// Package refusal says what a service refuses by name: a canonical code,
// a message for a person, and google.rpc.ErrorInfo with the reason and
// the domain of the service that refused, which a client reads instead
// of the message.
package refusal

import (
	"errors"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

// The domains of the services that refuse by name.
const (
	// Build is the build API's: builds, runs, releases and the graph.
	Build = "build.frontseat.dev"
	// Workspace is the workspace API's, and the control plane's own.
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

// New is a refusal of a code, with its reason, in a domain.
func New(domain string, code connect.Code, reason, message string) *connect.Error {
	refused := connect.NewError(code, message)
	if detail, err := connectproto.NewErrorDetail(&errdetails.ErrorInfo{Reason: reason, Domain: domain}); err == nil {
		refused = refused.WithDetail(detail)
	}
	return refused
}

// Reason is the reason and the domain an error carries as ErrorInfo;
// empty for an error that carries none.
func Reason(err error) (reason, domain string) {
	var refused *connect.Error
	if !errors.As(err, &refused) {
		return "", ""
	}
	for _, detail := range refused.Details() {
		message, err := connectproto.UnmarshalErrorDetail(detail)
		if err != nil {
			continue
		}
		if info, ok := message.(*errdetails.ErrorInfo); ok {
			return info.GetReason(), info.GetDomain()
		}
	}
	return "", ""
}

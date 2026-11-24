package frontseat

import (
	"context"
	"errors"
	"io"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

// Error is what a call to a server failed with: the canonical code, and,
// where the server refused the call by name, the reason and the domain
// of the service that refused (the google.rpc.ErrorInfo the refusal
// carries). A program reads Code and Reason, never Message, which is for
// a person. Every error a call returns from a server is one, reached
// with errors.As; errors.Is matches it against a sentinel by its reason.
//
//	var refused *frontseat.Error
//	if errors.As(err, &refused) && refused.Reason == frontseat.ReasonWorkspaceNotFound { ... }
//	if errors.Is(err, frontseat.ErrWorkspaceNotFound) { ... }
type Error struct {
	// Code is the canonical code: connect.CodeNotFound and the rest.
	Code connect.Code
	// Reason is why the server refused, as each call's contract lists
	// it: WORKSPACE_NOT_FOUND. Empty where the server gave none.
	Reason string
	// Domain is the domain of the service that refused:
	// workspace.frontseat.dev. Empty where the server gave none.
	Domain string
	// Message says what went wrong, for a person.
	Message string

	cause error
}

// Error is a server's refusal as Connect writes it, its code and its
// message; one the SDK made itself is its message.
func (e *Error) Error() string {
	var raw *connect.Error
	if errors.As(e.cause, &raw) {
		return raw.Error()
	}
	return e.Message
}

// Unwrap is the Connect error the server's answer was read as, so that
// connect.CodeOf reads its code; for one the SDK made itself, the
// sentinel of what it found (ErrWorkspaceFailed); nil for a sentinel.
func (e *Error) Unwrap() error { return e.cause }

// Is matches a sentinel *Error by its reason, and by its domain where
// the sentinel says one: errors.Is(err, ErrWorkspaceNotFound).
func (e *Error) Is(target error) bool {
	var want *Error
	if !errors.As(target, &want) || want.Reason == "" {
		return false
	}
	return e.Reason == want.Reason && (want.Domain == "" || e.Domain == want.Domain)
}

// Reason is why a server refused a call by name, and the domain of the
// service that refused; both are empty for an error that carries none.
// It is a shorthand for reading Error's fields.
//
//	if reason, _ := frontseat.Reason(err); reason == frontseat.ReasonWorkspaceNotFound { ... }
func Reason(err error) (reason, domain string) {
	var refused *Error
	if errors.As(err, &refused) {
		return refused.Reason, refused.Domain
	}
	var raw *connect.Error
	if errors.As(err, &raw) {
		read := refusedBy(raw)
		return read.Reason, read.Domain
	}
	return "", ""
}

// refusedBy is a Connect error as an Error, its reason read from the
// ErrorInfo it carries.
func refusedBy(raw *connect.Error) *Error {
	read := &Error{Code: raw.Code(), Message: raw.Message(), cause: raw}
	for _, detail := range raw.Details() {
		message, err := connectproto.UnmarshalErrorDetail(detail)
		if err != nil {
			continue
		}
		if info, ok := message.(*errdetails.ErrorInfo); ok {
			read.Reason, read.Domain = info.GetReason(), info.GetDomain()
			break
		}
	}
	return read
}

// refused is an error a call returned as the Error it is: what is no
// Connect error (a stream's end, a context that ended before anything
// was asked) is as it was.
func refused(err error) error {
	var raw *connect.Error
	if err == nil || errors.Is(err, io.EOF) || !errors.As(err, &raw) {
		return err
	}
	var already *Error
	if errors.As(err, &already) {
		return err
	}
	return refusedBy(raw)
}

// errorsRead is the interceptor that has every call of a Client fail
// with an Error: its opening, what it sends and what it receives.
func errorsRead(next connect.ClientFunc) connect.ClientFunc {
	return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
		stream, err := next(ctx, spec)
		if err != nil {
			return nil, refused(err)
		}
		return readStream{stream}, nil
	}
}

// readStream is a stream whose errors are read as Errors.
type readStream struct{ connect.ClientStream }

func (s readStream) SendHeaders() error     { return refused(s.ClientStream.SendHeaders()) }
func (s readStream) Send(message any) error { return refused(s.ClientStream.Send(message)) }
func (s readStream) CloseSend() error       { return refused(s.ClientStream.CloseSend()) }
func (s readStream) Receive(message any) error {
	return refused(s.ClientStream.Receive(message))
}
func (s readStream) Close() error { return refused(s.ClientStream.Close()) }

// The domains of the services that refuse by name, as an Error's Domain
// says them.
const (
	// DomainControlPlane is the control plane's own: a caller it does not
	// know is refused in it (IDENTITY_MISSING, IDENTITY_INVALID), by every
	// service it serves.
	DomainControlPlane = "frontseat.dev"
	// DomainWorkspace is the workspace API's.
	DomainWorkspace = "workspace.frontseat.dev"
	// DomainBuild is the build API's: builds, runs, releases and the
	// graph, which a build daemon serves.
	DomainBuild = "build.frontseat.dev"
	// DomainCredential is the credential API's.
	DomainCredential = "credential.frontseat.dev"
)

// Package identity is how a control plane knows who calls it: the port
// every caller is checked through, and who a call was found to be from.
// What checks a caller is an implementation of Authenticator: the token
// of the local stack's owner, which frontseat-local mints, is one
// (controlplane.OwnerToken); another, a company's sign-in, is not
// served here.
package identity

import (
	"context"
	"errors"
)

// Caller is who a call was found to be from.
type Caller struct {
	// Subject names them: "local" for the local stack's owner.
	Subject string
}

// Authenticator says who presented a token. It is never given what is
// not a bearer token, and it says nothing of the token in what it
// returns: an error is ErrMissing or ErrInvalid, or wraps one of them.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (Caller, error)
}

// ErrMissing is what is said of a call that presents no token.
var ErrMissing = errors.New("identity: the call presents no token")

// ErrInvalid is what is said of a token that is not one known.
var ErrInvalid = errors.New("identity: the token presented is not one known")

type key struct{}

// With is a context that carries who a call is from.
func With(ctx context.Context, caller Caller) context.Context {
	return context.WithValue(ctx, key{}, caller)
}

// Of is who a call is from, as the context carries it, and whether it
// carries anyone.
func Of(ctx context.Context) (Caller, bool) {
	caller, ok := ctx.Value(key{}).(Caller)
	return caller, ok
}

package build

import (
	"context"
	"errors"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/page"
	"github.com/frontseat-dev/frontseat/server/internal/refusal"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// failureOf preserves canonical codes and typed details across stored runs and
// RPCs. The fallback applies only to errors without a canonical status.
func failureOf(err error, fallback codes.Code) *statuspb.Status {
	if err == nil {
		return nil
	}
	if s, ok := status.FromError(err); ok {
		return s.Proto()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Proto()
	}
	return status.New(fallback, err.Error()).Proto()
}

// explained is a failure as a run says it: its message, and the diagnostics
// among its details, which say the rule and the file without the
// message being read.
func explained(failure *statuspb.Status) (string, []*typev1.Diagnostic) {
	if failure == nil {
		return "", nil
	}
	var diagnostics []*typev1.Diagnostic
	for _, detail := range failure.GetDetails() {
		diagnostic := new(typev1.Diagnostic)
		if detail.MessageIs(diagnostic) && detail.UnmarshalTo(diagnostic) == nil {
			diagnostics = append(diagnostics, diagnostic)
		}
	}
	return failure.GetMessage(), diagnostics
}

// failedRPC is an error as a call answers it: its code, or the fallback
// where it carries none, and its details. Where it is refused by no
// reason of its own, it is refused by the one given, if any.
func failedRPC(err error, fallback codes.Code, reason string) error {
	failure := failureOf(err, fallback)
	if failure == nil {
		return nil
	}
	out := connect.NewError(connect.Code(failure.GetCode()), failure.GetMessage())
	named := false
	for _, detail := range failure.GetDetails() {
		named = named || detail.MessageIs(new(errdetails.ErrorInfo))
		encoded, encodeErr := connectproto.NewErrorDetail(detail)
		if encodeErr != nil {
			return connect.NewError(connect.CodeInternal, "could not encode failure detail")
		}
		out = out.WithDetail(encoded)
	}
	// What carries no reason of its own is given this one, beside the
	// details it carries.
	if !named && reason != "" {
		info, err := connectproto.NewErrorDetail(&errdetails.ErrorInfo{Reason: reason, Domain: refusal.Build})
		if err != nil {
			return connect.NewError(connect.CodeInternal, "could not encode failure detail")
		}
		out = out.WithDetail(info)
	}
	return out
}

// refuse is a refusal of the build API, with its reason.
func refuse(code connect.Code, reason, message string) *connect.Error {
	return refusal.New(refusal.Build, code, reason, message)
}

// paged is the page of what a List answers that its request asks for,
// and the token of the next; a token no List gave is refused.
func paged[T any](items []T, size int32, token string) ([]T, string, error) {
	kept, next, err := page.Of(items, size, token)
	if err != nil {
		return nil, "", refuse(connect.CodeInvalidArgument, refusal.PageTokenInvalid, err.Error())
	}
	return kept, next, nil
}

package wire

import (
	"math"

	"github.com/frontseat-dev/frontseat/build/engine/model"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/protoadapt"
)

// Failure is a mistake as the API sends it, the one place it is: a
// failure of the code given that says message (the mistake in words,
// with whatever wraps it), carrying the mistake's reason as the reason
// it is refused by, and, where it says more than its message, a Diagnostic
// of its parts. A mistake that wraps a failure of its own, as a
// plugin's, is sent as that failure, saying message.
func Failure(m *model.Mistake, code codes.Code, message string) *statuspb.Status {
	if wrapped, ok := status.FromError(m.Cause); ok && m.Cause != nil {
		out := wrapped.Proto()
		out.Message = message
		return out
	}
	failure := status.New(code, message)
	var details []protoadapt.MessageV1
	if m.Reason != "" {
		details = append(details, &errdetails.ErrorInfo{Reason: m.Reason, Domain: "build.frontseat.dev"})
	}
	if m.File != "" || m.Line > 0 || m.Rule != "" || m.Help != "" {
		diagnostic := &typev1.Diagnostic{RuleId: m.Rule, Message: m.Message, Help: m.Help}
		if m.File != "" {
			diagnostic.SourceLocation = &typev1.SourceLocation{Repository: m.Repository, Path: m.File, Line: line(m.Line)}
		}
		details = append(details, diagnostic)
	}
	if len(details) == 0 {
		return failure.Proto()
	}
	detailed, err := failure.WithDetails(details...)
	if err != nil {
		// Each detail is one of the API's own: one that cannot be
		// encoded is not sent, and the failure still says the mistake.
		return failure.Proto()
	}
	return detailed.Proto()
}

// line is a line as a SourceLocation holds one; one past what it holds
// is said as no line.
func line(n int) int32 {
	if n <= 0 || n > math.MaxInt32 {
		return 0
	}
	return int32(n)
}

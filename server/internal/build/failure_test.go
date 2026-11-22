package build

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/frontseat-dev/frontseat/sdk/go"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/engine/release"
	"github.com/frontseat-dev/frontseat/server/internal/refusal"
)

func TestFailureAdaptersPreserveCodesAndDetails(t *testing.T) {
	want := &errdetails.ErrorInfo{Reason: "INVALID_MANIFEST", Domain: "go.plugins.frontseat.dev"}
	s, err := status.New(codes.FailedPrecondition, "invalid manifest").WithDetails(want)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := fmt.Errorf("evaluate: %w", s.Err())
	failure := failureOf(wrapped, codes.Internal)
	if failure.GetCode() != int32(codes.FailedPrecondition) || failure.GetMessage() != wrapped.Error() {
		t.Fatalf("canonical code changed: %v", failure)
	}
	var rpc *connect.Error
	if !errors.As(failedRPC(wrapped, codes.Internal, ""), &rpc) || len(rpc.Details()) != 1 {
		t.Fatalf("RPC lost details: %v", rpc)
	}
	got, err := connectproto.UnmarshalErrorDetail(rpc.Details()[0])
	if err != nil || !proto.Equal(got, want) {
		t.Fatalf("RPC changed detail: %v, %v", got, err)
	}
	for _, tc := range []struct {
		err  error
		code codes.Code
	}{
		{fmt.Errorf("read: %w", context.Canceled), codes.Canceled},
		{fmt.Errorf("read: %w", context.DeadlineExceeded), codes.DeadlineExceeded},
		{errors.New("transport failed"), codes.Internal},
	} {
		if got := failureOf(tc.err, codes.Internal); got.GetCode() != int32(tc.code) {
			t.Fatalf("%v: got %v", tc.err, got)
		}
	}
	if failureOf(nil, codes.Internal) != nil || failedRPC(nil, codes.Internal, "") != nil {
		t.Fatal("success acquired a failure")
	}
}

func TestCanonicalCancellationRetainsItsDetails(t *testing.T) {
	for _, tc := range []struct {
		code  codes.Code
		cause error
	}{
		{codes.Canceled, context.Canceled}, {codes.DeadlineExceeded, context.DeadlineExceeded},
	} {
		want := &errdetails.RetryInfo{}
		s, err := status.New(tc.code, "worker timeout").WithDetails(want)
		if err != nil {
			t.Fatal(err)
		}
		for _, err := range []error{s.Err(), fmt.Errorf("%w: %w", s.Err(), tc.cause)} {
			got := failureOf(err, codes.Internal)
			if got.GetCode() != int32(tc.code) || len(got.GetDetails()) != 1 {
				t.Fatalf("canonical cancellation lost details: %v", got)
			}
			info := new(errdetails.RetryInfo)
			if err := got.GetDetails()[0].UnmarshalTo(info); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// A failure that carries a diagnostic and no reason of its own is given
// the reason it is refused by beside the diagnostic, which it keeps; one
// that carries a reason keeps its own.
func TestAReasonGivenKeepsTheDetailsAFailureCarries(t *testing.T) {
	finding := &typev1.Diagnostic{RuleId: "go/invalid-manifest", Message: "no module"}
	s, err := status.New(codes.FailedPrecondition, "invalid manifest").WithDetails(finding)
	if err != nil {
		t.Fatal(err)
	}
	var rpc *connect.Error
	if !errors.As(failedRPC(s.Err(), codes.Internal, "SOURCE_MISTAKEN"), &rpc) || len(rpc.Details()) != 2 {
		t.Fatalf("the failure was answered %v; want its diagnostic and a reason", rpc)
	}
	kept, _ := connectproto.UnmarshalErrorDetail(rpc.Details()[0])
	info, _ := connectproto.UnmarshalErrorDetail(rpc.Details()[1])
	if !proto.Equal(kept, finding) || info.(*errdetails.ErrorInfo).GetReason() != "SOURCE_MISTAKEN" {
		t.Errorf("answered %v and %v; want the diagnostic, then SOURCE_MISTAKEN", kept, info)
	}
	named, _ := status.New(codes.FailedPrecondition, "x").WithDetails(&errdetails.ErrorInfo{Reason: "INVALID_MANIFEST"})
	if !errors.As(failedRPC(named.Err(), codes.Internal, "SOURCE_MISTAKEN"), &rpc) || len(rpc.Details()) != 1 {
		t.Errorf("a failure with a reason of its own was given another: %v", rpc.Details())
	}
}

// A release strategy's answer that the plugin contract does not let it
// give is refused as the contract says, INVALID_ARGUMENT with
// RELEASE_INVALID; what went wrong otherwise is refused by no reason.
func TestAStrategysWrongAnswerIsRefusedByItsReason(t *testing.T) {
	refused := releaseFailure(fmt.Errorf("planning greet: %w", &release.WrongAnswer{Message: "the versioning strategy answered wrongly"}))
	if reason, domain := frontseat.Reason(refused); connect.CodeOf(refused) != connect.CodeInvalidArgument || reason != "RELEASE_INVALID" || domain != refusal.Build {
		t.Errorf("a strategy's wrong answer was answered %v (%s of %s); want INVALID_ARGUMENT with RELEASE_INVALID", refused, reason, domain)
	}
	// The control: what is no wrong answer is given no reason.
	other := releaseFailure(errors.New("the disk is full"))
	if reason, _ := frontseat.Reason(other); connect.CodeOf(other) != connect.CodeInternal || reason != "" {
		t.Errorf("a failure that is no wrong answer was answered %v (%s); want INTERNAL and no reason", other, reason)
	}
}

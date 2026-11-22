package frontseat

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"

	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
)

// What a server refuses is an Error, by errors.As, whether a unary call
// or a stream was refused: its code, its reason and its domain read
// from it, and its code still read by connect.CodeOf. A sentinel of its
// reason matches it by errors.Is; the controls are a sentinel of another
// reason and one of the reason in another domain, which do not.
func TestARefusalIsAnErrorThatCanBeMatched(t *testing.T) {
	here := servedDaemon(t, &daemon{})
	ctx := context.Background()
	_, err := here.Build(ctx, BuildRequest{Profile: "nobodys"})
	var refusedBy *Error
	if !errors.As(err, &refusedBy) {
		t.Fatalf("the refusal %v is no Error", err)
	}
	if refusedBy.Code != connect.CodeInvalidArgument || refusedBy.Reason != ReasonProfileUnknown || refusedBy.Domain != DomainBuild || refusedBy.Message != "no one defined the profile" {
		t.Errorf("the refusal is read as %+v", refusedBy)
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("connect.CodeOf reads %v", connect.CodeOf(err))
	}
	if !errors.Is(err, &Error{Reason: ReasonProfileUnknown, Domain: DomainBuild}) || !errors.Is(err, &Error{Reason: ReasonProfileUnknown}) {
		t.Error("a sentinel of the refusal's reason does not match it")
	}
	if errors.Is(err, ErrBuildNotFound) || errors.Is(err, &Error{Reason: ReasonProfileUnknown, Domain: DomainWorkspace}) {
		t.Error("the control: a sentinel of another reason, or of another domain, matches")
	}
	for _, err := range here.BuildByID("build_1").Events(ctx, 0) {
		if !errors.As(err, &refusedBy) || refusedBy.Code != connect.CodeUnimplemented || refusedBy.Reason != "" {
			t.Errorf("a refused stream ended with %v, want an Error of its code and no reason", err)
		}
	}
}

// A workspace that cannot be worked in is refused by the SDK as a
// control plane refuses one, ErrWorkspaceNotReady, and by what it is:
// failed, or paused. One that is ready is no error.
func TestAWorkspaceThatCannotBeWorkedInIsRefusedByWhatItIs(t *testing.T) {
	held := func(state workspacev1.WorkspaceStatus_State) *workspacev1.Workspace {
		return &workspacev1.Workspace{Status: &workspacev1.WorkspaceStatus{State: state, Failure: "no room"}}
	}
	failed := ready("w", held(workspacev1.WorkspaceStatus_STATE_FAILED))
	if !errors.Is(failed, ErrWorkspaceNotReady) || !errors.Is(failed, ErrWorkspaceFailed) || errors.Is(failed, ErrWorkspacePaused) {
		t.Errorf("a failed workspace is refused with %v", failed)
	}
	if failed.Error() != "the workspace w failed: no room" {
		t.Errorf("a failed workspace is said as %q", failed.Error())
	}
	paused := ready("w", held(workspacev1.WorkspaceStatus_STATE_PAUSED))
	if !errors.Is(paused, ErrWorkspaceNotReady) || !errors.Is(paused, ErrWorkspacePaused) {
		t.Errorf("a paused workspace is refused with %v", paused)
	}
	if starting := ready("w", held(workspacev1.WorkspaceStatus_STATE_STARTING)); !errors.Is(starting, ErrWorkspaceNotReady) || errors.Is(starting, ErrWorkspaceFailed) {
		t.Errorf("a starting workspace is refused with %v", starting)
	}
	if err := ready("w", held(workspacev1.WorkspaceStatus_STATE_READY)); err != nil {
		t.Errorf("the control: a ready workspace is refused with %v", err)
	}
}

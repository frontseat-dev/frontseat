package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"github.com/frontseat-dev/frontseat/sdk/go"
	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/testsupport/drivertest"
	"google.golang.org/protobuf/types/known/durationpb"
)

// unending is a driver whose command runs until whoever asked for it
// stops waiting.
type unending struct{ *drivertest.Memory }

func (unending) Exec(ctx context.Context, _, _ string, _ []string, _ int) (int, []byte, bool, error) {
	<-ctx.Done()
	return 0, nil, false, ctx.Err()
}

// A command is said to have run past its timeout where it ran past the
// timeout the request gives. A caller who stops waiting before that is
// told so, and not that the command timed out.
func TestACallersOwnDeadlineIsNotTheCommandsTimeout(t *testing.T) {
	s := started(t, unending{drivertest.NewMemory()}, t.TempDir())
	made, err := s.CreateSandbox(context.Background(), box(nil))
	if err != nil {
		t.Fatal(err)
	}
	id := made.GetSandbox().GetId()

	// The control: the request's own timeout, reached, is the command's.
	_, err = s.ExecSandbox(context.Background(), &sandboxv1.ExecSandboxRequest{SandboxId: id, Arguments: []string{"sleep", "600"}, Timeout: durationpb.New(50 * time.Millisecond)})
	if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeDeadlineExceeded || reason != "COMMAND_TIMED_OUT" {
		t.Fatalf("a command past its timeout: %v; want DEADLINE_EXCEEDED with COMMAND_TIMED_OUT", err)
	}

	for name, leave := range map[string]func() (context.Context, context.CancelFunc){
		"a deadline": func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 50*time.Millisecond)
		},
		"going away": func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(50*time.Millisecond, cancel)
			return ctx, cancel
		},
	} {
		ctx, cancel := leave()
		_, err := s.ExecSandbox(ctx, &sandboxv1.ExecSandboxRequest{SandboxId: id, Arguments: []string{"sleep", "600"}, Timeout: durationpb.New(time.Minute)})
		cancel()
		if reason, _ := frontseat.Reason(err); err == nil || reason != "" || !errors.Is(err, ctx.Err()) {
			t.Errorf("%s of the caller's, before the command's timeout: %v (reason %q); want the caller's own and no reason", name, err, reason)
		}
	}
}

package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"github.com/frontseat-dev/frontseat/sdk/go"
	sandboxv1 "github.com/frontseat-dev/frontseat/server/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/sandbox/driver"
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

// runningOver serves the sandbox API over a driver, and returns it and
// the id of a sandbox it made.
func runningOver(t *testing.T, d driver.Driver) (*Server, string) {
	t.Helper()
	s := started(t, d, t.TempDir())
	made, err := s.CreateSandbox(context.Background(), box(nil))
	if err != nil {
		t.Fatal(err)
	}
	return s, made.GetSandbox().GetId()
}

// A command is said to have run past its timeout where it ran past the
// timeout the request gives. A caller who stops waiting before that is
// told so, and not that the command timed out.
func TestACallersOwnDeadlineIsNotTheCommandsTimeout(t *testing.T) {
	s, id := runningOver(t, unending{drivertest.NewMemory()})

	// The control: the request's own timeout, reached, is the command's.
	_, err := s.ExecSandbox(context.Background(), &sandboxv1.ExecSandboxRequest{SandboxId: id, Arguments: []string{"sleep", "600"}, Timeout: durationpb.New(50 * time.Millisecond)})
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

// printing is a driver whose command prints more than is kept of it
// where it is asked to print "much", and returns at once.
type printing struct{ *drivertest.Memory }

func (printing) Exec(_ context.Context, _, _ string, arguments []string, most int) (int, []byte, bool, error) {
	if arguments[0] == "much" {
		return 0, make([]byte, most), true, nil
	}
	return 0, []byte("little"), false, nil
}

// What a command printed past what is kept of it is said to be cut; the
// control is a command that printed little, which is not. A timeout of
// more than ten minutes is refused, and one of ten minutes is not.
func TestACommandsOutputSaysWhetherItWasCutAndItsTimeoutIsHeld(t *testing.T) {
	s, id := runningOver(t, printing{drivertest.NewMemory()})
	run := func(what string, timeout time.Duration) (*sandboxv1.ExecSandboxResponse, error) {
		return s.ExecSandbox(context.Background(), &sandboxv1.ExecSandboxRequest{SandboxId: id, Arguments: []string{what}, Timeout: durationpb.New(timeout)})
	}
	if much, err := run("much", time.Minute); err != nil || !much.GetOutputTruncated() || len(much.GetOutput()) != execOutput {
		t.Errorf("a command that printed more than is kept: %v, cut %v, %d bytes; want it said to be cut", err, much.GetOutputTruncated(), len(much.GetOutput()))
	}
	if little, err := run("little", 10*time.Minute); err != nil || little.GetOutputTruncated() {
		t.Errorf("the control, a command that printed little given ten minutes: %v, cut %v", err, little.GetOutputTruncated())
	}
	_, err := run("little", 10*time.Minute+time.Second)
	if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeInvalidArgument || reason != "TIMEOUT_INVALID" {
		t.Errorf("a timeout of more than ten minutes: %v; want TIMEOUT_INVALID", err)
	}
}

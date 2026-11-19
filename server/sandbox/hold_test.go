package sandbox

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"

	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/sandbox/driver"
	"github.com/frontseat-dev/frontseat/server/testsupport/drivertest"
)

// meeting is a driver that makes nothing until two have asked it to: two
// requests that arrive together are both inside it before either ends.
type meeting struct {
	*drivertest.Memory
	asked atomic.Int32
	both  chan struct{}
}

func (d *meeting) Apply(ctx context.Context, wanted driver.Sandbox) (driver.Sandbox, error) {
	if d.asked.Add(1) == 2 {
		close(d.both)
	}
	select {
	case <-d.both:
	case <-time.After(300 * time.Millisecond):
		// The second never came: it was held back, as it should be.
	case <-ctx.Done():
		return driver.Sandbox{}, ctx.Err()
	}
	return d.Memory.Apply(ctx, wanted)
}

// started is a service over a driver, with its records in a directory:
// a server as it starts.
func started(t *testing.T, d driver.Driver, state string) *service {
	t.Helper()
	s, err := newService(d, "", nil, state)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func box(change func(*sandboxv1.SandboxSpec)) *sandboxv1.CreateSandboxRequest {
	spec := &sandboxv1.SandboxSpec{Image: "box"}
	if change != nil {
		change(spec)
	}
	return &sandboxv1.CreateSandboxRequest{Spec: spec}
}

func TestOneRequestSentTwiceAtOnceMakesOneSandbox(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	held := &meeting{Memory: drivertest.NewMemory(), both: make(chan struct{})}
	s := started(t, held, t.TempDir())
	request := box(nil)
	request.RequestId = "0b0e7d4e-7f6a-4d50-9d8e-6a1f0a7a2c11"

	var together sync.WaitGroup
	made := make(chan string, 2)
	for range 2 {
		together.Go(func() {
			got, err := s.CreateSandbox(ctx, request)
			if err != nil {
				t.Error(err)
				return
			}
			made <- got.GetSandbox().GetId()
		})
	}
	together.Wait()
	close(made)

	there, err := held.List(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(there) != 1 {
		t.Errorf("one request made %d sandboxes", len(there))
	}
	first, second := <-made, <-made
	if first == "" || first != second {
		t.Errorf("the two were answered with %q and %q, want the one sandbox", first, second)
	}
}

// What a driver cannot give is refused, never run weaker; and what the
// server does not know the meaning of is refused too, not taken for
// nothing. The memory driver can list no hosts.
func TestASpecThatWouldBeRunWeakerIsRefused(t *testing.T) {
	cases := []struct {
		name   string
		change func(*sandboxv1.SandboxSpec)
		code   connect.Code
	}{
		{"only listed hosts, one listed", func(s *sandboxv1.SandboxSpec) {
			s.Egress = &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_ALLOW_LISTED, Rules: []*sandboxv1.EgressRule{{Host: "example.com"}}}
		}, connect.CodeFailedPrecondition},
		{"only listed hosts, none listed", func(s *sandboxv1.SandboxSpec) {
			s.Egress = &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_ALLOW_LISTED}
		}, connect.CodeFailedPrecondition},
		{"an egress mode nobody defined", func(s *sandboxv1.SandboxSpec) { s.Egress = &sandboxv1.Egress{Mode: 99} }, connect.CodeInvalidArgument},
		{"less than no CPU", func(s *sandboxv1.SandboxSpec) { s.Shape = &sandboxv1.Shape{MilliCpus: -1} }, connect.CodeInvalidArgument},
		{"less than no memory", func(s *sandboxv1.SandboxSpec) { s.Shape = &sandboxv1.Shape{MemoryBytes: -1} }, connect.CodeInvalidArgument},
		{"less than no disk", func(s *sandboxv1.SandboxSpec) { s.Shape = &sandboxv1.Shape{DiskBytes: -1} }, connect.CodeInvalidArgument},
		{"a volume that starts empty and cannot be written", func(s *sandboxv1.SandboxSpec) {
			s.Volumes = []*sandboxv1.Volume{{Name: "scratch", Path: "/scratch", ReadOnly: true}}
		}, connect.CodeInvalidArgument},
		{"root's user", func(s *sandboxv1.SandboxSpec) { s.User = "0" }, connect.CodeInvalidArgument},
		{"a user by name", func(s *sandboxv1.SandboxSpec) { s.User = "postgres" }, connect.CodeInvalidArgument},
		{"the whole image written", func(s *sandboxv1.SandboxSpec) { s.Writable = []string{"/"} }, connect.CodeInvalidArgument},
		{"more directories written than a sandbox may", func(s *sandboxv1.SandboxSpec) {
			for i := range 33 {
				s.Writable = append(s.Writable, fmt.Sprintf("/var/lib/d%d", i))
			}
		}, connect.CodeInvalidArgument},
		{"a directory written that climbs", func(s *sandboxv1.SandboxSpec) { s.Writable = []string{"/var/../etc"} }, connect.CodeInvalidArgument},
		{"a wrapper of no absolute program", func(s *sandboxv1.SandboxSpec) { s.Wrapper = []string{"routes", "--"} }, connect.CodeInvalidArgument},
		{"a wrapper with a word no command line holds", func(s *sandboxv1.SandboxSpec) { s.Wrapper = []string{"/bb/x", "a\x00b"} }, connect.CodeInvalidArgument},
	}
	for _, c := range cases {
		held := drivertest.NewMemory()
		s := started(t, held, t.TempDir())

		_, err := s.CreateSandbox(context.Background(), box(c.change))

		if connect.CodeOf(err) != c.code {
			t.Errorf("%s: %v, want it refused as %v", c.name, err, c.code)
		}
		if there, _ := held.List(context.Background(), nil); len(there) != 0 {
			t.Errorf("%s: %d sandboxes were made", c.name, len(there))
		}
	}
	// The control: the same driver makes a sandbox that asks for nothing
	// it cannot give.
	if _, err := started(t, drivertest.NewMemory(), t.TempDir()).CreateSandbox(context.Background(), box(nil)); err != nil {
		t.Errorf("a plain sandbox: %v", err)
	}
	// And one that runs as a user by number, writes a directory of its
	// image and is wrapped by a program named absolutely.
	if _, err := started(t, drivertest.NewMemory(), t.TempDir()).CreateSandbox(context.Background(), box(func(s *sandboxv1.SandboxSpec) {
		s.User, s.Writable, s.Wrapper = "70", []string{"/var/lib/postgresql"}, []string{"/bb/frontseat-action", "routes", "--"}
	})); err != nil {
		t.Errorf("a sandbox with its user, what it writes and its wrapper: %v", err)
	}
}

// Run with the race detector, which is what this is for.
func TestPauseAndResumeAtOnceTakeTurns(t *testing.T) {
	s := started(t, drivertest.NewMemory(), t.TempDir())
	ctx := context.Background()
	made, err := s.CreateSandbox(ctx, box(nil))
	if err != nil {
		t.Fatal(err)
	}
	id := made.GetSandbox().GetId()

	var together sync.WaitGroup
	for range 50 {
		together.Go(func() { _, _ = s.pause(ctx, id, true) })
		together.Go(func() { _, _ = s.pause(ctx, id, false) })
	}
	together.Wait()

	last, err := s.pause(ctx, id, false)
	if err != nil || last.GetState() != sandboxv1.Sandbox_STATE_RUNNING {
		t.Errorf("after them, resumed: %v %v, want it running", last.GetState(), err)
	}
}

// stuck is a driver that cannot delete until it is let.
type stuck struct {
	*drivertest.Memory
	let atomic.Bool
}

func (d *stuck) Delete(ctx context.Context, id string) error {
	if !d.let.Load() {
		return errors.New("the provider did not answer")
	}
	return d.Memory.Delete(ctx, id)
}

// A sandbox that could not be ended is still there, so it is still the
// service's: it is listed, and ending it again, once the provider
// answers, ends it.
func TestASandboxThatCouldNotBeEndedIsStillTheServices(t *testing.T) {
	ctx := context.Background()
	held := &stuck{Memory: drivertest.NewMemory()}
	s := started(t, held, t.TempDir())
	made, err := s.CreateSandbox(ctx, box(nil))
	if err != nil {
		t.Fatal(err)
	}
	id := made.GetSandbox().GetId()

	if _, err := s.DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: id}); err == nil {
		t.Fatal("a sandbox the provider did not end was said to be deleted")
	}
	listed, err := s.ListSandboxes(ctx, &sandboxv1.ListSandboxesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.GetSandboxes()) != 1 || listed.GetSandboxes()[0].GetId() != id {
		t.Fatalf("not ended, the service lists %v, want the sandbox still", listed.GetSandboxes())
	}

	held.let.Store(true)
	if _, err := s.DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: id}); err != nil {
		t.Fatalf("once it could be ended, deleting it = %v", err)
	}
	there, _ := held.List(ctx, nil)
	listed, _ = s.ListSandboxes(ctx, &sandboxv1.ListSandboxesRequest{})
	if len(there) != 0 || len(listed.GetSandboxes()) != 0 {
		t.Fatalf("once it could be ended it was not: the driver holds %d, the service lists %d", len(there), len(listed.GetSandboxes()))
	}
}

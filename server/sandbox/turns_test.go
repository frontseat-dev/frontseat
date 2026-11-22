package sandbox

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"

	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/sandbox/driver"
	"github.com/frontseat-dev/frontseat/server/testsupport/drivertest"
)

// holding is a driver that, once armed, holds the next Apply or Delete
// open until it is let go: what else is asked meanwhile is asked while
// the driver is still at work.
type holding struct {
	*drivertest.Memory
	applies, deletes atomic.Bool
	entered, letGo   chan struct{}
}

func newHolding() *holding {
	return &holding{Memory: drivertest.NewMemory(), entered: make(chan struct{}), letGo: make(chan struct{})}
}

// hold holds a call that is armed until it is let go, or until its
// asker gives up, as a provider that does not answer is given up on.
func (d *holding) hold(ctx context.Context, armed *atomic.Bool) error {
	if armed.CompareAndSwap(true, false) {
		close(d.entered)
		select {
		case <-d.letGo:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (d *holding) Apply(ctx context.Context, wanted driver.Sandbox) (driver.Sandbox, error) {
	if err := d.hold(ctx, &d.applies); err != nil {
		return driver.Sandbox{}, err
	}
	return d.Memory.Apply(ctx, wanted)
}

func (d *holding) Delete(ctx context.Context, id string) error {
	if err := d.hold(ctx, &d.deletes); err != nil {
		return err
	}
	return d.Memory.Delete(ctx, id)
}

// heldSandbox starts the service over a driver that holds what it is
// armed for, and has it make a sandbox; it returns the driver, the
// service and the sandbox's id.
func heldSandbox(t *testing.T) (*holding, *Server, string) {
	t.Helper()
	d := newHolding()
	s := started(t, d, t.TempDir())
	made, err := s.CreateSandbox(context.Background(), box(nil))
	if err != nil {
		t.Fatal(err)
	}
	return d, s, made.GetSandbox().GetId()
}

// A sandbox deleted while it was being paused is not made again by the
// pause: nothing is left that the service no longer knows.
func TestASandboxDeletedWhileItIsPausedIsNotMadeAgain(t *testing.T) {
	ctx := context.Background()
	d, s, id := heldSandbox(t)
	d.applies.Store(true)

	var both sync.WaitGroup
	both.Go(func() { _, _ = s.pause(ctx, id, true) })
	<-d.entered // the pause is with the driver
	deleted := make(chan error, 1)
	both.Go(func() {
		_, err := s.DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: id})
		deleted <- err
	})
	time.Sleep(100 * time.Millisecond)
	close(d.letGo)
	both.Wait()
	if err := <-deleted; err != nil {
		t.Fatalf("deleting the sandbox = %v", err)
	}
	if there, _ := d.List(ctx, nil); len(there) != 0 {
		t.Fatalf("the driver holds %v after the sandbox was deleted, which the service no longer knows", there)
	}
}

// What waits for a sandbox's turn waits no longer than its asker: while
// a delete holds the turn on a provider that does not answer, a delete
// whose asker gives up is answered that it was not waited for, and does
// not hang. The control is the delete that held the turn, which deletes
// the sandbox once the provider answers.
func TestWhatWaitsForASandboxsTurnWaitsNoLongerThanItsAsker(t *testing.T) {
	ctx := context.Background()
	d, s, id := heldSandbox(t)
	d.deletes.Store(true)
	first := make(chan error, 1)
	go func() {
		_, err := s.DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: id})
		first <- err
	}()
	<-d.entered // the delete holds the turn, on a provider that does not answer

	impatient, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stop()
	began := time.Now()
	_, err := s.DeleteSandbox(impatient, &sandboxv1.DeleteSandboxRequest{SandboxId: id})
	if connect.CodeOf(err) != connect.CodeDeadlineExceeded || time.Since(began) > time.Second {
		t.Fatalf("a delete whose asker gave up after 50ms took %s and ended %v", time.Since(began), err)
	}
	close(d.letGo)
	if err := <-first; err != nil {
		t.Fatalf("once the provider answered, deleting the sandbox = %v", err)
	}
	if there, _ := d.List(ctx, nil); len(there) != 0 {
		t.Fatalf("the driver still holds %v", there)
	}
}

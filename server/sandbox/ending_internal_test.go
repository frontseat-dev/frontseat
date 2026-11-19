package sandbox

import (
	"context"
	"sync"
	"testing"
	"time"

	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/sandbox/driver"
	"github.com/frontseat-dev/frontseat/server/testsupport/drivertest"
)

// slow is a driver that takes a moment to say how a sandbox is, as one
// that asks a provider over a network does.
type slow struct{ *drivertest.Memory }

func (d slow) Observe(ctx context.Context, id string) (driver.Sandbox, error) {
	time.Sleep(5 * time.Millisecond)
	return d.Memory.Observe(ctx, id)
}

// A request sent again by several at once, once the sandbox it made is
// gone, makes one sandbox, and each of them is answered with it: none is
// told the first is gone because another forgot it a moment before.
func TestARequestSentAgainByManyOnceItsSandboxIsGoneMakesOne(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for round := range 5 {
		held := drivertest.NewMemory()
		s := started(t, slow{held}, t.TempDir())
		request := box(nil)
		request.RequestId = "of-many"
		first, err := s.CreateSandbox(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		gone := held.Linger()
		if _, err := s.DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: first.GetSandbox().GetId()}); err == nil {
			t.Fatal("a sandbox that was not gone was said to be deleted")
		}
		gone()

		var together sync.WaitGroup
		made := make(chan string, 8)
		for asker := range 8 {
			together.Go(func() {
				got, err := s.CreateSandbox(ctx, request)
				if err != nil {
					t.Errorf("round %d, asker %d: %v", round, asker, err)
					return
				}
				made <- got.GetSandbox().GetId()
			})
		}
		together.Wait()
		close(made)
		ids := map[string]bool{}
		for id := range made {
			ids[id] = true
		}
		if len(ids) != 1 || ids[first.GetSandbox().GetId()] {
			t.Errorf("round %d: the request made %v, want one sandbox that is not the first", round, ids)
		}
	}
}

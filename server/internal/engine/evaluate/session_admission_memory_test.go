package evaluate

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/record"
)

// Run each request count in a separate process, without -race. This measures
// live Go heap at a controlled queue plateau, not RSS or the process high-water
// mark. The core is held so acquired real snapshots remain on caller stacks.
func TestRetainedGraphQueueHeap(t *testing.T) {
	if os.Getenv("FRONTSEAT_RETAINED_QUEUE_HEAP") == "" {
		t.Skip("opt-in queue heap measurement")
	}
	requests, err := strconv.Atoi(os.Getenv("FRONTSEAT_RETAINED_QUEUE_HEAP"))
	if err != nil || requests < 1 || requests > 256 {
		t.Fatal("request count must be 1..256")
	}
	root := retainedFixture(t, 500)
	synctest.Test(t, func(t *testing.T) {
		files := &source.Keeper{}
		var acquisitions atomic.Int32
		s := newSession(t, nil, RetentionLimits{8192, 8 << 20}, 1, &Answers{}, func(ctx context.Context) (Source, []record.Located, error) {
			src, err := files.Take(ctx, source.Tree{Root: root, Paths: map[string]string{"": "."}})
			if err == nil {
				acquisitions.Add(1)
			}
			return src, nil, err
		})
		ctx, cancel, done := heldCore(t, s)
		defer done()
		var before, queued, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		results := make(chan error, requests)
		for range requests {
			go func() { _, err := s.Projects(ctx); results <- err }()
		}
		synctest.Wait()
		runtime.GC()
		runtime.ReadMemStats(&queued)
		acquired := acquisitions.Load()
		cancel()
		synctest.Wait()
		for range requests {
			if err := <-results; !errors.Is(err, context.Canceled) {
				t.Errorf("queued caller: %v", err)
			}
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		runtime.KeepAlive(s)
		t.Logf("requests=%d modules=500 acquisitions=%d heap_before=%d heap_queued=%d heap_after_cancel=%d heap_queue_delta=%d gomaxprocs=%d", requests, acquired, before.HeapAlloc, queued.HeapAlloc, after.HeapAlloc, int64(queued.HeapAlloc)-int64(before.HeapAlloc), runtime.GOMAXPROCS(0))
	})
}

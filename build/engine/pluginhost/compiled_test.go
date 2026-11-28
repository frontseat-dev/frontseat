package pluginhost_test

import (
	"context"
	"crypto/rand"
	"runtime"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"github.com/frontseat-dev/frontseat/testsupport/testmodule"
)

// A plugin is compiled once in a process: a host made after another
// loaded it, and closed, loads it without compiling it again, as every
// daemon a test starts does. The control: the first load of a module no
// host has loaded compiles it, and allocates for each of its functions.
func TestAPluginCompiledOnceIsNotCompiledAgainByAnotherHost(t *testing.T) {
	ctx := context.Background()
	base, err := testmodule.Plugin("npm")
	if err != nil {
		t.Fatal(err)
	}
	// A custom section of random bytes, which wasm ignores, makes a
	// module of a digest of its own each time the test runs.
	module := append(append([]byte{}, base...), 0, 25, 8, 'c', 'o', 'm', 'p', 'i', 'l', 'e', 'd')
	module = append(module, rand.Text()[:16]...)
	first := loadAllocations(t, ctx, module)
	again := loadAllocations(t, ctx, module)
	t.Logf("first load %d allocations, again in another host %d", first, again)
	// A load from the cache makes about 10,600 allocations, a compiling
	// one about 42,000.
	if first < 25000 {
		t.Fatalf("the first load made %d allocations: it compiled nothing", first)
	}
	// What is left of a load from the cache is decoding the module,
	// instantiating it and asking what it is: about a quarter of the first.
	if again*3 > first {
		t.Fatalf("a second host's load made %d allocations, the first's %d: it compiled the module again", again, first)
	}
}

// loadAllocations is what a new host allocates loading the module, the
// host closed after.
func loadAllocations(t *testing.T, ctx context.Context, module []byte) uint64 {
	t.Helper()
	host, err := pluginhost.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	at := time.Now()
	plugin, err := host.Load(ctx, module)
	took := time.Since(at)
	runtime.ReadMemStats(&after)
	t.Logf("a load took %v", took)
	if err != nil {
		t.Fatal(err)
	}
	if err := plugin.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	return after.Mallocs - before.Mallocs
}

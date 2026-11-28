package pluginhost

import (
	"context"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/testsupport/testbudget"
	"github.com/frontseat-dev/frontseat/testsupport/testmodule"
	"github.com/tetratelabs/wazero"
)

// BenchmarkCompile is what a process pays the first time it loads a
// plugin, the Go plugin's: compiling it, on a runtime configured as the
// host's runtimes are and sharing nothing they compiled.
func BenchmarkCompile(b *testing.B) {
	ctx := context.Background()
	module, err := testmodule.Plugin("go")
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		runtimeCreation.Lock()
		rt := wazero.NewRuntimeWithConfig(ctx, configuration())
		runtimeCreation.Unlock()
		if _, err := rt.CompileModule(ctx, module); err != nil {
			b.Fatal(err)
		}
		if err := rt.Close(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// Measured 2026-11-28 with `go run ./specs/cmd/gate go-test -count=3
// -run '^$' -bench 'BenchmarkCompile$' -benchmem ./build/engine/pluginhost`
// on an Apple M-series laptop: compiling took 57.4–59.5 ms and
// 32,554–32,562 allocations. A module that grows costs more to compile,
// once in each process now: this holds what loading a plugin was held to
// before a process compiled each one once, at that budget's time
// (280 ms, about five times what was measured) and with allocations about
// a tenth more than measured.
func TestBudgetOfCompilingAPlugin(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 280 * time.Millisecond, Allocs: 35800}, BenchmarkCompile)
}

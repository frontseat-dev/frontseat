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
// plugin, the Go plugin's: compiling it as a host does, on a runtime
// configured as the host's runtimes are and sharing nothing they
// compiled.
func BenchmarkCompile(b *testing.B) {
	ctx := compiling(context.Background())
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
//
// Measured 2026-11-28 the same way once a module's functions came to be
// compiled four at once (compileWorkers): compiling took 19.2–19.9 ms and
// 72,004–72,817 allocations, 42 MB allocated (not the peak), where it had taken 57.4–59.5 ms and
// 32,554, 16.9 MB, each worker holding a compiler of its own; two at once
// took 31.8–32.2 ms and about 50,000. It is paid once on a machine, and
// once by a job on a runner of its own. The allowance for allocations is
// raised to 79,500 (about a tenth more), on the owner's word
// (2026-11-28); the time allowance is unchanged.
func TestBudgetOfCompilingAPlugin(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 280 * time.Millisecond, Allocs: 79500}, BenchmarkCompile)
}

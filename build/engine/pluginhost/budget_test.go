package pluginhost_test

import (
	"context"
	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost/conformance"
	"github.com/frontseat-dev/frontseat/testsupport/testbudget"
	"testing"
	"time"
)

// benchmarkAnalysis has a plugin analyze a source in each round of a
// benchmark, and holds each answer to the contract as a run holds it.
func benchmarkAnalysis(b *testing.B, plugin *pluginhost.Plugin, name string, source files, request *pluginv1.AnalyzeRequest) {
	b.Helper()
	for b.Loop() {
		response := new(pluginv1.AnalyzeResponse)
		if _, err := plugin.Call(b.Context(), source, "Analyze", request, response); err != nil {
			b.Fatal(err)
		}
		if err := pluginhost.CheckAnalysis(name, request.Roots, response); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLoad is a module made ready to be asked by a host: compiled
// by the first round alone, then in each round taken from what the
// process compiled, instantiated, and asked what it is.
func BenchmarkLoad(b *testing.B) {
	ctx := context.Background()
	host, err := pluginhost.New(ctx)
	if err != nil {
		b.Fatal(err)
	}
	defer host.Close(ctx)
	for b.Loop() {
		plugin, err := host.Load(ctx, module)
		if err != nil {
			b.Fatal(err)
		}
		_ = plugin.Close(ctx)
	}
}

// BenchmarkAnalyze is one module with a lock analyzed, across the
// boundary and back.
func BenchmarkAnalyze(b *testing.B) {
	ctx := context.Background()
	plugin := conformance.Load(b, module)
	source := files{"go.mod": goMod, "go.sum": goSum}
	for b.Loop() {
		if _, err := plugin.Call(ctx, source, "Analyze", &pluginv1.AnalyzeRequest{Context: goContext()}, &pluginv1.AnalyzeResponse{}); err != nil {
			b.Fatal(err)
		}
	}
}

// Measured 2026-11-05 on an Apple M-series laptop, three runs of
// `go test -run TestBudget -v ./internal/pluginhost/`: loading took 21.2
// to 22.3 ms and 18,352 allocations, analyzing 134 to 144 µs and 541.
// The time allowed is ten times that, the allocations a tenth more.
//
// Both were raised when each call was given an instance of its own, so
// that a call that is cancelled or goes wrong cannot reach the next:
// analyzing was 47 µs and 246 allocations on one shared instance, and
// the 87 µs more is what an instance costs. Loading had been measured at
// 0.98 ms only because the runtime still held the compiled module of
// the iteration before; a plugin that is closed now lets it go, and
// 21 ms is what compiling one really takes, once for each daemon.
//
// Measured again 2026-11-06, the same way, the Go plugin's module being
// 114 KB: loading takes 27.7 to 27.9 ms and 21,036 allocations, and
// analyzing 155 µs and 583.
//
// Measured 2026-11-07 with `mise exec -- go test -count=3 -run '^$'
// -bench 'Benchmark(Analyze|Load)$' -benchmem ./build/engine/pluginhost`:
// loading takes 24.0–24.8 ms and 20,556–20,577 allocations; analyzing takes
// 138.8–140.3 µs and 628 allocations. Analysis now returns the additional
// locked-metadata fetch action needed for offline workspace builds. Its
// protobuf fields raise the allocation allowance to 690 (about 10% headroom);
// the time allowance is unchanged.
//
// Measured 2026-11-07 after typed options and structured failures, with
// `mise exec -- go test -run '^$' -bench 'Benchmark(Load|Analyze)$' -benchmem
// -count=3 ./build/engine/pluginhost`: loading took 26.80–27.21 ms
// and 21,752–21,772 allocations; analysis took 149.4–189.1 µs and 645
// allocations. Both remain within the existing budgets; neither is raised.
//
// Measured 2026-11-08 after the check/fix contract, with `mise exec -- go test
// -run '^$' -bench 'BenchmarkAnalyze$' -benchmem -count=3
// ./build/internal/pluginhost`: analysis took 155.6–160.4 µs and 723
// allocations. The Go plugin now answers a fourth action, format, with its
// fix command and rewrite patterns, and excludes nested modules from each
// input set; the allocation allowance is raised to 800 (about 10% headroom).
// The time allowance is unchanged.
//
// Measured 2026-11-08 after the Go plugin came to check each go.sum line as
// golang.org/x/mod/module.Check does, with `mise exec -- go test -run '^$'
// -bench 'Benchmark(Load|Analyze)$' -benchmem -count=3
// ./build/internal/pluginhost`: loading took 36.7–37.4 ms and 24,654–24,683
// allocations, analysis 194.5–201.8 µs and 758. Its module holds the more
// code, and compiling it once for each daemon allocates for each of its
// functions: the allocation allowance for loading is raised to 27,100
// (about 10% headroom). The time allowances are unchanged.
//
// Measured 2026-11-11 after the SDK came to answer Branch and Version, with
// `mise exec -- go test -run '^$' -bench 'BenchmarkLoad$' -benchmem -count=3
// ./build/internal/pluginhost` in each checkout: main at 9be1408 loaded in
// 39.8–41.3 ms with 26,090–26,129 allocations, and this change in 42.3–43.5
// ms with 30,848–30,890. Every module now holds the code that reads the two
// requests, the Go plugin's among them though it serves neither (its module
// went from 175,739 to 192,715 bytes), and compiling a module allocates for
// each of its functions. The allocation allowance for loading is raised to
// 34,000 (about 10% headroom). The time allowance is unchanged.
//
// Measured 2026-11-12 after the Go plugin came to say a test for each
// package and its shards, with the same command in each checkout: main at
// 4828651 loaded in 47.0–47.8 ms with 33,233–33,278 allocations, within 2.2%
// of the allowance, and this change in 48.6–48.7 ms with 34,202–34,250. The
// Go module holds the code that hands the go command a module's tests (its
// module went from 217,449 bytes to 233,411); four rounds of making
// that code smaller took it from 34,648 and no further. The allocation
// allowance for loading is raised to 37,600 (about 10% headroom), on the
// owner's word (2026-11-12). The time allowance is unchanged.
//
// Measured 2026-11-22 with `go test -run '^$' -bench 'BenchmarkAnalyze$'
// -benchmem -count=3 ./internal/pluginhost`: analysis had come to 800
// allocations on arm64 and 802 on amd64 (801 on the grid's x86-64
// workers), at and over the allowance. Each instance was made with a
// configuration built anew, copied by each of wazero's With calls, and
// asked to start with an _initialize no module exports, of whose absence
// wazero made an error each time. Made once as a module is loaded, and
// asking for _initialize only of a module that exports it, analysis
// takes 790 allocations on arm64 and 791 on amd64. Neither allowance is
// raised.
//
// Measured 2026-11-28 with `go run ./specs/cmd/gate go-test -count=3 -run
// '^$' -bench 'BenchmarkLoad$' -benchmem ./build/engine/pluginhost`, after
// a process came to compile each plugin once and every host to load it
// from what was compiled: loading took 2.0–2.1 ms and 8,267–8,331
// allocations, where it had taken 58.6–60.1 ms and 34,060–34,113 at main
// (4cab4f0b), measured the same way. What compiling costs is held by
// TestBudgetOfCompilingAPlugin. The allowance for loading is lowered to
// 9,200 allocations (about a tenth more) and 10 ms, about five times what
// was measured.

func TestBudgetOfLoadingAPlugin(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 10 * time.Millisecond, Allocs: 9200}, BenchmarkLoad)
}

// Measured 2026-11-28 with `go run ./specs/cmd/gate go-test -count=3 -run
// '^$' -bench 'Benchmark(Analyze|Load)$' -benchmem ./build/engine/pluginhost`,
// once a module's linear memory came to be reserved from the kernel and not
// grown on the heap (allocate, as frontseat-archive did in f85d3109):
// analysis took 151–153 µs, 105 KB and 791 allocations, where it had taken
// 176–186 µs, 1.22 MB and 790; loading 0.79 MB, where it took 3.0 MB.
// Neither allowance is raised.
func TestBudgetOfAnalyzingAModule(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 1500 * time.Microsecond, Allocs: 800}, BenchmarkAnalyze)
}

// BenchmarkReport is a check's output read for its findings through the
// real Go plugin: what every check of every run costs once it has ended.
func BenchmarkReport(b *testing.B) {
	ctx := context.Background()
	plugin := conformance.Load(b, module)
	request := vetFailed("# example.com/hello\n./main.go:6:14: fmt.Printf format %d has arg \"x\" of wrong type string\n./sub/x.go:3:1: unreachable code\n")
	for b.Loop() {
		response := new(pluginv1.ReportResponse)
		if _, err := plugin.Call(ctx, files{}, "Report", request, response); err != nil {
			b.Fatal(err)
		}
		if err := pluginhost.CheckReport("go", response); err != nil || len(response.GetDiagnostics()) != 2 {
			b.Fatalf("%v %v", response, err)
		}
	}
}

// Measured 2026-11-08 on an Apple M4 Max, three runs of
// `go test -run '^$' -bench '^BenchmarkReport$' -benchmem -count=3
// ./build/engine/pluginhost/`: 83.6 to 94.0 µs and 364 host
// allocations for two findings through the real Go plugin. The time
// allowed is ten times that, the allocations a tenth more.
func TestBudgetOfReadingACheckForItsFindings(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 1 * time.Millisecond, Allocs: 400}, BenchmarkReport)
}

package compare_test

// What frontseat is held to: the same work built by Bazel on the same
// grid, each of its actions run in the same runner sandboxes, where
// frontseat's are also confined by frontseat-confine. Each case is built by both in
// rounds, the one that goes first alternating, so that what the machine
// does meanwhile falls on both; a case fails where frontseat is clearly
// more than its allowance times as slow: where the lower bound of a 95%
// interval of the round's ratios, frontseat's over Bazel's, is above it.
// It is run when asked for (mise run perf), on a grid nothing else uses.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/frontseat-dev/frontseat/local/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/sdk/go"
	"github.com/frontseat-dev/frontseat/testsupport/testmodule"
	"github.com/frontseat-dev/frontseat/testsupport/testworkload"
)

var (
	perf        = flag.Bool("perf", false, "hold frontseat to Bazel on the same grid: build each case with both, in rounds, and fail where frontseat is over its allowance")
	rounds      = flag.Int("rounds", 10, "how many rounds each case is built in, by each tool")
	firstRounds = flag.Int("first-rounds", 10, "how many rounds the first build, of a workspace no cache has seen, is built in")
)

// allowances are how many times as long as Bazel frontseat may take, by
// case (owner, 2026-11-09).
//
// The baseline, `mise run perf` on 2026-11-09 at 51623f0 (main 230a8d1),
// a thousand modules in groups of ten, ten rounds each, on an Apple M4
// Max with the local grid's eight slots in OrbStack's ten CPUs, Bazel
// 9.3.2; the machine was not quiet (an IDE indexing and a k3d cluster in
// the same VM), so the intervals are wide and the medians the better
// guide. frontseat's median, Bazel's, their ratio and its 95% interval:
//
//	the first build                     20506 ms  20822 ms  0.94  0.81 to 1.28
//	nothing changed                       112 ms    364 ms  0.33  0.22 to 0.48
//	a file nothing else needs changed     179 ms    412 ms  0.42  0.32 to 0.59
//	a file its group needs changed        405 ms    647 ms  0.74  0.56 to 1.20
//
// Of the first build not judged, on the grid: frontseat's actions held a
// worker 37.6 ms each and the build was on the grid 16.4 s; Bazel's 42.9
// ms and 19.9 s. A run on a quiet machine replaces these.
var allowances = map[string]float64{
	"the first build":                   1.2,
	"nothing changed":                   1.0,
	"a file nothing else needs changed": 1.0,
	"a file its group needs changed":    1.2,
}

// cases are the cases in the order they are built.
var cases = []string{"the first build", "nothing changed", "a file nothing else needs changed", "a file its group needs changed"}

// bazelOnGrid is Bazel, as this repository pins it, building a workspace
// on the local grid, its actions confined as frontseat's are. Before it
// is timed its server is started, as frontseat's daemon is, and the
// modules it is built with resolved: each new workspace otherwise
// downloads its modules' files from the Bazel Central Registry, 4.9 s of
// the 6.9 s a first build of fifty modules took (its profile, 2026-11-09),
// work of the network that frontseat does none of. log names a file the
// next build writes what it ran to, once.
func bazelOnGrid(t *testing.T, shape testworkload.Shape, log *string) tool {
	t.Helper()
	root, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	// The pinned Bazel, as the repository's mise gives it, run in the
	// workspace: what is timed is Bazel, not a launcher.
	path := strings.TrimSpace(output(t, root, nil, "mise", "which", "bazel"))
	version := strings.TrimPrefix(strings.TrimSpace(output(t, t.TempDir(), nil, path, "--version")), "bazel ")
	platform := testgrid.Platform()
	dir := t.TempDir()
	if err := testworkload.Bazel(dir, shape, version, &testworkload.Grid{Address: testgrid.Address(t), Platform: platform, Confined: testgrid.Confined(t)}); err != nil {
		t.Fatal(err)
	}
	output(t, dir, nil, path, "mod", "deps")
	tl := bazelTool(path, version, dir, log)
	t.Cleanup(tl.close)
	return tl
}

// minimumRounds is the fewest rounds a case is judged in: with fewer, the
// lower bound of the interval is one round's own ratio.
const minimumRounds = 8

// bazelTool is a Bazel at a path building a workspace in a directory.
// It is let go of once, whoever asks first.
func bazelTool(path, version, dir string, log *string) tool {
	var once sync.Once
	stop := func() {
		once.Do(func() { stopBazel(path, dir) })
	}
	return tool{name: "Bazel", close: stop, about: "bazel " + version + ", remote on the local grid, each action in its runner sandboxes", dir: dir, try: func(_ *testing.T, target string) (string, error) {
		cmd := exec.Command(path, "build", target)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		return string(out), err
	}, change: onThisMachine(dir), counted: regexp.MustCompile(`Elapsed time: ([0-9.]+s)`), build: func(t *testing.T) string {
		args := []string{"build", "//..."}
		if *log != "" {
			args = append(args, "--execution_log_json_file="+*log)
			*log = ""
		}
		return output(t, dir, nil, path, args...)
	}, ran: func(_ *testing.T, out string) string { return remotely(out) }}
}

// summary is the line in which Bazel counts the processes of a build, by
// kind: "INFO: 3,001 processes: 1 internal, 3,000 remote.", or "6 remote
// cache hit" where the grid answered from what it kept.
var (
	summary = regexp.MustCompile(`INFO: [\d,]+ process(?:es)?: ([^\n]*)\.`)
	kinds   = regexp.MustCompile(`^([\d,]+) (.+)$`)
)

// remotely is how many of a build's actions Bazel had the grid run, as
// it counts them: not those the grid answered from what it kept, nor
// its own; "?" where it says nothing that can be read so.
func remotely(out string) string {
	found := summary.FindStringSubmatch(out)
	if found == nil {
		return "?"
	}
	ran := 0
	for _, part := range strings.Split(found[1], ", ") {
		kind := kinds.FindStringSubmatch(strings.TrimSpace(part))
		if kind == nil {
			return "?"
		}
		if kind[2] == "remote" {
			n, err := strconv.Atoi(strings.ReplaceAll(kind[1], ",", ""))
			if err != nil {
				return "?"
			}
			ran += n
		}
	}
	return strconv.Itoa(ran)
}

// sample is one build of a case by each tool, in one round.
type sample struct{ frontseat, bazel built }

// held builds a case with both tools in a round, the one that goes first
// as the round says, and holds each to having run what the case runs.
func held(t *testing.T, round int, want string, before func(tl tool, n int), tools ...tool) sample {
	t.Helper()
	order := []int{0, 1}
	if round%2 == 1 {
		order = []int{1, 0}
	}
	var got [2]built
	for _, i := range order {
		tl := tools[i]
		before(tl, round)
		one := tl.timed(t)
		after := tl.ran(t, tl.build(t))
		one.want, one.late = want, after != "0"
		if one.missed() {
			t.Fatalf("%s ran %s and then %s where the case runs %s: what it is timed for is not the work", tl.name, one.ran, after, want)
		}
		got[i] = one
	}
	return sample{frontseat: got[0], bazel: got[1]}
}

// ratios are frontseat's time over Bazel's, round by round.
func ratios(samples []sample) []float64 {
	out := make([]float64, len(samples))
	for i, s := range samples {
		out[i] = float64(s.frontseat.took) / float64(s.bazel.took)
	}
	return out
}

// median is the middle of some values, or the mean of the two in the
// middle.
func median(values []float64) float64 {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// interval is a 95% interval of the median of some values, by resampling
// them with replacement (the percentile bootstrap), from a fixed seed so
// that the same values give the same interval.
func interval(values []float64) (low, high float64) {
	random := rand.New(rand.NewPCG(1, 2))
	const draws = 10000
	medians := make([]float64, draws)
	again := make([]float64, len(values))
	for d := range draws {
		for i := range again {
			again[i] = values[random.IntN(len(values))]
		}
		medians[d] = median(again)
	}
	slices.Sort(medians)
	return medians[draws*25/1000], medians[draws*975/1000]
}

// over says whether a case is clearly over its allowance: the lower
// bound of its interval is above it.
func over(values []float64, allowance float64) bool {
	low, _ := interval(values)
	return low > allowance
}

func TestPerf(t *testing.T) {
	if !*perf {
		t.Skip("held when asked for: mise run perf")
	}
	// Each tool goes first as often as the other; and with fewer than
	// eight rounds the interval's bound is one round's own ratio, so that
	// one quick round decides (TestACaseIsOverItsAllowanceOnlyWhereItIsClearlySo).
	if *rounds < minimumRounds || *firstRounds < minimumRounds || *rounds%2 != 0 || *firstRounds%2 != 0 {
		t.Fatalf("-rounds and -first-rounds are even, and %d or more", minimumRounds)
	}
	shape := func() testworkload.Shape {
		return testworkload.Shape{Modules: *modules, Group: *group, Salt: time.Now().Format(time.RFC3339Nano)}
	}
	samples := map[string][]sample{}
	var bazelLog string
	every := strconv.Itoa(len(testworkload.Actions) * *modules)

	// A first build of each that is not judged: Bazel writes what it ran,
	// which is work of its own no judged build does, and what the grid
	// recorded of each is read at once, before the action cache lets it
	// go for what later builds crowd in.
	logged := filepath.Join(t.TempDir(), "executed.json")
	fs, bz := frontseatInASandbox(t, shape()), bazelOnGrid(t, shape(), &bazelLog)
	bazelLog = logged
	held(t, 0, every, func(tool, int) {}, fs, bz)
	recorded := [2]string{phases(t, "frontseat", ranOnTheGrid(t, fs)), phases(t, "Bazel", executed(t, logged))}
	fs.close()
	bz.close()

	// The first build, of a workspace no cache has seen: each round, both
	// tools are given a workspace of their own, with a salt of its own,
	// and let go of it when the round is over.
	for round := range *firstRounds {
		fs, bz := frontseatInASandbox(t, shape()), bazelOnGrid(t, shape(), &bazelLog)
		samples["the first build"] = append(samples["the first build"], held(t, round, every, func(tool, int) {}, fs, bz))
		fs.close()
		bz.close()
	}

	// The rest, on one workspace each, built once before.
	fs, bz = frontseatInASandbox(t, shape()), bazelOnGrid(t, shape(), &bazelLog)
	fs.timed(t)
	bz.timed(t)
	one, groupOf := strconv.Itoa(len(testworkload.Actions)), strconv.Itoa(len(testworkload.Actions)*min(*group, *modules))
	change := func(module string) func(tool, int) {
		return func(tl tool, n int) {
			tl.change(t, module+"/word.go", testworkload.Word(fmt.Sprintf("%s changed %d at %d", module, n, time.Now().UnixNano())))
		}
	}
	for round := range *rounds {
		samples["nothing changed"] = append(samples["nothing changed"], held(t, round, "0", func(tool, int) {}, fs, bz))
		samples["a file nothing else needs changed"] = append(samples["a file nothing else needs changed"], held(t, round, one, change(testworkload.Module(1)), fs, bz))
		samples["a file its group needs changed"] = append(samples["a file its group needs changed"], held(t, round, groupOf, change(testworkload.Module(0)), fs, bz))
	}

	var table strings.Builder
	fmt.Fprintf(&table, "\n%d modules of %d actions, in groups of %d; the first build in %d rounds, the rest in %d\n\n", *modules, len(testworkload.Actions), *group, *firstRounds, *rounds)
	fmt.Fprintf(&table, "| case | frontseat, median | Bazel, median | frontseat / Bazel | 95%% interval | allowed |\n|---|---|---|---|---|---|\n")
	for _, name := range cases {
		s := samples[name]
		var f, b []float64
		for _, each := range s {
			f = append(f, float64(each.frontseat.took.Milliseconds()))
			b = append(b, float64(each.bazel.took.Milliseconds()))
		}
		r := ratios(s)
		low, high := interval(r)
		verdict := ""
		if over(r, allowances[name]) {
			verdict = " OVER"
			t.Errorf("%s: frontseat took %.2f times as long as Bazel (%.2f to %.2f), more than the %.2f allowed", name, median(r), low, high, allowances[name])
		}
		fmt.Fprintf(&table, "| %s | %.0f ms | %.0f ms | %.2f | %.2f to %.2f | %.2f%s |\n", name, median(f), median(b), median(r), low, high, allowances[name], verdict)
	}
	fmt.Fprintf(&table, "\nThe actions of a first build that is not judged, as the grid's action cache says each went: %s\n%s\n", recorded[0], recorded[1])
	fmt.Fprintf(&table, "\nfrontseat: %s\nBazel: %s\n", fs.about, bz.about)
	t.Log(table.String())
}

// ranOnTheGrid returns what the grid knows each action of frontseat's
// first build by: of its runs, the one that ran the most.
func ranOnTheGrid(t *testing.T, fs tool) []*repb.Digest {
	t.Helper()
	listed, err := frontseat.Collect(fs.fixture.Builds(context.Background()))
	if err != nil || len(listed) == 0 {
		t.Fatalf("no run to read: %v", err)
	}
	most := listed[0]
	for _, run := range listed {
		if run.GetCost().GetActionsExecuted() > most.GetCost().GetActionsExecuted() {
			most = run
		}
	}
	actions, err := frontseat.Collect(fs.fixture.BuildByID(most.GetId()).Actions(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	var out []*repb.Digest
	for _, action := range actions {
		if action.GetWorkerTime() != nil && action.GetActionDigest() != nil {
			out = append(out, &repb.Digest{Hash: action.GetActionDigest().GetHash(), SizeBytes: action.GetActionDigest().GetSizeBytes()})
		}
	}
	return out
}

// executed returns what the grid knows each action Bazel ran remotely by,
// from its execution log.
func executed(t *testing.T, log string) []*repb.Digest {
	t.Helper()
	raw, err := os.Open(log)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	decoder := json.NewDecoder(raw)
	var out []*repb.Digest
	for decoder.More() {
		var spawn struct {
			Runner   string `json:"runner"`
			CacheHit bool   `json:"cacheHit"`
			Digest   struct {
				Hash      string `json:"hash"`
				SizeBytes string `json:"sizeBytes"`
			} `json:"digest"`
		}
		if err := decoder.Decode(&spawn); err != nil {
			t.Fatal(err)
		}
		// Run on the grid by this build: a hit of the grid's cache is
		// "remote cache hit", and its metadata is of the build that ran it
		// (Bazel's spawn.proto, SpawnExec.runner).
		if spawn.Runner == "remote" && !spawn.CacheHit && spawn.Digest.Hash != "" {
			size, _ := strconv.ParseInt(spawn.Digest.SizeBytes, 10, 64)
			out = append(out, &repb.Digest{Hash: spawn.Digest.Hash, SizeBytes: size})
		}
	}
	return out
}

// phases says, of some actions, how long each waited for a worker, ran,
// and had its outputs taken, and how long a worker was held for it in
// all, as the worker recorded it in the grid's action cache: medians, in
// milliseconds. Running includes being given its inputs: the local
// grid's workers give an action its root as a FUSE file system, which
// fetches a file when it is read. And of them all, from the first asked
// for to the last done, how long that was and how much of it workers were
// held, summed over the workers: where one tool's build is longer, this
// says whether its actions held a worker longer, or the workers waited
// between them.
func phases(t *testing.T, name string, digests []*repb.Digest) string {
	conn, err := grpc.NewClient(testgrid.Address(t), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return name + ": " + err.Error()
	}
	defer conn.Close()
	cache := repb.NewActionCacheClient(conn)
	ms := func(from, to *timestamppb.Timestamp) float64 {
		return float64(to.AsTime().Sub(from.AsTime()).Microseconds()) / 1000
	}
	var queued, executing, uploading, holding []float64
	var first, last time.Time
	held := 0.0
	for _, digest := range digests {
		got, err := cache.GetActionResult(context.Background(), &repb.GetActionResultRequest{ActionDigest: digest})
		if err != nil {
			continue
		}
		m := got.GetExecutionMetadata()
		queued = append(queued, ms(m.GetQueuedTimestamp(), m.GetWorkerStartTimestamp()))
		executing = append(executing, ms(m.GetExecutionStartTimestamp(), m.GetExecutionCompletedTimestamp()))
		uploading = append(uploading, ms(m.GetOutputUploadStartTimestamp(), m.GetOutputUploadCompletedTimestamp()))
		holding = append(holding, ms(m.GetWorkerStartTimestamp(), m.GetWorkerCompletedTimestamp()))
		held += holding[len(holding)-1]
		if at := m.GetQueuedTimestamp().AsTime(); first.IsZero() || at.Before(first) {
			first = at
		}
		if at := m.GetWorkerCompletedTimestamp().AsTime(); at.After(last) {
			last = at
		}
	}
	if len(executing) == 0 {
		return name + ": the grid holds nothing of them"
	}
	return fmt.Sprintf("%s: %d of %d actions, waited %.1f ms, executed %.1f ms, outputs taken %.1f ms, held a worker %.1f ms; %.1f s from the first asked for to the last done, workers held %.1f s of it in all",
		name, len(executing), len(digests), median(queued), median(executing), median(uploading), median(holding), last.Sub(first).Seconds(), held/1000)
}

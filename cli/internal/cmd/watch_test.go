package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"

	"github.com/frontseat-dev/frontseat/build/engine/engine/source/sourcetest"
	"github.com/frontseat-dev/frontseat/cli/internal/machine"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
)

// rounds is a build daemon as a watch asks it: it counts the builds it
// was asked for, and ends each as the test says. Each build lists the
// directory watched as a run that begins lists it, and reads by name
// what the test says, and a wait for what it read to change is the
// engine's own (sourcetest.Ran), as a daemon's is.
type rounds struct {
	buildv1alpha1connect.UnimplementedBuildServiceHandler
	// directory is what each build lists, and reads are the files each
	// reads by name beneath it, as a plugin does.
	directory string
	reads     []string
	// unwaited, where set, is what each wait is refused with: a daemon
	// replaced, or ending, as it was waited on.
	unwaited error
	mu       sync.Mutex
	created  int
	// waits is how to wait for what each build read to change, by its id.
	waits map[string]func(context.Context) ([]string, error)
	// builds is what each build was asked for with.
	builds []*buildv1.CreateBuildRequest
	// ends is how each build ends, the first first; one it does not say
	// passes. refuses is what a build is refused with when it is asked
	// for, by its number, from one.
	ends    []buildv1.ExitCode
	refuses map[int]error
	// holds, where set, is waited on before a build ends, and followed
	// is told each time a build's events are asked for.
	holds    chan struct{}
	followed chan struct{}
}

func (r *rounds) CreateBuild(_ context.Context, request *buildv1.CreateBuildRequest) (*buildv1.CreateBuildResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.created++
	r.builds = append(r.builds, request)
	if err := r.refuses[r.created]; err != nil {
		return nil, err
	}
	id := fmt.Sprintf("build_%d", r.created)
	wait, err := sourcetest.Ran(context.Background(), r.directory, time.Millisecond, r.reads...)
	if err != nil {
		return nil, err
	}
	if r.waits == nil {
		r.waits = map[string]func(context.Context) ([]string, error){}
	}
	r.waits[id] = wait
	return &buildv1.CreateBuildResponse{Build: &buildv1.Build{Id: id}}, nil
}

func (r *rounds) WaitSourceChanged(ctx context.Context, request *buildv1.WaitSourceChangedRequest) (*buildv1.WaitSourceChangedResponse, error) {
	r.mu.Lock()
	wait := r.waits[request.GetBuildId()]
	r.mu.Unlock()
	switch {
	case r.unwaited != nil:
		return nil, r.unwaited
	case wait == nil:
		return nil, connect.NewError(connect.CodeNotFound, "no build has the id")
	}
	changed, err := wait(ctx)
	if err != nil {
		return nil, err
	}
	return &buildv1.WaitSourceChangedResponse{SourceChanged: &buildv1.SourceChanged{BuildId: request.GetBuildId(), Paths: changed}}, nil
}

func (r *rounds) StreamBuildEvents(ctx context.Context, request *buildv1.StreamBuildEventsRequest, stream buildv1alpha1connect.BuildServiceStreamBuildEventsServerStream) error {
	var number int
	if _, err := fmt.Sscanf(request.GetBuildId(), "build_%d", &number); err != nil {
		return err
	}
	select {
	case r.followed <- struct{}{}:
	default:
	}
	if r.holds != nil {
		select {
		case <-r.holds:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	code := buildv1.ExitCode_EXIT_CODE_SUCCESS
	if number <= len(r.ends) {
		code = r.ends[number-1]
	}
	return stream.Send(&buildv1.StreamBuildEventsResponse{Events: []*buildv1.BuildEvent{
		{Event: &buildv1.BuildEvent_Finished_{Finished: &buildv1.BuildEvent_Finished{Build: &buildv1.Build{ExitCode: code}}}},
	}})
}

// asked is how many builds it was asked for.
func (r *rounds) asked() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.created
}

// spoken is what a command that goes on printed so far, read while it
// prints.
type spoken struct {
	mu   sync.Mutex
	said bytes.Buffer
}

func (s *spoken) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.said.Write(p)
}

func (s *spoken) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.said.String()
}

// watch is a command line that watches, under way: the directory it
// builds, what it printed and said went wrong, what interrupts it, and
// how it ended.
type watch struct {
	directory string
	printed   *spoken
	interrupt context.CancelFunc
	ended     chan int
}

// watches runs a command line that watches a directory of the test's
// own, asking the daemons in turn, the last for every round after:
// each is the daemon the command line finds for the round, and waits,
// looking every millisecond, for what the round's build read to
// change. It starts no daemon. The directory holds a file a plugin
// reads by name beneath a directory whose name begins with a dot, which
// no listing takes. The command is interrupted when the test ends.
func watches(t *testing.T, daemons []*rounds, args ...string) *watch {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	w := &watch{directory: t.TempDir(), printed: &spoken{}, ended: make(chan int, 1)}
	w.write(t, "frontseat.yaml", "plugins: {}\n")
	w.write(t, "app/main.go", "package main\n")
	w.write(t, ".cargo/config.toml", "[build]\n")
	var at []string
	for _, d := range daemons {
		d.followed = make(chan struct{}, 1)
		d.directory = w.directory
		at = append(at, serving(t, func(api *connect.Server) { buildv1alpha1connect.RegisterBuildServiceHandler(api, d) }))
	}
	found, stood, again := builder, directory, askedAgain
	var turn sync.Mutex
	directory = func(bool) (string, error) { return w.directory, nil }
	builder = func(context.Context, string, bool) (machine.Builder, error) {
		turn.Lock()
		defer turn.Unlock()
		next := at[0]
		if len(at) > 1 {
			at = at[1:]
		}
		return machine.Builder{Address: strings.TrimPrefix(next, "http://"), Token: besideToken}, nil
	}
	askedAgain = 50 * time.Millisecond
	out, errOut = w.printed, w.printed
	ctx, interrupt := context.WithCancel(context.Background())
	w.interrupt = interrupt
	go func() {
		code, _ := Execute(ctx, args)
		w.ended <- code
	}()
	t.Cleanup(func() {
		interrupt()
		select {
		case code := <-w.ended:
			w.ended <- code
		case <-time.After(10 * time.Second):
			t.Error("the command went on after it was interrupted")
		}
		builder, directory, askedAgain = found, stood, again
		out, errOut = os.Stdout, os.Stderr
	})
	return w
}

// write writes a file of the directory watched.
func (w *watch) write(t *testing.T, name, content string) {
	t.Helper()
	testfiles.Write(t, filepath.Join(w.directory, filepath.FromSlash(name)), content)
}

// until waits for something to be so, and fails the test where it is
// not within ten seconds.
func (w *watch) until(t *testing.T, what string, so func() bool) {
	t.Helper()
	for begun := time.Now(); !so(); time.Sleep(2 * time.Millisecond) {
		if time.Since(begun) > 10*time.Second {
			t.Fatalf("%s: not within ten seconds; the command printed\n%s", what, w.printed)
		}
	}
}

// stays holds something to stay so for three hundred milliseconds,
// hundreds of the looks a wait under test takes, and the command to go
// on for as long.
func (w *watch) stays(t *testing.T, what string, so func() bool) {
	t.Helper()
	for begun := time.Now(); time.Since(begun) < 300*time.Millisecond; time.Sleep(2 * time.Millisecond) {
		select {
		case code := <-w.ended:
			w.ended <- code
			t.Fatalf("%s: the command ended, %d, having printed\n%s", what, code, w.printed)
		default:
		}
		if !so() {
			t.Fatalf("%s: no longer; the command printed\n%s", what, w.printed)
		}
	}
}

// ends holds the command to end with a code within five seconds.
func (w *watch) ends(t *testing.T, what string, want int) {
	t.Helper()
	select {
	case code := <-w.ended:
		w.ended <- code
		if code != want {
			t.Errorf("%s: the command ended %d, want %d; it printed\n%s", what, code, want, w.printed)
		}
	case <-time.After(5 * time.Second):
		t.Errorf("%s: the command went on for five seconds more; it printed\n%s", what, w.printed)
	}
}

// With --watch a build is built, and built again each time a file of
// the directory changes, and not otherwise: with nothing written, a
// watch that looks sixty times asks for no build. Before each build
// after the first it says, on one line, which files changed. The same
// holds of test. Interrupted while it waits, it exits 130 at once.
// Control, of the round that is not asked for: the write, which is.
func TestAWatchBuildsAgainWhenAFileChangesAndNotOtherwise(t *testing.T) {
	for _, command := range []string{"build", "test"} {
		t.Run(command, func(t *testing.T) {
			d := &rounds{}
			w := watches(t, []*rounds{d}, command, "--watch", "app", "--only", "lint")
			w.until(t, "the first build", func() bool { return d.asked() == 1 })
			w.stays(t, "with nothing written, one build", func() bool { return d.asked() == 1 })

			w.write(t, "app/main.go", "package main // more\n")
			w.until(t, "the build of the change", func() bool { return d.asked() == 2 })
			w.stays(t, "after one write, two builds", func() bool { return d.asked() == 2 })
			if printed := w.printed.String(); strings.Count(printed, "app/main.go changed: building again\n") != 1 || strings.Count(printed, "building again") != 1 || !strings.HasPrefix(printed, "watching "+w.directory+": ") {
				t.Errorf("it printed\n%s\nwant that it watches the directory, and one line of what changed", printed)
			}

			// Every round is asked for as the command was written: what
			// it was pointed at, the actions kept, and tests alone
			// where it tests.
			d.mu.Lock()
			for _, asked := range d.builds {
				if !slices.Equal(asked.GetTargets(), []string{"app"}) || !slices.Equal(asked.GetOnly(), []string{"lint"}) || asked.GetTests() != (command == "test") {
					t.Errorf("%s --watch app --only lint asked for a build of %v, only %v, tests: %t", command, asked.GetTargets(), asked.GetOnly(), asked.GetTests())
				}
			}
			d.mu.Unlock()

			for name, content := range map[string]string{"app/a.go": "a", "app/b.go": "b", "app/c.go": "c", "app/d.go": "d", "app/e.go": "e"} {
				w.write(t, name, content)
			}
			w.until(t, "the line of many files", func() bool {
				return strings.Contains(w.printed.String(), "app/a.go, app/b.go, app/c.go and 2 more changed, 5 files: building again\n")
			})
			w.interrupt()
			w.ends(t, "interrupted while it waits", ExitInterrupted)
		})
	}
}

// A file written while a round is under way is built by one round
// after it, whenever in the round it was written: the round is held
// against how the files looked before it was asked for. The round under
// way is followed to its end, and is not asked for again. Interrupted
// while a round is under way, the command exits 130 at once.
func TestAChangeDuringARoundIsBuiltByOneRoundAfterIt(t *testing.T) {
	d := &rounds{holds: make(chan struct{})}
	w := watches(t, []*rounds{d}, "build", "--watch")
	<-d.followed
	w.write(t, "app/main.go", "package main // written while it was built\n")
	w.stays(t, "while the round is under way, one build", func() bool { return d.asked() == 1 })
	d.holds <- struct{}{}
	w.until(t, "the round after", func() bool { return d.asked() == 2 })
	<-d.followed
	d.holds <- struct{}{}
	w.stays(t, "one round after the change", func() bool { return d.asked() == 2 })

	w.write(t, "app/main.go", "package main // and again\n")
	w.until(t, "a third round", func() bool { return d.asked() == 3 })
	<-d.followed

	// A file made and removed again while the round is under way, which
	// the round did not list, and so never read: every file it read is
	// as it was, and no round follows. Control: the writes before it,
	// and the one after, each followed by one.
	passing := filepath.Join(w.directory, "app", "passing.go")
	w.write(t, "app/passing.go", "package main\n")
	if err := os.Remove(passing); err != nil {
		t.Fatal(err)
	}
	d.holds <- struct{}{}
	w.stays(t, "after a file made and removed again while a round was under way, three rounds", func() bool { return d.asked() == 3 })
	w.write(t, "app/main.go", "package main // and once more\n")
	w.until(t, "a fourth round", func() bool { return d.asked() == 4 })
	<-d.followed
	w.interrupt()
	w.ends(t, "interrupted while a round is under way", ExitInterrupted)
}

// A daemon found as it was ending refuses the round by name and builds
// nothing: the round is asked again at once, of the daemon there is by
// then, with no change waited for. Refused so three times running, the
// command ends as of a daemon that does not answer. Control: refused
// UNAVAILABLE by no such name, a round is not asked again
// (TestARoundThatFailedWaitsForTheNextChange).
func TestARoundADaemonRefusedAsItWasEndingIsAskedOfTheNext(t *testing.T) {
	ending := func() error { return refusal(t, connect.CodeUnavailable, "DAEMON_ENDING", "the daemon is ending") }
	first, second := &rounds{refuses: map[int]error{1: ending()}}, &rounds{refuses: map[int]error{2: ending(), 3: ending(), 4: ending()}}
	w := watches(t, []*rounds{first, second}, "build", "--watch")
	w.until(t, "the round, of the daemon after the one that was ending", func() bool { return second.asked() == 1 })
	w.stays(t, "with nothing written, one round", func() bool { return first.asked() == 1 && second.asked() == 1 })
	w.write(t, "app/main.go", "package main // more\n")
	w.ends(t, "refused as ending three times running", ExitUnanswered)
	if second.asked() != 4 {
		t.Errorf("the second daemon was asked for %d builds; want one, and three for the round after", second.asked())
	}
}

// A round that failed is a round's result: the watch waits for the
// next change, and builds again. A build the daemon refuses of the
// directory as it is, which is said, built nothing to wait on: it is
// asked for again a while after, with nothing written, until it is
// built. What ends the command is a daemon that cannot be asked: the
// control, by the code of one that did not answer.
func TestARoundThatFailedWaitsForTheNextChange(t *testing.T) {
	d := &rounds{ends: []buildv1.ExitCode{buildv1.ExitCode_EXIT_CODE_BUILD_FAILURE, buildv1.ExitCode_EXIT_CODE_TESTS_FAILED}, refuses: map[int]error{
		3: connect.NewError(connect.CodeFailedPrecondition, "the workspace is not whole: it is being filled"),
		4: connect.NewError(connect.CodeFailedPrecondition, "the workspace is not whole: it is being filled"),
		6: connect.NewError(connect.CodeUnavailable, "the daemon is ending"),
	}}
	w := watches(t, []*rounds{d}, "test", "--watch")
	for round := 1; round <= 2; round++ {
		w.until(t, fmt.Sprintf("round %d", round), func() bool { return d.asked() == round })
		w.stays(t, fmt.Sprintf("after round %d, which failed, the watch", round), func() bool { return d.asked() == round })
		w.write(t, "app/main.go", fmt.Sprintf("package main // %d\n", round))
	}
	w.until(t, "the round refused, and asked for twice again with nothing written", func() bool { return d.asked() == 5 })
	w.stays(t, "after the round that was built, the watch", func() bool { return d.asked() == 5 })
	w.write(t, "app/main.go", "package main // 5\n")
	w.ends(t, "a daemon that did not answer", ExitUnanswered)
	if printed := w.printed.String(); d.asked() != 6 || !strings.Contains(printed, "the workspace is not whole: it is being filled") {
		t.Errorf("it asked for %d builds and printed\n%s\nwant six, and what the third was refused with", d.asked(), printed)
	}
}

// Each round asks the build daemon there is then: one that was replaced
// since the round before, its program, plugins or settings being
// others, or that ended itself with nothing to do, is found or started
// by the round as by any command, and the watch goes on with it.
func TestAWatchGoesOnWithTheDaemonThereIsForEachRound(t *testing.T) {
	first, second := &rounds{}, &rounds{}
	w := watches(t, []*rounds{first, second}, "build", "--watch")
	w.until(t, "the first build", func() bool { return first.asked() == 1 })
	w.write(t, "app/main.go", "package main // more\n")
	w.until(t, "the second build, of the daemon there is then", func() bool { return second.asked() == 1 })
	w.write(t, "app/main.go", "package main // and more\n")
	w.until(t, "the third build", func() bool { return second.asked() == 2 })
	if first.asked() != 1 {
		t.Errorf("the daemon that was replaced was asked for %d builds; want the first alone", first.asked())
	}
}

// A daemon that cannot answer the wait on its build, replaced by
// another command while it was waited on, is built again of at once,
// with nothing written, of the daemon there is then, which is waited
// on; where the wait after that cannot be answered either, the command
// ends. Control: TestAWatchGoesOnWithTheDaemonThereIsForEachRound,
// whose daemons answer, and are asked for a build only once something
// was written.
func TestAWaitTheDaemonCouldNotAnswerIsBuiltAgainOfTheDaemonThereIsThen(t *testing.T) {
	gone := func() error { return connect.NewError(connect.CodeUnavailable, "the daemon ended") }
	for name, last := range map[string]*rounds{"the daemon after answers": {}, "the daemon after cannot answer either": {unwaited: gone()}} {
		t.Run(name, func(t *testing.T) {
			replaced := &rounds{unwaited: gone()}
			w := watches(t, []*rounds{replaced, last}, "build", "--watch")
			w.until(t, "the build of the daemon there is after the one replaced", func() bool { return last.asked() == 1 })
			if last.unwaited != nil {
				w.ends(t, "no wait answered twice running", ExitUnanswered)
				return
			}
			w.stays(t, "with nothing written, the build of the daemon there is then", func() bool { return replaced.asked() == 1 && last.asked() == 1 })
			if printed := w.printed.String(); !strings.Contains(printed, "the build daemon did not wait for a change (") {
				t.Errorf("it printed\n%s\nwant that the daemon did not wait", printed)
			}
		})
	}
}

// A file a plugin reads by name beneath a directory whose name begins
// with a dot, which no listing takes (.cargo/config.toml), is what a
// round read too: written, and nothing else, it is built again, and
// named. Control: a round that did not read it is not built again for
// it.
func TestAWatchBuildsAgainWhenOnlyAFileReadByNameBeneathADotDirectoryChanges(t *testing.T) {
	for name, tc := range map[string]struct {
		reads  []string
		rounds int
	}{
		"read by name":        {[]string{".cargo/config.toml"}, 2},
		"the control, unread": {nil, 1},
	} {
		t.Run(name, func(t *testing.T) {
			d := &rounds{reads: tc.reads}
			w := watches(t, []*rounds{d}, "test", "--watch")
			w.until(t, "the first build", func() bool { return d.asked() == 1 })
			w.stays(t, "with nothing written, one build", func() bool { return d.asked() == 1 })
			w.write(t, ".cargo/config.toml", "[build]\njobs = 1\n")
			w.until(t, fmt.Sprintf("%d builds", tc.rounds), func() bool { return d.asked() == tc.rounds })
			w.stays(t, fmt.Sprintf("after the write, %d builds", tc.rounds), func() bool { return d.asked() == tc.rounds })
			if said := strings.Contains(w.printed.String(), ".cargo/config.toml changed: building again\n"); said != (tc.rounds == 2) {
				t.Errorf("it printed\n%s\nwant the file named where it was built again for it", w.printed)
			}
		})
	}
}

// --watch is build's and test's, and goes with nothing that makes no
// sense of it: with --fix, a build would write the files it is built
// again for; a command that reads the builds there are (build list)
// takes no --watch. Each is refused as a command line written wrongly,
// before a daemon is asked for. The control is
// TestAWatchBuildsAgainWhenAFileChangesAndNotOtherwise.
func TestAWatchIsRefusedWhereItMakesNoSense(t *testing.T) {
	asked := 0
	found := builder
	t.Cleanup(func() { builder = found })
	builder = func(context.Context, string, bool) (machine.Builder, error) {
		asked++
		return machine.Builder{}, fmt.Errorf("a daemon was asked for")
	}
	for _, args := range [][]string{{"build", "--watch", "--fix"}, {"build", "list", "--watch"}, {"build", "--watch", "cancel", "build_1"}, {"run", "--watch", "greet"}, {"query", "--watch"}} {
		_, code, err := runAt(t, context.Background(), "", args...)
		if code != ExitUsage || err == nil || !strings.Contains(err.Error(), "--watch") || asked != 0 {
			t.Errorf("%v ended %d (%v), after %d daemons were asked for; want it refused as written wrongly, by the flag's name", args, code, err, asked)
		}
	}
}

package build

// What a daemon holds of its runs, through the API.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
)

// Nothing holds a directory of runs to one daemon. A second
// daemon given the directory of one that is alive takes its run for one
// "its daemon died under", ends it in the first's record, and the run
// the first then finishes well is on record as failed for every daemon
// after. The control gives the second daemon a directory of its own.
func TestARunIsEndedOnlyByTheDaemonThatRunsIt(t *testing.T) {
	for name, shared := range map[string]bool{"control: a directory each": false, "one directory for both": true} {
		t.Run(name, func(t *testing.T) {
			workspace, kept := oneModule(t), t.TempDir()
			gate := make(chan struct{})
			first := serveAs(t, configuration{Workspace: workspace, Mounted: true, Executor: scripted{gate: gate}, Builds: kept})
			id := create(t, first.runs).GetId()
			// The run is under way and its first events are in its record.
			time.Sleep(time.Second)

			other := kept
			if !shared {
				other = t.TempDir()
			}
			// A directory is one daemon's at a time: a second daemon given
			// the first's does not start, and one given its own does.
			second, err := newDaemon(configuration{Workspace: workspace, Mounted: true, Executor: scripted{}, Builds: other})
			if shared != (err != nil) {
				t.Fatalf("a second daemon: %v; want it refused where the directory is the first's, and only there", err)
			}
			if second != nil {
				defer second.Close()
			}

			close(gate)
			ended := wait(t, first.runs, id)
			if ended.GetExitCode().GetName() != "SUCCESS" {
				t.Fatalf("the daemon that ran it says it ended %s (%s)", ended.GetExitCode().GetName(), ended.GetFailure())
			}
			first.daemon.Close()

			after := serveAs(t, configuration{Workspace: workspace, Mounted: true, Executor: scripted{}, Builds: kept})
			said, err := after.runs.DescribeBuild(context.Background(), &buildv1.DescribeBuildRequest{BuildId: id})
			if err != nil {
				t.Fatalf("the daemon after has no such run: %v", err)
			}
			if said.GetBuild().GetExitCode().GetName() != "SUCCESS" {
				t.Errorf("the daemon after says the run ended %s (%s); the daemon that ran it said SUCCESS", said.GetBuild().GetExitCode().GetName(), said.GetBuild().GetFailure())
			}
		})
	}
}

// A run is let go as soon as twelve newer ones have ended,
// whether or not whoever made it has yet asked how it ended. With more
// than twelve clients building at once on one daemon, a client that
// makes a run and then waits for it is told there is no such run. The
// control is twelve clients. Run with -race: nothing races.
func TestWhoeverMadeARunLearnsHowItEnded(t *testing.T) {
	for name, clientsAtOnce := range map[string]int{"control: twelve clients": 12, "thirty clients": 30} {
		t.Run(name, func(t *testing.T) {
			config := configuration{Workspace: oneModule(t), Mounted: true, Executor: printing(), Builds: t.TempDir()}
			c := serveAs(t, config)
			var all sync.WaitGroup
			var mu sync.Mutex
			lost := map[string]error{}
			for range clientsAtOnce {
				all.Go(func() {
					created, err := c.runs.CreateBuild(context.Background(), &buildv1.CreateBuildRequest{})
					if err != nil {
						t.Error(err)
						return
					}
					id := created.GetBuild().GetId()
					// What the command line does next: follow the run to its end.
					if _, err := c.runs.WaitBuild(context.Background(), &buildv1.WaitBuildRequest{BuildId: id}); connect.CodeOf(err) == connect.CodeNotFound {
						mu.Lock()
						lost[id] = err
						mu.Unlock()
					}
				})
			}
			all.Wait()
			if len(lost) > 0 {
				t.Errorf("%d of %d clients made a run, waited for it, and were told there is no such run", len(lost), clientsAtOnce)
			}
			// What is kept afterwards is whole, here and for the next daemon.
			check := func(c clients) {
				t.Helper()
				listed := listedRuns(t, c)
				if len(listed) != clientsAtOnce {
					t.Errorf("the daemon lists %d runs, want the %d that have only just ended", len(listed), clientsAtOnce)
				}
				for _, run := range listed {
					if run.GetExitCode().GetName() != "SUCCESS" {
						t.Errorf("%s ended %s (%s)", run.GetId(), run.GetExitCode().GetName(), run.GetFailure())
					}
					printed, err := logsOf(c.runs, run.GetId(), "//hello:go.lint")
					if err != nil || len(printed) != 3 {
						t.Errorf("the logs of %s, a run that is listed: %d lines, %v", run.GetId(), len(printed), err)
					}
				}
				if on := runsIn(t, config.Builds); on != clientsAtOnce {
					t.Errorf("the directory holds %d runs, want %d", on, clientsAtOnce)
				}
			}
			check(c)
			c.daemon.Close()
			check(serveAs(t, config))
		})
	}
}

// onePassed waits until a run says one of its actions passed, for at most
// half a minute.
func onePassed(t *testing.T, c clients, id string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		described, err := c.runs.DescribeBuild(context.Background(), &buildv1.DescribeBuildRequest{BuildId: id})
		if err != nil {
			t.Fatal(err)
		}
		if described.GetBuild().GetCounts().GetPassed() == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the run got no further than %v", described.GetBuild())
		}
		time.Sleep(time.Millisecond)
	}
}

// listedRuns are the runs a daemon lists.
func listedRuns(t *testing.T, c clients) []*buildv1.Build {
	t.Helper()
	listed, err := c.runs.ListBuilds(context.Background(), &buildv1.ListBuildsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return listed.GetBuilds()
}

// A daemon closed while a run's events are still in the buffer: the next
// daemon says of the run what the closed one said.
func TestADaemonClosedAtOnceKeepsEveryEvent(t *testing.T) {
	workspace, kept := oneModule(t), t.TempDir()
	c := serveAs(t, configuration{Workspace: workspace, Mounted: true, Executor: halting{}, Builds: kept})
	id := create(t, c.runs).GetId()
	onePassed(t, c, id)
	c.daemon.Close()
	want := tellOf(t, c, id)
	if want.run.GetState() != buildv1.Build_STATE_CANCELLED {
		t.Fatalf("the run is %s", want.run.GetState())
	}

	same(t, tellOf(t, serveAs(t, configuration{Workspace: workspace, Mounted: true, Executor: scripted{}, Builds: kept}), id), want)
}

// What an action printed is kept whole and sent in parts, so that it is
// read back whole however much it is: a message of the API holds four
// mebibytes (connect's default, which client takes), and an
// action may print more.
func TestAnActionThatPrintsALotIsReadBackWhole(t *testing.T) {
	for name, kibibytes := range map[string]int{"one mebibyte": 1 << 10, "sixteen mebibytes": 16 << 10} {
		t.Run(name, func(t *testing.T) {
			line := bytes.Repeat([]byte("x"), 1023)
			var printed []byte
			for range kibibytes {
				printed = append(append(printed, line...), '\n')
			}
			printed = append(printed, "the last line has no end"...)
			config := configuration{Workspace: oneModule(t), Mounted: true, Builds: t.TempDir(), Executor: answering{as: func(r *exec.Result) { r.Stdout = printed }}}
			c := serveAs(t, config)
			id := wait(t, c.runs, create(t, c.runs).GetId()).GetId()
			check := func(c clients) {
				t.Helper()
				got, err := logsOf(c.runs, id, "//hello:go.lint")
				if err != nil {
					t.Errorf("what the lint printed: %v", err)
					return
				}
				if n := len(got); n != kibibytes+1 || got[n-1].GetText() != "the last line has no end" {
					t.Errorf("%d lines; want %d, and the last one whole", n, kibibytes+1)
					return
				}
				for i, each := range got[:kibibytes] {
					if each.GetText() != string(line) || each.GetStream().String() != "LOG_STREAM_STDOUT" {
						t.Fatalf("line %d is %d bytes to %v, want the %d that were printed to standard output", i, len(each.GetText()), each.GetStream(), len(line))
					}
				}
			}
			check(c)
			on, _ := os.ReadDir(filepath.Join(config.Builds, id, "printed"))
			if len(on) != 1 {
				t.Errorf("four actions printed the same and it is kept %d times", len(on))
			}
			c.daemon.Close()
			check(serveAs(t, config))
		})
	}
}

// A directory of runs that cannot be written. (Exploration.)
func TestADirectoryThatCannotBeWritten(t *testing.T) {
	kept := t.TempDir()
	c := serveAs(t, configuration{Workspace: oneModule(t), Mounted: true, Executor: scripted{}, Builds: kept})
	if err := os.Chmod(kept, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(kept, 0o700)
	_, err := c.runs.CreateBuild(context.Background(), &buildv1.CreateBuildRequest{})
	if err == nil {
		t.Fatal("a run was made with nowhere to keep it")
	}
	t.Logf("a run asked of a daemon that cannot keep it: %v", err)
	listed, _ := c.runs.ListBuilds(context.Background(), &buildv1.ListBuildsRequest{})
	if len(listed.GetBuilds()) != 0 {
		t.Errorf("the daemon lists %d runs", len(listed.GetBuilds()))
	}
}

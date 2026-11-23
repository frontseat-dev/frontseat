package exec_test

import (
	"context"
	"fmt"
	"runtime"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
)

// loud is a grid whose every action prints a lot.
type loud struct{ prints int }

func (l loud) Key(_ context.Context, action exec.Action) (string, error) { return action.Label, nil }

func (l loud) Execute(context.Context, exec.Action) (exec.Result, error) {
	return exec.Result{Stdout: make([]byte, l.prints)}, nil
}

// quiet is the same grid, which cannot say what an action is: nothing
// of what it answers is kept.
type quiet struct{ loud }

func (q quiet) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	return q.loud.Execute(ctx, action)
}

func heap() uint64 {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// what an action printed is written to the run's record, by its
// digest, and the daemon needs none of it afterwards. What it keeps of
// an answer is held to a bound here: a megabyte for each of fifty
// actions that each printed eight.
func TestWhatAnActionPrintedIsNotHeldByTheDaemon(t *testing.T) {
	const actions, prints = 50, 8 << 20
	for name, grid := range map[string]exec.Executor{"the control, a grid whose answers are not kept": struct{ exec.Executor }{quiet{loud{prints}}}, "a grid whose answers are kept": loud{prints}} {
		t.Run(name, func(t *testing.T) {
			kept := &exec.Remembering{Executor: grid}
			before := heap()
			for i := range actions {
				if _, err := kept.Execute(context.Background(), exec.Action{Label: fmt.Sprint("//m", i, ":go.test")}); err != nil {
					t.Fatal(err)
				}
			}
			held := int64(heap()) - int64(before)
			t.Logf("after %d actions that printed %d MiB each, the daemon holds %d MiB more", actions, prints>>20, held>>20)
			if held > actions<<20 {
				t.Errorf("the daemon holds %d MiB for %d answers; want no more than a megabyte each", held>>20, actions)
			}
			runtime.KeepAlive(kept)
		})
	}
}

// moody is a grid that sometimes says it has let something go.
type moody struct{}

func (moody) Key(_ context.Context, action exec.Action) (string, error) { return action.Label, nil }

func (moody) Execute(_ context.Context, action exec.Action) (exec.Result, error) {
	if len(action.Arguments) > 0 {
		return exec.Result{}, fmt.Errorf("%s: %w", action.Label, exec.ErrNotKept)
	}
	return exec.Result{Stdout: []byte(action.Label), Outputs: map[string]exec.Held{"out": {Digest: exec.Digest{Hash: action.Label}}}}, nil
}

// runs that begin, actions asked about and a grid that lets go,
// all at once: each answer is the action's own, and the race detector
// finds nothing.
func TestRememberingAtOnce(t *testing.T) {
	kept := &exec.Remembering{Executor: moody{}}
	done := make(chan struct{})
	for worker := range 16 {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := range 2000 {
				label := fmt.Sprint("//m", (worker+i)%50)
				switch {
				case i%97 == 0:
					kept.Begin()
				case i%211 == 0:
					_, _ = kept.Execute(context.Background(), exec.Action{Label: "gone", Arguments: []string{"x"}})
				default:
					result, err := kept.Execute(context.Background(), exec.Action{Label: label})
					if err != nil || string(result.Stdout) != label || result.Outputs["out"].Digest.Hash != label {
						t.Errorf("%s was answered with %q, %v, err %v", label, result.Stdout, result.Outputs, err)
						return
					}
				}
			}
		}()
	}
	for range 16 {
		<-done
	}
}

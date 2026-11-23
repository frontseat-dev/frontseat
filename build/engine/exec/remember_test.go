package exec_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
)

// counting is an executor that knows an action by its label and its
// arguments, counts what it is asked to run, and answers as a test
// says.
type counting struct {
	ran  map[string]int
	exit map[string]int
	err  map[string]error
}

func (c *counting) Key(_ context.Context, action exec.Action) (string, error) {
	return fmt.Sprint(action.Label, action.Arguments), nil
}

func (c *counting) Execute(_ context.Context, action exec.Action) (exec.Result, error) {
	if c.ran == nil {
		c.ran = map[string]int{}
	}
	c.ran[action.Label]++
	if err := c.err[action.Label]; err != nil {
		return exec.Result{}, err
	}
	return exec.Result{ExitCode: c.exit[action.Label], Stdout: []byte("ran " + action.Label), Outputs: map[string]exec.Held{"out": {Digest: exec.Digest{Hash: action.Label, Size: 1}}}}, nil
}

func asked(t *testing.T, e exec.Executor, label string, arguments ...string) exec.Result {
	t.Helper()
	result, err := e.Execute(context.Background(), exec.Action{Label: label, Arguments: arguments})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// What the grid answered for an action is what it answers for the same
// action: the daemon keeps the answer, and does not ask again.
func TestAnActionAnsweredForIsNotAskedAgain(t *testing.T) {
	grid := &counting{}
	kept := &exec.Remembering{Executor: grid}

	first := asked(t, kept, "//a:compile", "go", "build")
	again := asked(t, kept, "//a:compile", "go", "build")

	if grid.ran["//a:compile"] != 1 {
		t.Errorf("the grid was asked %d times; want once", grid.ran["//a:compile"])
	}
	if first.Cached || !again.Cached {
		t.Errorf("the first answer says it was kept: %v, the second: %v; want only the second to", first.Cached, again.Cached)
	}
	if string(again.Stdout) != "ran //a:compile" || again.Outputs["out"].Digest.Hash != "//a:compile" {
		t.Errorf("answered again with %q and %v; want what the grid answered", again.Stdout, again.Outputs)
	}
	// The control: another action is another question.
	asked(t, kept, "//a:compile", "go", "build", "-race")
	if grid.ran["//a:compile"] != 2 {
		t.Errorf("an action with other arguments was asked of the grid %d times in all; want it asked", grid.ran["//a:compile"])
	}
}

// Only what the grid keeps is kept: an action that failed is run again
// by the grid, and so it is asked again; one that could not be run has
// no answer at all.
func TestWhatFailedIsAskedAgain(t *testing.T) {
	grid := &counting{exit: map[string]int{"//a:test": 1}, err: map[string]error{"//a:lint": errors.New("the grid is away")}}
	kept := &exec.Remembering{Executor: grid}

	asked(t, kept, "//a:test")
	asked(t, kept, "//a:test")
	for range 2 {
		if _, err := kept.Execute(context.Background(), exec.Action{Label: "//a:lint"}); err == nil {
			t.Fatal("an action the grid could not run was answered for")
		}
	}

	if grid.ran["//a:test"] != 2 || grid.ran["//a:lint"] != 2 {
		t.Errorf("the grid was asked %v; want each asked both times", grid.ran)
	}
}

// What was answered names content the grid may since have let go. An
// action handed such content says so, and then nothing kept is gone by:
// everything is asked again.
func TestWhatIsKeptIsLetGoWhenTheGridHasLetGo(t *testing.T) {
	grid := &counting{err: map[string]error{"//b:compile": exec.ErrNotKept}}
	kept := &exec.Remembering{Executor: grid}
	asked(t, kept, "//a:compile")

	if _, err := kept.Execute(context.Background(), exec.Action{Label: "//b:compile"}); !errors.Is(err, exec.ErrNotKept) {
		t.Fatalf("err %v; want what the grid said", err)
	}
	asked(t, kept, "//a:compile")

	if grid.ran["//a:compile"] != 2 {
		t.Errorf("the grid was asked %d times; want it asked again once it had let something go", grid.ran["//a:compile"])
	}
}

// plain is an executor that cannot say what an action is.
type plain struct{ ran int }

func (p *plain) Execute(context.Context, exec.Action) (exec.Result, error) {
	p.ran++
	return exec.Result{}, nil
}

// An executor that cannot say whether two actions are the same is asked
// every time.
func TestAnExecutorThatCannotTellActionsApartIsAlwaysAsked(t *testing.T) {
	grid := &plain{}
	kept := &exec.Remembering{Executor: grid}

	asked(t, kept, "//a:compile")
	asked(t, kept, "//a:compile")

	if grid.ran != 2 {
		t.Errorf("asked %d times; want both", grid.ran)
	}
}

package schedule

import (
	"context"
	"sync"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/model"
)

// Outcome is how an action ended, as far as what needs it is concerned.
type Outcome int

const (
	// Unreached is an action the run was over before reaching.
	Unreached Outcome = iota
	// Passed is an action that ran, or was answered, and passed.
	Passed
	// Failed is an action that ran and did not pass.
	Failed
	// Errored is an action that could not be run.
	Errored
	// Skipped is an action that was not run, because one it needs did
	// not pass.
	Skipped
)

// Ending is how an action ended and what it left, each thing by where it
// sits in the workspace.
type Ending struct {
	Outcome Outcome
	Left    map[string]exec.Held
	// Err explains an execution error; an unsuccessful tool exit has an outcome
	// but need not have a transport or staging error.
	Err error
}

// Handed is what one action left for an action that needs it.
type Handed struct {
	// From is the action that left it.
	From model.Label
	// Left is what it left, each thing by where it sits in the workspace.
	Left map[string]exec.Held
}

// Runner runs the actions of a run and hears of the ones that are not
// run.
type Runner interface {
	// Run runs an action with what the actions it needs left, each
	// apart, in the order it needs them, and says how it ended and what
	// it left.
	Run(ctx context.Context, action plan.Action, handed []Handed) Ending
	// Skip is told of an action that is not run because one it needs did
	// not pass: which one, and how that one ended.
	Skip(action plan.Action, need model.Label, ended Outcome)
}

// All runs actions, and returns when each has ended or the context is
// done. An action runs when everything it needs has passed, with what
// those left; actions that do not need each other run at the same time,
// atOnce of them at most. An action that needs one that did not pass is
// not run, and the runner is told which stopped it; so, in turn, is
// whatever needs that one.
//
// The actions hold everything any of them needs: what plan.Of returns.
func All(ctx context.Context, actions []plan.Action, atOnce int, runner Runner) {
	// ended is how an action ended, once done is closed.
	type ended struct {
		done chan struct{}
		Ending
	}
	endings := make(map[string]*ended, len(actions))
	for _, action := range actions {
		endings[action.Label.String()] = &ended{done: make(chan struct{})}
	}
	tasks := traceTasks(ctx, actions)
	free := make(chan struct{}, atOnce)
	var running sync.WaitGroup
	for _, action := range actions {
		running.Add(1)
		go func() {
			defer running.Done()
			mine := endings[action.Label.String()]
			defer close(mine.done)
			ctx := ctx
			if tasks != nil {
				ctx = tasks[action.Label.String()]
				defer func() { endTask(ctx, mine.Ending) }()
			}
			handed := make([]Handed, 0, len(action.Needs))
			for _, need := range action.Needs {
				theirs := endings[need.String()]
				<-theirs.done
				if ctx.Err() != nil || theirs.Outcome == Unreached {
					return
				}
				if theirs.Outcome != Passed {
					mine.Outcome = Skipped
					runner.Skip(action, need, theirs.Outcome)
					return
				}
				if len(theirs.Left) > 0 {
					handed = append(handed, Handed{From: need, Left: theirs.Left})
				}
			}
			select {
			case free <- struct{}{}:
				defer func() { <-free }()
			case <-ctx.Done():
				return
			}
			mine.Ending = runner.Run(ctx, action, handed)
		}()
	}
	running.Wait()
}

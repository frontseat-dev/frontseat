package tools_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools/toolstest"
	"github.com/frontseat-dev/frontseat/testsupport/testpins"
)

// counting is a grid that installs at once and counts how often it is
// asked for the environment of the tools, holding each answer a moment,
// so that actions asking at once overlap, unless it is given up on.
type counting struct {
	installing
	asked, gaveUp atomic.Int32
}

func (c *counting) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	if toolstest.Asks(action) {
		c.asked.Add(1)
		// As a grid does, it gives up on what is given up on.
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			c.gaveUp.Add(1)
			return exec.Result{}, ctx.Err()
		}
	}
	return c.installing.Execute(ctx, action)
}

// Actions of a run that ask at once for the environment of the same
// tools wait for one answer, and the grid is asked once; another tool
// set is asked of its own.
func TestActionsThatAskAtOnceAreGivenOneEnvironment(t *testing.T) {
	grid := &counting{}
	_, p, action := pinnedGo(t, grid)
	var joined sync.WaitGroup
	for range 50 {
		joined.Go(func() {
			if _, err := p.Use(context.Background(), action, "repo", []string{"go"}, false, nil); err != nil {
				t.Error(err)
			}
		})
	}
	joined.Wait()
	if asked := grid.asked.Load(); asked != 1 {
		t.Errorf("50 actions asking at once asked the grid %d times; want once", asked)
	}
	if _, err := p.Use(context.Background(), action, "repo", nil, false, nil); err != nil {
		t.Fatal(err)
	}
	if asked := grid.asked.Load(); asked != 2 {
		t.Errorf("an action of no tool asked the grid %d times in all; want its own once more", asked)
	}
}

// An action that gives up waiting for the environment of its tools does
// not take it from another waiting for the same: the other is answered,
// the grid is asked once, and the one that gave up is told so.
func TestAnActionThatGivesUpWaitingTakesTheEnvironmentFromNoOther(t *testing.T) {
	grid := &counting{}
	root, p, action := pinnedGo(t, grid)
	// The tool is installed first, so that both wait on the environment.
	installedGo(t, p, root)
	impatient, giveUp := context.WithCancel(context.Background())
	var joined sync.WaitGroup
	var gaveUp, waited error
	joined.Go(func() { _, gaveUp = p.Use(impatient, action, "repo", []string{"go"}, false, nil) })
	time.Sleep(10 * time.Millisecond)
	joined.Go(func() { _, waited = p.Use(context.Background(), action, "repo", []string{"go"}, false, nil) })
	time.Sleep(10 * time.Millisecond)
	giveUp()
	joined.Wait()
	if !errors.Is(gaveUp, context.Canceled) {
		t.Errorf("the action that gave up was told %v, want that it was cancelled", gaveUp)
	}
	if waited != nil {
		t.Errorf("the action that waited was told %v", waited)
	}
	if asked := grid.asked.Load(); asked != 1 {
		t.Errorf("the grid was asked %d times, want once", asked)
	}
}

// An action that comes as the environment of its tools is being given
// to those that asked is given it too, and does not ask again: the grid
// is asked once, however the actions of a run fall about the answer.
func TestAnActionThatComesAsTheEnvironmentIsGivenDoesNotAskAgain(t *testing.T) {
	for trial := range 20 {
		grid := &counting{}
		root, p, action := pinnedGo(t, grid)
		installedGo(t, p, root)
		var joined sync.WaitGroup
		for n := range 200 {
			// Spread about the moment the answer comes, 50ms in.
			after := 40*time.Millisecond + time.Duration(n)*100*time.Microsecond
			joined.Go(func() {
				time.Sleep(after)
				if _, err := p.Use(context.Background(), action, "repo", []string{"go"}, false, nil); err != nil {
					t.Error(err)
				}
			})
		}
		go func() { _, _ = p.Use(context.Background(), action, "repo", []string{"go"}, false, nil) }()
		joined.Wait()
		if asked := grid.asked.Load(); asked != 1 {
			t.Fatalf("trial %d: the grid was asked %d times, want once", trial, asked)
		}
	}
}

// Asking stops when none waits for the answer: the grid is told to give
// up, nothing is kept of it, and an action that asks later asks anew and
// is answered.
func TestAskingForAnEnvironmentNoneWaitsForStops(t *testing.T) {
	grid := &counting{}
	root, p, action := pinnedGo(t, grid)
	installedGo(t, p, root)
	impatient, giveUp := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer giveUp()
	if _, err := p.Use(impatient, action, "repo", []string{"go"}, false, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the action that gave up was told %v", err)
	}
	for wait := time.Now(); grid.gaveUp.Load() == 0; time.Sleep(time.Millisecond) {
		if time.Since(wait) > time.Second {
			t.Fatal("the grid was never told to give up: the asking goes on with none waiting for it")
		}
	}
	if _, err := p.Use(context.Background(), action, "repo", []string{"go"}, false, nil); err != nil {
		t.Fatalf("an action that asked later was told %v", err)
	}
	if asked := grid.asked.Load(); asked != 2 {
		t.Errorf("the grid was asked %d times, want once for each", asked)
	}
}

// holding is a grid that answers what mise is asked at once, and holds
// the install of Go until it is let go or given up on, counting the
// installs of Go it was asked for, and failing them while it refuses.
type holding struct {
	installing
	installs, gaveUp atomic.Int32
	refuse           atomic.Bool
	started          chan struct{}
	let              chan struct{}
}

func (h *holding) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	if strings.HasPrefix(action.Label, "mise install go") || strings.HasPrefix(action.Label, "mise install core:go") {
		if h.installs.Add(1) == 1 {
			close(h.started)
		}
		select {
		case <-h.let:
		case <-ctx.Done():
			h.gaveUp.Add(1)
			return exec.Result{}, ctx.Err()
		}
		if h.refuse.Load() {
			return exec.Result{}, errors.New("the grid could not install it")
		}
	}
	return h.installing.Execute(ctx, action)
}

// impatient asks for the Go the repository at root pins on a context it
// returns the cancelling of, and tells on the channel what it was told.
func impatient(p *tools.Provisioner, root string) (context.CancelFunc, <-chan error) {
	ctx, giveUp := context.WithCancel(context.Background())
	told := make(chan error, 1)
	go func() {
		_, _, err := p.Installed(ctx, root, nil, "repo", "go")
		told <- err
	}()
	return giveUp, told
}

// within is how long what is not to wait for an install is given.
const within = 5 * time.Second

// answered fails the test where what was begun has not answered in time,
// or answered with an error.
func answered(t *testing.T, what string, begun func() error) {
	t.Helper()
	told := make(chan error, 1)
	go func() { told <- begun() }()
	select {
	case err := <-told:
		if err != nil {
			t.Errorf("%s was told %v", what, err)
		}
	case <-time.After(within):
		t.Errorf("%s waited for the install of a tool it does not use", what)
	}
}

// While one tool is installed on the grid, which may take minutes,
// another tool of the repository is installed and an action of no tool
// is given its environment: neither waits for an install it does not
// use. Actions that need the tool being installed wait for that one
// install, and are given it once.
func TestAnInstallOfOneToolHoldsUpNoOther(t *testing.T) {
	grid := &holding{started: make(chan struct{}), let: make(chan struct{})}
	root, p, action := pinnedGo(t, grid)
	testpins.Node(t, root+"/repo")
	var installs sync.WaitGroup
	for range 3 {
		installs.Go(func() { installedGo(t, p, root) })
	}
	<-grid.started

	answered(t, "the install of another tool", func() error {
		_, _, err := p.Installed(context.Background(), root, nil, "repo", "node")
		return err
	})
	answered(t, "an action of no tool", func() error {
		_, err := p.Use(context.Background(), action, "repo", nil, false, nil)
		return err
	})

	close(grid.let)
	installs.Wait()
	if n := grid.installs.Load(); n != 1 {
		t.Errorf("three actions that need one tool at once installed it %d times; want once", n)
	}
}

// An action that gives up waiting for the install of its tool is told
// so at once, and takes the install from no other that waits for it.
func TestAnActionThatGivesUpWaitingForAnInstallIsToldSo(t *testing.T) {
	grid := &holding{started: make(chan struct{}), let: make(chan struct{})}
	root, p, _ := pinnedGo(t, grid)
	var waiting sync.WaitGroup
	waiting.Go(func() { installedGo(t, p, root) })
	<-grid.started
	giveUp, gaveUp := impatient(p, root)
	time.Sleep(10 * time.Millisecond)
	giveUp()

	select {
	case err := <-gaveUp:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the action that gave up was told %v, want that it was cancelled", err)
		}
	case <-time.After(within):
		t.Error("the action that gave up went on waiting for the install")
	}
	if n := grid.gaveUp.Load(); n != 0 {
		t.Errorf("the install another waits for was given up on %d times", n)
	}
	close(grid.let)
	waiting.Wait()
	if n := grid.installs.Load(); n != 1 {
		t.Errorf("the tool was installed %d times; want once", n)
	}
}

// An install that failed is not kept: the next action that needs the
// tool has it installed again, and is given it.
func TestAnInstallThatFailedIsNotKept(t *testing.T) {
	grid := &holding{started: make(chan struct{}), let: make(chan struct{})}
	root, p, _ := pinnedGo(t, grid)
	close(grid.let)
	grid.refuse.Store(true)
	if _, _, err := p.Installed(context.Background(), root, nil, "repo", "go"); err == nil {
		t.Fatal("an install the grid failed was given")
	}
	grid.refuse.Store(false)
	installedGo(t, p, root)
	if n := grid.installs.Load(); n != 2 {
		t.Errorf("the tool was installed %d times; want again after it failed", n)
	}
}

// An install none waits for any more is stopped: the grid is told to
// give up, and the next action that needs the tool installs it anew.
func TestAnInstallNoneWaitsForIsStopped(t *testing.T) {
	grid := &holding{started: make(chan struct{}), let: make(chan struct{})}
	root, p, _ := pinnedGo(t, grid)
	giveUp, gaveUp := impatient(p, root)
	<-grid.started
	giveUp()
	if err := <-gaveUp; !errors.Is(err, context.Canceled) {
		t.Fatalf("the action that gave up was told %v", err)
	}
	for begun := time.Now(); grid.gaveUp.Load() == 0; time.Sleep(time.Millisecond) {
		if time.Since(begun) > within {
			t.Fatal("the grid was never told to give up: the install goes on with none waiting for it")
		}
	}
	close(grid.let)
	installedGo(t, p, root)
	if n := grid.installs.Load(); n != 2 {
		t.Errorf("the tool was installed %d times; want anew after it was stopped", n)
	}
}

// A provisioner that is joined has stopped whatever it still had the
// grid doing: an install none waits for any more, told to stop when the
// last gave up, is still inside its call to the grid, and Join returns
// only once that call has. The control is the same install before it is
// let, which had not ended. Joined, the provisioner asks the grid
// nothing more: a tool it was not answered for is refused by name.
func TestAJoinedProvisionerHasStoppedWhatItHadTheGridDo(t *testing.T) {
	grid := toolstest.NewUnwinding()
	root, p, _ := pinnedGo(t, grid)
	giveUp, gaveUp := impatient(p, root)
	grid.Begun(t)
	giveUp()
	if err := <-gaveUp; !errors.Is(err, context.Canceled) {
		t.Fatalf("the action that gave up was told %v", err)
	}
	joined := make(chan struct{})
	go func() { p.Join(); close(joined) }()
	grid.KeepsBack(t, "the provisioner's join", joined)
	// Bounded, so that a provisioner that began the install again fails
	// this by what it says.
	bounded, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	if _, _, err := p.Installed(bounded, root, nil, "repo", "go"); !errors.Is(err, tools.ErrEnded) {
		t.Errorf("a joined provisioner asked for a tool answered %v; want %v", err, tools.ErrEnded)
	}
}

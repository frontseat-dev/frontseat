// Package toolstest answers, for a test whose grid runs nothing, what
// the tools package asks a grid itself: the environment mise gives an
// action's tools (tools.Provisioner's environ), and the directories an
// installation makes. One answer, for every such grid of the tests. It
// holds, too, the call to a grid that goes on after it was told to stop
// (Unwinding), which what joins a run's tools is tested with.
package toolstest

import (
	"context"
	"encoding/json"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
)

// root is the root a grid that runs nothing says it asked mise in.
const root = "/worker/build/0/root"

// Asks reports whether an action asks mise what its tools put in the
// environment.
func Asks(action exec.Action) bool {
	return action.Label == "mise env" || strings.HasPrefix(action.Label, "mise env ")
}

// Environment is what mise would answer it: each tool it holds on the
// path, before the system's directories.
func Environment(action exec.Action) exec.Result {
	return environment(action, nil)
}

// environment is what mise would answer an action, with what said says
// beside the path: a value written "./…" is beneath the root mise was
// asked in.
func environment(action exec.Action, said map[string]string) exec.Result {
	var dirs []string
	for at := range action.Held {
		if strings.Contains(at, "/mise/installs/") {
			dirs = append(dirs, path.Join(root, at, "bin"))
		}
	}
	sort.Strings(dirs)
	answer := map[string]string{"PATH": strings.Join(append(dirs, "/usr/bin", "/bin"), ":")}
	for name, value := range said {
		if rest, beneath := strings.CutPrefix(value, "./"); beneath {
			value = path.Join(root, rest)
		}
		answer[name] = value
	}
	printed, _ := json.Marshal(answer)
	return exec.Result{Stdout: append([]byte(root+"\n"), printed...)}
}

// Configured is a grid that runs nothing, whose mise answers that a
// repository's mise.toml puts Said in the environment beside the tools'
// path: a value written "./…" is beneath the root mise was asked in, as
// mise says a path of the repository's ({{config_root}}). It keeps each
// action that asked mise for an environment, and answers the rest as an
// install is answered.
type Configured struct {
	Said  map[string]string
	mu    sync.Mutex
	asked []exec.Action
}

// Execute implements exec.Executor.
func (c *Configured) Execute(_ context.Context, action exec.Action) (exec.Result, error) {
	if !Asks(action) {
		return Installed(action), nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = append(c.asked, action)
	return environment(action, c.Said), nil
}

// Asked is each action that asked mise for an environment, in order.
func (c *Configured) Asked() []exec.Action {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.asked)
}

// Installed is what a grid answers an action that installs tools: each
// output it names, made, as a directory.
func Installed(action exec.Action) exec.Result {
	left := map[string]exec.Held{}
	for _, output := range action.Outputs {
		left[output] = exec.Held{Digest: exec.Digest{Hash: strings.Repeat("0", 64), Size: 1}, Directory: true}
	}
	return exec.Result{Outputs: left}
}

// Installs reports whether an action installs a tool.
func Installs(action exec.Action) bool {
	return strings.HasPrefix(action.Label, "mise install ")
}

// Unwinding is a call to a grid that is told to stop and goes on a
// while, as one does that is unwinding: it has returned only once the
// test lets it. As a grid, it holds so the install of a tool, and
// answers at once whatever else the tools package asks.
type Unwinding struct {
	begun, let chan struct{}
	once       sync.Once
	ended      atomic.Bool
}

// NewUnwinding is a call that has not begun.
func NewUnwinding() *Unwinding {
	return &Unwinding{begun: make(chan struct{}), let: make(chan struct{})}
}

// Hold is the call: it waits to be told to stop, and from then until it
// is let, and returns why it was stopped.
func (u *Unwinding) Hold(ctx context.Context) error {
	u.once.Do(func() { close(u.begun) })
	<-ctx.Done()
	<-u.let
	u.ended.Store(true)
	return ctx.Err()
}

// Execute implements exec.Executor.
func (u *Unwinding) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	switch {
	case Installs(action):
		return exec.Result{}, u.Hold(ctx)
	case Asks(action):
		return Environment(action), nil
	}
	return Installed(action), nil
}

// Begun returns once the call has begun, failing the test where it
// never does.
func (u *Unwinding) Begun(t testing.TB) {
	t.Helper()
	select {
	case <-u.begun:
	case <-time.After(time.Minute):
		t.Fatal("the grid was never asked what the test holds")
	}
}

// KeepsBack fails the test unless the call, which was told to stop,
// keeps back what must join it: returned, a channel closed when that has
// returned, is not closed while the call is unwinding, and is once the
// call is let and has ended. The control is that the call had not ended
// before it was let.
func (u *Unwinding) KeepsBack(t testing.TB, what string, returned <-chan struct{}) {
	t.Helper()
	select {
	case <-returned:
		t.Fatalf("%s returned while what it had the grid do was still unwinding", what)
	case <-time.After(100 * time.Millisecond):
	}
	if u.ended.Load() {
		t.Fatal("the control: the call to the grid had ended before it was let")
	}
	close(u.let)
	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never returned, though what it had the grid do had stopped", what)
	}
	if !u.ended.Load() {
		t.Errorf("%s returned, and what it had the grid do had not stopped", what)
	}
}

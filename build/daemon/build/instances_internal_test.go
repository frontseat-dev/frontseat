package build

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/reapi"
	"github.com/frontseat-dev/frontseat/build/engine/exec/reapi/reapitest"
)

// A daemon dials its grid as its settings say, its own instance and
// those it reads (Grid.Instance, Grid.Reads): what an instance it reads
// kept answers its action, and the grid runs nothing for it. The control
// is the daemon told to read nothing, for which the grid runs the action
// under its own instance.
func TestADaemonReadsTheInstancesItsSettingsName(t *testing.T) {
	grid := &reapitest.Keeping{}
	address := grid.Serve(t)
	action := func() exec.Action {
		return exec.Action{Label: "//test:compile", Root: t.TempDir(), Inputs: []string{"."}, Arguments: []string{"tool"}}
	}
	ran := func(config Grid) (bool, []string) {
		t.Helper()
		dialed, err := dial(t.Context(), config, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer dialed.Close()
		got, err := dialed.Execute(t.Context(), action())
		if err != nil {
			t.Fatal(err)
		}
		_, ran, _ := grid.Asked()
		return got.Cached, ran
	}
	ran(Grid{Address: address, Instance: "b"})
	if cached, ran := ran(Grid{Address: address, Instance: "p", Reads: []string{"b"}}); !cached || !slices.Equal(ran, []string{"b"}) {
		t.Errorf("a daemon of p told to read b, where b kept the action: cached %v, the grid ran it for %v; want what b kept, and nothing run for p", cached, ran)
	}
	if cached, ran := ran(Grid{Address: address, Instance: "p"}); cached || !slices.Equal(ran, []string{"b", "p"}) {
		t.Errorf("the control: a daemon of p told to read nothing: cached %v, the grid ran it for %v; want it run for p", cached, ran)
	}
}

// A daemon told to read an instance its grid does not serve it is
// refused when it dials, at once: the grid has answered, and would
// answer the same again (reapi.Unread). The control is an instance read
// the grid answers PermissionDenied, which Buildbarn answers too for an
// authorizer it cannot reach or a token run out: the daemon dials, at
// once, and is neither refused nor held up retrying.
func TestADaemonToldToReadAnInstanceItsGridDoesNotServeIsRefusedAtOnce(t *testing.T) {
	for code, refused := range map[codes.Code]bool{codes.InvalidArgument: true, codes.PermissionDenied: false} {
		grid := &reapitest.Keeping{}
		address := grid.Serve(t)
		grid.Refuse("x", code)
		ctx, stop := context.WithTimeout(t.Context(), 20*time.Second)
		began := time.Now()
		dialed, err := dial(ctx, Grid{Address: address, Instance: "p", Reads: []string{"x"}}, nil)
		stop()
		if err == nil {
			dialed.Close()
		}
		if reapi.Unread(err) != refused || (err != nil) != refused || time.Since(began) > 10*time.Second {
			t.Errorf("an instance read answered %s, after %s: %v; want refused %v, at once", code, time.Since(began), err, refused)
		}
	}
}

// What a daemon is told of an instance it reads that the grid refuses is
// said in the daemon's log, where it says what a person should know
// (Config.Log), by its entry of grid.reads. The control is an instance
// read that kept nothing, which is said nowhere.
func TestADaemonSaysInItsLogAnInstanceReadTheGridRefuses(t *testing.T) {
	for code, want := range map[codes.Code]bool{codes.PermissionDenied: true, codes.NotFound: false} {
		var said strings.Builder
		grid := &reapitest.Keeping{}
		grid.Refuse("x", code)
		dialed, err := dial(t.Context(), Grid{Address: grid.Serve(t), Instance: "p", Reads: []string{"x"}}, &said)
		if err == nil {
			_, err = dialed.Execute(t.Context(), exec.Action{Label: "//test:compile", Root: t.TempDir(), Inputs: []string{"."}, Arguments: []string{"tool"}})
			dialed.Close()
		}
		if err != nil || strings.Contains(said.String(), "grid.reads' entry 1") != want {
			t.Errorf("an instance read answering %s: %v, and the daemon's log says %q; want the action run, and it said %v", code, err, said.String(), want)
		}
	}
}

package run

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/schedule"
	"github.com/frontseat-dev/frontseat/server/internal/model"
)

// merger is a grid that puts directories together by what it is told,
// and counts how often it is asked.
type merger struct {
	exec.Executor
	asked int
	got   [][]exec.Held
	err   error
}

func (m *merger) Merge(_ context.Context, all []exec.Held) (exec.Held, error) {
	m.asked++
	m.got = append(m.got, all)
	if m.err != nil {
		return exec.Held{}, m.err
	}
	return tree("merged"), nil
}

func tree(hash string) exec.Held {
	return exec.Held{Digest: exec.Digest{Hash: hash, Size: 1}, Directory: true}
}

func from(name string, left map[string]exec.Held) schedule.Handed {
	return schedule.Handed{From: model.Label{Path: "lab/" + name, Plugin: "go", Name: "compile"}, Left: left}
}

func TestWhatSeveralLeftAtOnePlaceIsPutTogetherOnce(t *testing.T) {
	grid := &merger{}
	rs := &Runner{Answers: &exec.Remembering{Executor: grid}}
	handed := []schedule.Handed{
		from("a", map[string]exec.Held{"bin": tree("a"), "a/only": tree("own")}),
		from("b", map[string]exec.Held{"bin": tree("b")}),
		from("c", map[string]exec.Held{"bin": tree("a")}),
		from("d", map[string]exec.Held{"bin": tree("d")}),
	}

	together, clash, err := rs.together(t.Context(), handed)

	if err != nil || clash != "" {
		t.Fatal(clash, err)
	}
	if together["bin"] != tree("merged") || together["a/only"] != tree("own") || len(together) != 2 {
		t.Errorf("handed %v", together)
	}
	if grid.asked != 1 || len(grid.got[0]) != 3 {
		t.Errorf("the grid was asked %d times, %v; want once, for the three that differ", grid.asked, grid.got)
	}
	// Nothing changed: the grid is asked to put nothing together.
	if _, _, err := rs.together(t.Context(), handed); err != nil || grid.asked != 1 {
		t.Errorf("asked again: %d times, %v", grid.asked, err)
	}
	// It may have let go of what it put together.
	rs.Answers.Forget()
	if _, _, err := rs.together(t.Context(), handed); err != nil || grid.asked != 2 {
		t.Errorf("after letting go: asked %d times, %v", grid.asked, err)
	}
}

func TestWhatLeavesTheSameThingAtOnePlaceAsksTheGridNothing(t *testing.T) {
	grid := &merger{}
	rs := &Runner{Answers: &exec.Remembering{Executor: grid}}

	together, clash, err := rs.together(t.Context(), []schedule.Handed{from("a", map[string]exec.Held{"bin": tree("a")}), from("b", map[string]exec.Held{"bin": tree("a")})})

	if err != nil || clash != "" || together["bin"] != tree("a") || grid.asked != 0 {
		t.Errorf("handed %v, %q, %v, asking the grid %d times", together, clash, err, grid.asked)
	}
}

// Of many that left something in one place, the two that differ are the
// two that are named.
func TestAClashNamesTheTwoActionsThatLeftIt(t *testing.T) {
	grid := &merger{err: &exec.Clash{Path: "hello", First: 1, Second: 2}}
	rs := &Runner{Answers: &exec.Remembering{Executor: grid}}

	_, clash, err := rs.together(t.Context(), []schedule.Handed{
		from("more", map[string]exec.Held{"go/bin": tree("more")}),
		from("programs", map[string]exec.Held{"go/bin": tree("programs")}),
		from("same", map[string]exec.Held{"go/bin": tree("same")}),
	})

	if err != nil {
		t.Fatal(err)
	}
	for _, said := range []string{"//lab/programs:go.compile", "//lab/same:go.compile", "go/bin/hello"} {
		if !strings.Contains(clash, said) {
			t.Errorf("%q does not say %s", clash, said)
		}
	}
	if strings.Contains(clash, "more") {
		t.Errorf("%q names what did not leave it", clash)
	}
}

func TestWhatTheGridCouldNotPutTogetherIsAnErrorAndNoClash(t *testing.T) {
	lost := errors.Join(exec.ErrNotKept)
	for name, c := range map[string]struct {
		executor exec.Executor
		want     error
	}{
		"it let something go": {&merger{err: lost}, exec.ErrNotKept},
		"it cannot":           {scriptless{}, exec.ErrCannotMerge},
	} {
		rs := &Runner{Answers: &exec.Remembering{Executor: c.executor}}
		_, clash, err := rs.together(t.Context(), []schedule.Handed{from("a", map[string]exec.Held{"bin": tree("a")}), from("b", map[string]exec.Held{"bin": tree("b")})})
		if !errors.Is(err, c.want) || clash != "" {
			t.Errorf("%s: %q, %v", name, clash, err)
		}
	}
}

// scriptless is a grid that runs nothing and merges nothing.
type scriptless struct{ exec.Executor }

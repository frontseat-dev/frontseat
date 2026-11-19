package reapi_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgrid"
)

// What the grid knows an action by is said without asking the grid, and
// is what the action is run and answered under: the same for the same
// action, and another for an action that differs in anything it is
// made from.
func TestAnActionIsKnownByWhatItIsMadeFrom(t *testing.T) {
	grid := dial(t)
	dir := root(t, map[string]string{"greeting.txt": "hello\n"})
	greeting := filepath.Join(dir, "greeting.txt")
	action := exec.Action{
		Label:       "//test:key",
		Root:        dir,
		Inputs:      []string{"greeting.txt"},
		Files:       said{greeting: "hello\n"},
		Arguments:   []string{"/bin/sh", "-c", "cat greeting.txt"},
		Environment: map[string]string{"A": "1"},
		Platform:    testgrid.Platform(),
	}
	key, err := grid.Key(context.Background(), action)
	if err != nil || key == "" {
		t.Fatalf("key %q, err %v", key, err)
	}

	again, err := grid.Key(context.Background(), action)
	if err != nil || again != key {
		t.Errorf("asked twice: %q and then %q, err %v; want the same", key, again, err)
	}
	done, err := grid.Execute(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	if ran := fmt.Sprintf("%s-%d", done.Action.Hash, done.Action.Size); key != ran {
		t.Errorf("known by %q and run as %q; want the same", key, ran)
	}

	for name, other := range map[string]func(a *exec.Action){
		"a file that holds something else": func(a *exec.Action) { a.Files = said{greeting: "goodbye\n"} },
		"another argument":                 func(a *exec.Action) { a.Arguments = []string{"/bin/sh", "-c", "cat greeting.txt; true"} },
		"another environment":              func(a *exec.Action) { a.Environment = map[string]string{"A": "2"} },
		"another platform":                 func(a *exec.Action) { a.Platform = map[string]string{"OSFamily": "other"} },
		"something more left":              func(a *exec.Action) { a.Outputs = []string{"out"} },
		"something held": func(a *exec.Action) {
			a.Held = map[string]exec.Held{"tool": {Digest: exec.Digest{Hash: done.Action.Hash, Size: 1}}}
		},
		"something written":     func(a *exec.Action) { a.Written = map[string]exec.Written{"note": {Content: []byte("x")}} },
		"somewhere else to run": func(a *exec.Action) { a.WorkingDirectory = "elsewhere" },
	} {
		changed := action
		other(&changed)
		differs, err := grid.Key(context.Background(), changed)
		if err != nil || differs == key {
			t.Errorf("%s: known by %q, err %v; want another than %q", name, differs, err, key)
		}
	}
	// The label is what the action is called, and no part of what it is.
	named := action
	named.Label = "//test:another"
	if same, err := grid.Key(context.Background(), named); err != nil || same != key {
		t.Errorf("under another label: %q, err %v; want %q", same, err, key)
	}
}

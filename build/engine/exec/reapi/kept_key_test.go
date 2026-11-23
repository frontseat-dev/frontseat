package reapi_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/local/testsupport/testgrid"
)

// an action as a run stages it has tools held in several
// directories, files handed from another project and files written for
// it. What it is known by is the same every time it is asked, and
// differs with each of those.
func TestAStagedActionIsKnownByTheSameKeyEveryTime(t *testing.T) {
	grid := dial(t)
	dir := root(t, map[string]string{"repo/module/a.go": "package a\n", "repo/module/sub/b.go": "package b\n", "repo/other/c.go": "package c\n", "repo/mise.toml": "[tools]\n"})
	files := said{}
	for _, name := range []string{"repo/module/a.go", "repo/module/sub/b.go", "repo/other/c.go", "repo/mise.toml"} {
		content, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.Join(dir, filepath.FromSlash(name))] = string(content)
	}
	hash := func(c string) string { return strings.Repeat(c, 64) }
	action := exec.Action{
		Label:  "//repo/module:go.compile",
		Root:   dir,
		Inputs: []string{"repo/module/a.go", "repo/module/sub/b.go", "repo/mise.toml"},
		Files:  files,
		Held: map[string]exec.Held{
			".frontseat/mise/installs/go":  {Digest: exec.Digest{Hash: hash("2"), Size: 10}, Directory: true},
			".frontseat/mise/installs/buf": {Digest: exec.Digest{Hash: hash("3"), Size: 10}, Directory: true},
			".frontseat/home/go/pkg/mod":   {Digest: exec.Digest{Hash: hash("4"), Size: 10}, Directory: true},
			"repo/module/gen/out.bin":      {Digest: exec.Digest{Hash: hash("5"), Size: 10}, Executable: true},
		},
		Copies:           map[string]exec.Copy{"repo/module/.required/other": {From: filepath.Join(dir, "repo", "other"), Files: []string{"c.go"}}},
		Written:          map[string]exec.Written{"repo/module/go.work": {Content: []byte("go 1.27\n")}},
		WorkingDirectory: "repo/module",
		Arguments:        []string{"/bin/sh", "-c", "true"},
		Environment:      map[string]string{"A": "1", "B": "2", "C": "3"},
		Outputs:          []string{"out", "bin/"},
		Platform:         testgrid.Platform(),
	}
	first, err := grid.Key(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	for range 200 {
		again, err := grid.Key(context.Background(), action)
		if err != nil || again != first {
			t.Fatalf("known by %q and then by %q, err %v", first, again, err)
		}
	}
	for name, other := range map[string]func(a *exec.Action){
		"another tool tree": func(a *exec.Action) {
			a.Held = map[string]exec.Held{}
			for at, each := range action.Held {
				a.Held[at] = each
			}
			a.Held[".frontseat/mise/installs/go"] = exec.Held{Digest: exec.Digest{Hash: hash("9"), Size: 10}, Directory: true}
		},
		"a held file that may not be run": func(a *exec.Action) {
			a.Held = map[string]exec.Held{}
			for at, each := range action.Held {
				a.Held[at] = each
			}
			a.Held["repo/module/gen/out.bin"] = exec.Held{Digest: exec.Digest{Hash: hash("5"), Size: 10}}
		},
		"a handed file that holds something else": func(a *exec.Action) {
			held := said{}
			for at, content := range files {
				held[at] = content
			}
			held[filepath.Join(dir, "repo", "other", "c.go")] = "package c // more\n"
			a.Files = held
		},
		"handed somewhere else": func(a *exec.Action) {
			a.Copies = map[string]exec.Copy{"repo/module/.required/elsewhere": action.Copies["repo/module/.required/other"]}
		},
		"written something else": func(a *exec.Action) {
			a.Written = map[string]exec.Written{"repo/module/go.work": {Content: []byte("go 1.28\n")}}
		},
		"written so that it may be run": func(a *exec.Action) {
			a.Written = map[string]exec.Written{"repo/module/go.work": {Content: []byte("go 1.27\n"), Executable: true}}
		},
		"one output fewer": func(a *exec.Action) { a.Outputs = []string{"out"} },
	} {
		changed := action
		other(&changed)
		differs, err := grid.Key(context.Background(), changed)
		if err != nil || differs == first {
			t.Errorf("%s: known by %q, err %v; want another than %q", name, differs, err, first)
		}
	}

	// Nothing is read of a file the run answers for: with every file
	// gone from the disk, the action is known by the same.
	if err := os.RemoveAll(filepath.Join(dir, "repo")); err != nil {
		t.Fatal(err)
	}
	if gone, err := grid.Key(context.Background(), action); err != nil || gone != first {
		t.Errorf("with its files gone from the disk: known by %q, err %v; want %q, nothing being read", gone, err, first)
	}
}

// reapi.Grid.Key says "Nothing is asked of the grid". With the
// connection to the grid closed, an action is known by what it was
// known by; the control is that it cannot be run.
func TestAnActionIsKnownWithoutTheGrid(t *testing.T) {
	grid := dial(t)
	dir := root(t, map[string]string{"greeting.txt": "hello\n"})
	action := exec.Action{
		Label:     "//test:key",
		Root:      dir,
		Inputs:    []string{"greeting.txt"},
		Files:     said{filepath.Join(dir, "greeting.txt"): "hello\n"},
		Arguments: []string{"/bin/sh", "-c", "cat greeting.txt"},
		Platform:  testgrid.Platform(),
	}
	before, err := grid.Key(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	if err := grid.Close(); err != nil {
		t.Fatal(err)
	}
	if after, err := grid.Key(context.Background(), action); err != nil || after != before {
		t.Errorf("with the grid away: known by %q, err %v; want %q", after, err, before)
	}
	if _, err := grid.Execute(context.Background(), action); err == nil {
		t.Error("the control: the action ran on a grid whose connection is closed")
	}
}

// Of directories to put together, what is not a directory is a clash and
// what the grid does not hold is said to be gone, for a run to start over.
func TestAMergeAsksTheGridAndSaysWhatItDoesNotHold(t *testing.T) {
	ctx := context.Background()
	grid := dial(t)
	directory := func(hash string) exec.Held {
		return exec.Held{Digest: exec.Digest{Hash: hash, Size: 3}, Directory: true}
	}
	missing := func(c string) exec.Held { return directory(strings.Repeat(c, 64)) }

	_, err := grid.Merge(ctx, []exec.Held{missing("a"), missing("b")})
	if !errors.Is(err, exec.ErrNotKept) {
		t.Errorf("merging what the grid does not hold: %v", err)
	}
	_, err = grid.Merge(ctx, []exec.Held{missing("a"), {Digest: exec.Digest{Hash: strings.Repeat("c", 64), Size: 3}}})
	var clash *exec.Clash
	if !errors.As(err, &clash) || clash.First != 0 || clash.Second != 1 {
		t.Errorf("a directory and a file: %v", err)
	}
}

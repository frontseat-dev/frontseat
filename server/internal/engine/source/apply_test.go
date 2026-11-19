package source_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
)

// A change is applied over the file as the run read it, and nothing else.
func TestAChangeIsAppliedOverWhatTheRunRead(t *testing.T) {
	tree, run := readInside(t)
	// Replaced, and made executable.
	if err := run.Apply("", source.Change{Path: "inside", Kind: source.Replace, From: hashed("inside"), Content: []byte("new"), Executable: true}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(tree.Root, "inside"))
	if err != nil || info.Mode()&0o100 == 0 {
		t.Fatalf("inside is not executable after the change: %v %v", info.Mode(), err)
	}
	if content, _, _ := run.Read(context.Background(), "", "inside"); string(content) != "new" {
		t.Fatalf("to the run, inside holds %q after the change", content)
	}
	// Changed again from what it was first read as: refused, the file untouched.
	err = run.Apply("", source.Change{Path: "inside", Kind: source.Replace, From: hashed("inside"), Content: []byte("stale")})
	if err == nil || !strings.Contains(err.Error(), "inside") {
		t.Fatalf("a change worked out from stale content was applied: %v", err)
	}
	if content, _ := os.ReadFile(filepath.Join(tree.Root, "inside")); string(content) != "new" {
		t.Fatalf("the stale change wrote %q", content)
	}
}

func TestAFileIsDeletedAndIsThenGoneToTheRun(t *testing.T) {
	tree, run := readInside(t)
	if err := run.Apply("", source.Change{Path: "inside", Kind: source.Delete, From: hashed("inside")}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tree.Root, "inside")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inside is still there: %v", err)
	}
	if _, found, _ := run.Read(context.Background(), "", "inside"); found {
		t.Fatal("inside is still read after its deletion")
	}
	if names, _ := run.Glob(context.Background(), "", []string{"**"}); len(names) != 0 {
		t.Fatalf("inside is still listed after its deletion: %v", names)
	}
	// Deleting what the run never read, or over stale content, is refused.
	if err := run.Apply("", source.Change{Path: "inside", Kind: source.Delete, From: hashed("inside")}); err == nil {
		t.Fatal("a file deleted once was deleted again")
	}
}

func TestAFileIsCreatedWhereThereIsNoneAndRefusedWhereThereIsOne(t *testing.T) {
	tree := repository(t)
	run := taken(t, tree)
	if err := run.Apply("", source.Change{Path: "made/new.sh", Kind: source.Create, Content: []byte("#!/bin/sh\n"), Executable: true}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(tree.Root, "made", "new.sh"))
	if err != nil || info.Mode()&0o100 == 0 {
		t.Fatalf("made/new.sh was not created executable: %v %v", info, err)
	}
	if names, _ := run.Glob(context.Background(), "", []string{"**"}); len(names) != 2 || names[1] != "made/new.sh" {
		t.Fatalf("the created file is not listed to the run: %v", names)
	}
	if content, found, _ := run.Read(context.Background(), "", "made/new.sh"); !found || string(content) != "#!/bin/sh\n" {
		t.Fatalf("the created file is not read as written: %q %v", content, found)
	}
	for _, refused := range []source.Change{
		{Path: "inside", Kind: source.Create, Content: []byte("x")},
		{Path: "made/new.sh", Kind: source.Create, Content: []byte("x")},
		{Path: "other", Kind: source.Create, Content: []byte("x"), From: hashed("x")},
	} {
		if err := run.Apply("", refused); err == nil {
			t.Errorf("a creation over %s was not refused", refused.Path)
		}
	}
}

func TestAChangeOutsideTheRepositoryOrOfWhatItIgnoresIsRefused(t *testing.T) {
	tree := repository(t)
	write(t, filepath.Join(tree.Root, source.IgnoreFile), "ignored.txt\n")
	write(t, filepath.Join(tree.Root, "ignored.txt"), "x")
	run := taken(t, tree)
	for _, change := range []source.Change{
		{Path: "../outside", Kind: source.Create, Content: []byte("x")},
		{Path: "/etc/passwd", Kind: source.Create, Content: []byte("x")},
		{Path: "", Kind: source.Create, Content: []byte("x")},
		{Path: "ignored.txt", Kind: source.Create, Content: []byte("x")},
		{Path: "inside", Content: []byte("x")},
	} {
		if err := run.Apply("", change); err == nil {
			t.Errorf("the change %+v was applied", change)
		}
	}
	if content, _ := os.ReadFile(filepath.Join(filepath.Dir(tree.Root), "outside")); string(content) != "outside" {
		t.Fatalf("the file outside the repository is %q", content)
	}
}

// A link is not deleted for what it leads to, and nothing is created
// through one: the run would know a file by a name it is not at.
func TestALinkIsNeitherDeletedNorCreatedThrough(t *testing.T) {
	tree := repository(t)
	write(t, filepath.Join(tree.Root, "sub", "target"), "target")
	if err := os.Symlink("sub/target", filepath.Join(tree.Root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sub", filepath.Join(tree.Root, "in")); err != nil {
		t.Fatal(err)
	}
	run := taken(t, tree)
	if _, _, err := run.Read(context.Background(), "", "link"); err != nil {
		t.Fatal(err)
	}
	if err := run.Apply("", source.Change{Path: "link", Kind: source.Delete, From: hashed("target")}); err == nil || !strings.Contains(err.Error(), "link") {
		t.Fatalf("a link was deleted for what it leads to: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tree.Root, "sub", "target")); err != nil {
		t.Fatal("the file the link leads to is gone")
	}
	if err := run.Apply("", source.Change{Path: "in/new.txt", Kind: source.Create, Content: []byte("x")}); err == nil || !strings.Contains(err.Error(), "through a link") {
		t.Fatalf("a file was created through a link: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tree.Root, "sub", "new.txt")); err == nil {
		t.Fatal("the file was created where the link leads")
	}
	// The same file is created by its own name.
	if err := run.Apply("", source.Change{Path: "sub/new.txt", Kind: source.Create, Content: []byte("x")}); err != nil {
		t.Fatal(err)
	}
}

package source

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// settled is a repository of one file, which holds "before", and where
// the file is; the keeper, which holds what a file held as soon as it is
// read; and a way to take the repository by it, as a run that begins now
// sees it.
func settled(t *testing.T) (string, *Keeper, func() *Snapshot) {
	t.Helper()
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	k := &Keeper{now: func() time.Time { return time.Now().Add(3 * time.Second) }}
	return file, k, func() *Snapshot {
		s, err := k.Take(t.Context(), Tree{Root: root, Paths: map[string]string{"": "."}})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
}

func TestSnapshotChangeHintsUseAlreadyCollectedMetadata(t *testing.T) {
	file, _, take := settled(t)
	hint := func(s *Snapshot, path string) bool {
		if h, ok := any(s).(interface{ MayHaveChanged(string, string) bool }); ok {
			return h.MayHaveChanged("", path)
		}
		return false
	}
	warm := take()
	if _, found, err := warm.Digest(t.Context(), "", "file"); err != nil || !found {
		t.Fatalf("warm: %v %v", found, err)
	}
	if hint(take(), "file") {
		t.Fatal("unchanged known file hinted dirty")
	}
	if err := os.WriteFile(file, []byte("changed and longer"), 0644); err != nil {
		t.Fatal(err)
	}
	changed := take()
	if !hint(changed, "file") {
		t.Error("changed metadata not hinted dirty")
	}
	// A hint performs no fresh I/O and grants no read authority. The file
	// changing again after Take must still be rejected by Digest.
	if err := os.WriteFile(file, []byte("changed again, after snapshot"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := changed.Digest(t.Context(), "", "file"); err == nil {
		t.Fatal("hint bypassed snapshot freshness")
	}
}

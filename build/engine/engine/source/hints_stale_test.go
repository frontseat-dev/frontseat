package source

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSnapshotHintsNamesLinksAndStaleMetadata(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	file := filepath.Join(root, "a[1]")
	if err := os.WriteFile(file, []byte("inside"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "outside"), []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink("a[1]", link); err != nil {
		t.Fatal(err)
	}
	k := &Keeper{now: func() time.Time { return time.Now().Add(3 * time.Second) }}
	s, err := k.Take(t.Context(), Tree{Root: root, Paths: map[string]string{"": "."}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a[1]", "link"} {
		if _, found, err := s.Digest(t.Context(), "", name); err != nil || !found {
			t.Fatalf("sound control %q: %v %v", name, found, err)
		}
		if s.MayHaveChanged("", name) {
			t.Fatalf("known control %q was not retained", name)
		}
	}
	before := s.Hashed()
	for _, name := range []string{"", ".", "../outside", "/outside", "missing", "\x00"} {
		if !s.MayHaveChanged("", name) {
			t.Fatalf("unknown name %q hinted known", name)
		}
	}
	if !s.MayHaveChanged("unknown-repository", "a[1]") {
		t.Fatal("unknown repository hinted known")
	}
	if s.Hashed() != before {
		t.Fatal("hints hashed file content")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "outside"), link); err != nil {
		t.Fatal(err)
	}
	// It intentionally still sees cached metadata, even for a now-escaping
	// link. The authoritative read must reject that false-clean hint.
	if s.MayHaveChanged("", "link") {
		t.Fatal("hint unexpectedly refreshed filesystem metadata")
	}
	if _, _, err := s.Digest(t.Context(), "", "link"); err == nil {
		t.Fatal("escaping replacement accepted after false-clean hint")
	}
	if err := os.WriteFile(file, []byte("changed after snapshot and longer"), 0644); err != nil {
		t.Fatal(err)
	}
	if s.MayHaveChanged("", "a[1]") {
		t.Fatal("hint unexpectedly performed a fresh stat")
	}
	var changed *Changed
	if _, _, err := s.Digest(t.Context(), "", "a[1]"); !errors.As(err, &changed) {
		t.Fatalf("stale metadata did not refuse authoritative digest: %v", err)
	}
}

func TestSnapshotHintsConcurrentKeeperUpdatesDoNotAuthorizeReuse(t *testing.T) {
	file, k, take := settled(t)
	old := take()
	if _, _, err := old.Digest(t.Context(), "", "file"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("after and longer"), 0644); err != nil {
		t.Fatal(err)
	}
	next := take()
	if !next.MayHaveChanged("", "file") {
		t.Fatal("new metadata control was not dirty")
	}
	want, _, err := next.Digest(t.Context(), "", "file")
	if err != nil {
		t.Fatal(err)
	}
	if next.MayHaveChanged("", "file") {
		t.Fatal("another read did not make metadata known")
	}
	known := next.knows(next.repositories[""], "file").known
	var wait sync.WaitGroup
	wait.Go(func() {
		for range 1000 {
			k.forget(file)
			k.keep(file, known)
		}
	})
	wait.Go(func() {
		for range 1000 {
			_ = old.MayHaveChanged("", "file")
			_ = next.MayHaveChanged("", "file")
		}
	})
	wait.Go(func() {
		for range 1000 {
			got, found, err := next.Digest(t.Context(), "", "file")
			if err != nil || !found || got != want {
				t.Errorf("authoritative current digest changed: found=%v err=%v", found, err)
				return
			}
		}
	})
	wait.Wait()
	var changed *Changed
	if _, _, err := old.Digest(t.Context(), "", "file"); !errors.As(err, &changed) {
		t.Fatalf("old source became reusable through keeper updates: %v", err)
	}
}

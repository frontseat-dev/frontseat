package fix

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/exec"
)

// slowGrid brings a tree when it is let, and says which it is bringing.
type slowGrid struct {
	started chan string
	release chan struct{}
}

func (g *slowGrid) Materialize(_ context.Context, held exec.Held, into string) error {
	g.started <- held.Digest.Hash
	<-g.release
	return os.MkdirAll(filepath.Join(into, "bin"), 0o755)
}

// Two fixes that need different tools bring them at once: neither waits
// for the other. One tree is brought once, whole, and found by the next.
func TestTreesAreBroughtAtOnceAndEachOnce(t *testing.T) {
	grid := &slowGrid{started: make(chan string, 4), release: make(chan struct{})}
	r := Runner{Grid: grid, Kept: t.TempDir()}
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, hash := range []string{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.materialize(context.Background(), exec.Digest{Hash: hash, Size: 1})
			results <- err
		}()
	}
	timeout := time.After(10 * time.Second)
	for range 2 {
		select {
		case <-grid.started:
		case <-timeout:
			t.Fatal("the second tree waited for the first to be brought")
		}
	}
	close(grid.release)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, hash := range []string{a, b} {
		if _, err := os.Stat(filepath.Join(r.Kept, "tools", hash+"-1", "bin")); err != nil {
			t.Errorf("the tree %s is not whole where it was kept: %v", hash[:4], err)
		}
	}
	// Asked again, a tree that is here is not brought again.
	if _, err := r.materialize(context.Background(), exec.Digest{Hash: a, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if len(grid.started) != 0 {
		t.Fatal("a tree that was here was brought again")
	}
}

// A tree a daemon died bringing is not taken for whole: it is brought again,
// and what was left of it is gone.
func TestATreeLeftHalfBroughtIsBroughtAgain(t *testing.T) {
	grid := &slowGrid{started: make(chan string, 1), release: make(chan struct{})}
	close(grid.release)
	r := Runner{Grid: grid, Kept: t.TempDir()}
	c := strings.Repeat("c", 64)
	partial := filepath.Join(r.Kept, "tools", c+"-1.partial")
	if err := os.MkdirAll(filepath.Join(partial, "junk"), 0o755); err != nil {
		t.Fatal(err)
	}
	at, err := r.materialize(context.Background(), exec.Digest{Hash: c, Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(at, "bin")); err != nil {
		t.Errorf("the tree was not brought: %v", err)
	}
	if _, err := os.Stat(filepath.Join(at, "junk")); !os.IsNotExist(err) {
		t.Error("what was left of the half-brought tree is in the whole one")
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Error("the half-brought tree is still beside the whole one")
	}
}

// A tree is named by a digest, and nothing else names a place to keep it.
func TestATreeIsNamedByADigestOnly(t *testing.T) {
	grid := &slowGrid{started: make(chan string, 1), release: make(chan struct{})}
	close(grid.release)
	r := Runner{Grid: grid, Kept: t.TempDir()}
	for _, hash := range []string{"../escape", strings.Repeat("A", 64), strings.Repeat("a", 63), "", strings.Repeat("a", 60) + "/../x"} {
		if _, err := r.materialize(context.Background(), exec.Digest{Hash: hash, Size: 1}); err == nil {
			t.Errorf("%q was taken for a digest", hash)
		}
	}
	if len(grid.started) != 0 {
		t.Fatal("a tree with no digest was brought")
	}
}

package exec

import (
	"context"
	"errors"
	"testing"
	"time"
)

type reviewListCache struct {
	Executor
	asked int
	fail  bool
}

func (l *reviewListCache) List(context.Context, Held) (map[string]Held, error) {
	l.asked++
	if l.fail {
		return nil, ErrNotKept
	}
	return map[string]Held{"a.pb.go": {Digest: Digest{Hash: "file", Size: 1}}}, nil
}
func TestReviewGeneratedListingCacheIsForgottenAndExpires(t *testing.T) {
	grid := &reviewListCache{}
	now := time.Unix(0, 0)
	r := Remembering{Executor: grid, now: func() time.Time { return now }}
	root := Held{Digest: Digest{Hash: "root", Size: 1}, Directory: true}
	ask := func() {
		t.Helper()
		if _, err := r.List(t.Context(), root); err != nil {
			t.Fatal(err)
		}
	}
	ask()
	ask()
	if grid.asked != 1 {
		t.Fatalf("not cached: %d", grid.asked)
	}
	r.Forget()
	ask()
	if grid.asked != 2 {
		t.Fatalf("forget failed: %d", grid.asked)
	}
	now = now.Add(lasts)
	ask()
	if grid.asked != 3 {
		t.Fatalf("expiration failed: %d", grid.asked)
	}
	grid.fail = true
	other := root
	other.Digest.Hash = "missing"
	if _, err := r.List(t.Context(), other); !errors.Is(err, ErrNotKept) {
		t.Fatal(err)
	}
	grid.fail = false
	ask()
	if grid.asked != 5 {
		t.Fatalf("missing-CAS did not clear listings: %d", grid.asked)
	}
}

package build

import (
	"sync"

	"google.golang.org/protobuf/proto"
)

// endedKept is how many builds, runs and releases that have ended a
// daemon remembers of each, the newest; one that has not ended is
// always remembered.
const endedKept = 12

// lasting is a run or a release a daemon made and stays with until it
// has ended: what it is now, and ended, which is closed when it has.
type lasting[M proto.Message] struct {
	mu    sync.Mutex
	state M
	ended chan struct{}
}

// snapshot returns it as it is now.
func (l *lasting[M]) snapshot() M {
	l.mu.Lock()
	defer l.mu.Unlock()
	return proto.Clone(l.state).(M)
}

// change makes of it what a function does, which no snapshot is taken
// during.
func (l *lasting[M]) change(change func(M)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	change(l.state)
}

// over reports whether it has ended.
func (l *lasting[M]) over() bool {
	select {
	case <-l.ended:
		return true
	default:
		return false
	}
}

// remembered returns what a daemon remembers of what it made, oldest
// first: all that has not ended, and of what has ended the newest
// endedKept. It is given what was made in the same order, and keeps it
// in the same place.
func remembered[T interface{ over() bool }](made []T) []T {
	over := 0
	for _, each := range made {
		if each.over() {
			over++
		}
	}
	kept := made[:0]
	for _, each := range made {
		if each.over() && over > endedKept {
			over--
			continue
		}
		kept = append(kept, each)
	}
	return kept
}

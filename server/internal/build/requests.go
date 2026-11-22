package build

import (
	"sync"

	connect "connectrpc.com/connect/v2"

	"github.com/frontseat-dev/frontseat/server/internal/refusal"
)

// requestsKept is how many request ids a daemon remembers of each kind
// of call: a retry comes soon after the call it repeats.
const requestsKept = 4096

// requests remembers which resource each request id made, so that a call
// sent again with the same id, as after a lost answer, is answered with
// what the first made and makes nothing.
type requests struct {
	mu    sync.Mutex
	made  map[string]string
	order []string
}

// once answers a request: with what its id made before, where find still
// finds it, and otherwise with what create makes now. Two calls with one id
// are answered one after the other, so that both are never made.
func once[T any](r *requests, id string, find func(string) (T, bool), create func() (T, string, error)) (T, error) {
	var none T
	if len(id) > refusal.RequestIDMost {
		return none, refuse(connect.CodeInvalidArgument, "REQUEST_ID_INVALID", "a request id is at most 128 bytes")
	}
	if id == "" {
		made, _, err := create()
		return made, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if earlier, ok := r.made[id]; ok {
		if found, ok := find(earlier); ok {
			return found, nil
		}
	}
	made, key, err := create()
	if err != nil {
		return none, err
	}
	if r.made == nil {
		r.made = map[string]string{}
	}
	if _, ok := r.made[id]; !ok {
		r.order = append(r.order, id)
	}
	r.made[id] = key
	if len(r.order) > requestsKept {
		delete(r.made, r.order[0])
		r.order = r.order[1:]
	}
	return made, nil
}

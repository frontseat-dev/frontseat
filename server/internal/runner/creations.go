package runner

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1/sandboxv1alpha1connect"
	"github.com/frontseat-dev/frontseat/server/internal/httpserver"
)

const creationHeader = "Frontseat-Runner-Attempt"

// creations admits Create calls only for attempts registered by this runner.
// Closing an attempt removes admission before joining its already admitted
// HTTP handlers. A delayed request cannot recreate it after cleanup: unknown
// attempts are refused, and identities are never reused. This registry is
// private to the runner; catalog labels cannot register an attempt.
type creations struct {
	next   http.Handler
	mu     sync.Mutex
	active map[string]*httpserver.Requests
}

func (c *creations) begin(id string) (*httpserver.Requests, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id == "" || c.next == nil || c.active[id] != nil {
		return nil, fmt.Errorf("creation attempt cannot be registered")
	}
	if c.active == nil {
		c.active = map[string]*httpserver.Requests{}
	}
	tracked := httpserver.Track(c.next)
	c.active[id] = tracked
	return tracked, nil
}

func (c *creations) close(id string, tracked *httpserver.Requests) <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active[id] == tracked {
		delete(c.active, id)
	}
	return tracked.Close()
}

func (c *creations) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != sandboxv1alpha1connect.SandboxServiceCreateSandboxProcedure {
		c.next.ServeHTTP(w, r)
		return
	}
	c.mu.Lock()
	tracked := c.active[r.Header.Get(creationHeader)]
	c.mu.Unlock()
	if tracked == nil {
		http.Error(w, "creation attempt is not admitted", http.StatusForbidden)
		return
	}
	tracked.ServeHTTP(w, r)
}

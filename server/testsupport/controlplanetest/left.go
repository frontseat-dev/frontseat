package controlplanetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/page"
	"github.com/frontseat-dev/frontseat/server/sandbox"
	sandboxv1 "github.com/frontseat-dev/frontseat/server/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgateway"
)

// EndLeft ends, once a server that kept its sandboxes' records in state
// is stopped, every sandbox it made in the local stack's gateway and
// did not end: they are its own to end, since a sandbox service holds a
// record of no sandbox but those it made, and the gateway's driver ends
// one by the id it made it under alone. A server stopped before it
// ended a workspace's sandbox, its state gone with the test, would
// otherwise leave the sandbox to no one, and the gateway starts every
// sandbox it holds again whenever it starts. It says what it could not
// end, to whoever is there to hear it: a suite's plane is stopped after
// its tests have ended.
func EndLeft(identity, state string) error {
	left, err := sandbox.New(sandbox.Options{PublicURL: "http://127.0.0.1", State: state, Gateway: &sandbox.Gateway{Address: testgateway.Address, Identity: identity, Runtime: "docker"}})
	if err != nil {
		return fmt.Errorf("the sandboxes a stopped server left could not be ended: %w", err)
	}
	defer left.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	listed, err := page.All(func(token string) ([]*sandboxv1.Sandbox, string, error) {
		page, err := left.ListSandboxes(ctx, &sandboxv1.ListSandboxesRequest{PageToken: token})
		return page.GetSandboxes(), page.GetNextPageToken(), err
	})
	if err != nil {
		return fmt.Errorf("the sandboxes a stopped server left could not be listed: %w", err)
	}
	var (
		ending sync.WaitGroup
		mu     sync.Mutex
		failed []error
	)
	for _, each := range listed {
		ending.Go(func() {
			if _, err := left.DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: each.GetId()}); err != nil {
				mu.Lock()
				failed = append(failed, fmt.Errorf("a sandbox a stopped server left, %s, is ending still: %w", each.GetId(), err))
				mu.Unlock()
			}
		})
	}
	ending.Wait()
	return errors.Join(failed...)
}

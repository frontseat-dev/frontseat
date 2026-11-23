package e2e

import (
	"testing"

	"github.com/frontseat-dev/frontseat/workspace/server/controlplanetest"
)

// local serves a control plane over the local stack's OpenShell gateway,
// as frontseat-server does, and returns it. Without a grid the test is
// skipped, before a gateway is asked for.
func local(t *testing.T) controlplanetest.Served {
	t.Helper()
	return controlplanetest.Start(t)
}

// endLeft ends what a frontseat-server of the test's, stopped, left in
// the local stack's gateway: the sandboxes recorded in its state.
func endLeft(t *testing.T, identity, state string) {
	t.Helper()
	if err := controlplanetest.EndLeft(identity, state); err != nil {
		t.Log(err)
	}
}

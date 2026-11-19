package cli_test

import (
	"testing"

	"github.com/frontseat-dev/frontseat/server/testsupport/controlplanetest"
)

// controlPlane serves a control plane over the local stack's OpenShell
// gateway, as frontseat-local does, and returns it. Without a grid the
// test is skipped, before a gateway is asked for.
func controlPlane(t *testing.T) controlplanetest.Served {
	t.Helper()
	return controlplanetest.Start(t)
}

// endLeft ends what a frontseat-local of the test's, stopped, left in
// the local stack's gateway: the sandboxes recorded in its state. One it
// could not end fails the test: a sandbox left behind is no pass.
func endLeft(t *testing.T, identity, state string) {
	t.Helper()
	if err := controlplanetest.EndLeft(identity, state); err != nil {
		t.Error(err)
	}
}

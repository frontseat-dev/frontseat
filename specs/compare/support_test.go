package compare_test

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/frontseat-dev/frontseat/workspace/server/controlplanetest"
)

// servedPlane serves a control plane private to the comparison, over the
// local stack's gateway, and returns its server and the plane, which the
// comparison closes itself once it has ended its workspaces, and what it
// is: how its owner is known among it. Without a
// grid the test is skipped, before a gateway is asked for.
func servedPlane(t *testing.T) (*httptest.Server, controlplanetest.Plane, controlplanetest.Served) {
	t.Helper()
	return controlplanetest.Serving(t, filepath.Join(t.TempDir(), "state"), nil)
}

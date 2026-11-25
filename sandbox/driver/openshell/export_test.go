package openshell

import (
	"testing"

	client "github.com/ai-openshell/sdk-go/openshell/v1"
)

// OverHeld is a driver over a gateway the SDK holds in memory and a
// compute driver of the gateway's, for the tests of this package's that
// serve it as the sandbox service does.
func OverHeld(t *testing.T, gateway client.ClientInterface, compute string) *Driver {
	t.Helper()
	d := overHeld(t, gateway)
	d.config.Compute = compute
	return d
}

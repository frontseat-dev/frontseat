package drivertest_test

import (
	"testing"

	"github.com/frontseat-dev/frontseat/sandbox/driver"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/drivertest"
)

func TestTheMemoryDriverConforms(t *testing.T) {
	drivertest.Conforms(t, drivertest.NewMemory(), driver.Sandbox{ID: "memory", Image: "none", Ports: []int{80}})
}

func TestTheMemoryDriverThatTakesRulesConforms(t *testing.T) {
	drivertest.Conforms(t, drivertest.RuledMemory(), driver.Sandbox{ID: "memory", Image: "none", Ports: []int{80}})
}

func TestAMemorySandboxWhoseEndingLingersConforms(t *testing.T) {
	d := drivertest.NewMemory()
	drivertest.Lingering(t, d, driver.Sandbox{ID: "memory", Image: "none", Ports: []int{80}}, d.Linger)
}

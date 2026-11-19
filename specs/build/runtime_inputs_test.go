package build_test

import (
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

// A test reads a separately built runtime fixture without giving that fixture
// to compilation. Source names beginning with : and = remain literal sources.
func TestRuntimeArtifactsBelongOnlyToTheActionsThatReadThem(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)
	workspace := spec.runtimeInputs()
	value := fmt.Sprintf("this execution: %d\n", time.Now().UnixNano())
	workspace.write("lib/:source.txt", value)
	const compile, test, fixture = "//app:input-fixture.compile", "//app:input-fixture.test", "//fixture:input-fixture.compile"
	// The runtime source does not exist: compilation must not schedule it.
	built := workspace.build(of(compile))
	spec.Expect(built.outcome()).To(Equal("SUCCESS"), built.diagnostics())
	spec.Expect(built.cameFrom(compile)).To(Equal("executed"))
	missing := workspace.build(of(test))
	spec.Expect(missing.outcome()).To(Equal("BUILD_FAILURE"), missing.diagnostics())
	spec.Expect(missing.outcomeOf(fixture)).To(Equal("failed"))
	workspace.write("fixture/=fixture.txt", value)
	passed := workspace.build(of(test))
	spec.Expect(passed.outcome()).To(Equal("SUCCESS"), passed.diagnostics())
	spec.Expect(passed.cameFrom(test)).To(Equal("executed"))
	spec.Expect(passed.cameFrom(compile)).NotTo(Equal("executed"))
	spec.Expect(workspace.build(of(test)).cameFrom(test)).NotTo(Equal("executed"))
	// Changing only the runtime fixture reruns the test and its fixture producer.
	workspace.write("fixture/=fixture.txt", value+"different\n")
	changed := workspace.build(of(test))
	spec.Expect(changed.outcome()).To(Equal("TESTS_FAILED"), changed.diagnostics())
	spec.Expect(changed.cameFrom(test)).To(Equal("executed"))
	spec.Expect(changed.cameFrom(fixture)).To(Equal("executed"))
	spec.Expect(changed.cameFrom(compile)).NotTo(Equal("executed"))
}

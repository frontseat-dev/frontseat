package build_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

// Native tests keep their discovery and sharding while reading a separately
// built fixture, component-local source and a pinned runtime tool.
func TestNativeTestShardsReceiveOnlyTheirComponentsRuntimeInputs(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)
	workspace := spec.nativeRuntimeInputs()
	const compile = "//app:go.compile-tests"
	const fixture = "//fixture:custom.prepare"
	const first = "//app:go.test/cases@1of2"
	const second = "//app:go.test/cases@2of2"
	// Its producer cannot run yet, but compilation does not need the fixture.
	built := workspace.build(of(compile))
	spec.Expect(built.outcome()).To(Equal("SUCCESS"), built.diagnostics())
	spec.Expect(built.actions()).NotTo(ContainElement(fixture))
	missing := workspace.build(of("//app:go.test/cases"))
	spec.Expect(missing.outcome()).To(Equal("BUILD_FAILURE"), missing.diagnostics())
	spec.Expect(missing.outcomeOf(fixture)).To(Equal("failed"))
	value := fmt.Sprintf("fixture for this run: %d", time.Now().UnixNano())
	workspace.write("fixture/source.txt", value)
	passed := workspace.build(of("//app:go.test/cases"))
	spec.Expect(passed.outcome()).To(Equal("SUCCESS"), passed.diagnostics())
	for _, shard := range []string{first, second} {
		spec.Expect(passed.cameFrom(shard)).To(Equal("executed"), shard)
		spec.Expect(passed.needsOf(shard)).To(ContainElement(fixture), shard)
	}
	spec.Expect(passed.cameFrom(compile)).NotTo(Equal("executed"))
	for _, name := range []string{"TestFirst", "TestSecond", "TestThird", "TestFourth"} {
		spec.Expect(strings.Count(passed.printed(first)+passed.printed(second), "ran "+name+"\n")).To(Equal(1), name)
	}
	kept := workspace.build(of("//app:go.test/cases"))
	spec.Expect(kept.outcome()).To(Equal("SUCCESS"), kept.diagnostics())
	for _, shard := range []string{first, second} {
		spec.Expect(kept.cameFrom(shard)).NotTo(Equal("executed"))
	}
	workspace.write("fixture/source.txt", value+" changed")
	changed := workspace.build(of("//app:go.test/cases"))
	spec.Expect(changed.outcome()).To(Equal("SUCCESS"), changed.diagnostics())
	spec.Expect(changed.cameFrom(fixture)).To(Equal("executed"))
	spec.Expect(changed.cameFrom(compile)).NotTo(Equal("executed"))
	for _, shard := range []string{first, second} {
		spec.Expect(changed.cameFrom(shard)).To(Equal("executed"))
	}
}

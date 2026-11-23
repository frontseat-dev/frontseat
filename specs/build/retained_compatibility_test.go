package build_test

import (
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/testsupport/testpins"
	. "github.com/onsi/gomega"
)

// Retained plans preserve npm's installed artifacts and the application's
// prepared service program, while execution still reads changed source.
func TestRetainedRunKeepsArtifactHandoffsAndRebuildsChangedServicePrograms(t *testing.T) {
	spec := newScenario(t)
	collector := spec.observeBuilds()
	w := spec.applicationServiceWorkspace()
	testpins.Node(spec.TB, w.directory)
	spec.addJestSuite(w)
	w.write("frontseat.yaml", "plugins: {go: {}, npm: {}, jest: {}, backstage: {}, container: {}}\n")
	w.write("web/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: web}\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n  consumesApis: [api:default/orders]\n")
	w.write("web/service.test.cjs", `test('the prepared provider answers', async () => {
  const response = await fetch(process.env.ORDERS_URL, {signal: AbortSignal.timeout(5000)});
  expect((await response.text()).trim()).toBe('hello');
  console.log('retained-provider=' + response.headers.get('x-provider-version'));
});
`)
	const label = "//web:jest.test"
	first := w.build(of(label))
	expectRetainedBuildSucceeded(first)
	spec.Expect(first.cameFrom(label)).To(Equal("executed"))
	spec.Expect(first.printed(label)).To(ContainSubstring("retained-provider=one"))
	spec.Expect(first.actions()).To(ContainElement("//web:npm.install"))
	cold := spec.retainedRunStages(collector, first)
	spec.Expect(spec.requiredSpanBool(cold.evaluate, "retained")).To(BeFalse())

	second := w.build(of(label))
	expectRetainedBuildSucceeded(second)
	spec.Expect(second.cameFrom(label)).To(Equal("grid_cache"))
	warm := spec.retainedRunStages(collector, second)
	spec.expectEvaluateStage(warm.evaluate, true, false)
	spec.Expect(spec.requiredSpanBool(warm.plan, "retained")).To(BeTrue())

	program := w.file("orders/main.go")
	spec.Expect(program).To(ContainSubstring(`"X-Provider-Version", "one"`))
	w.write("orders/main.go", strings.Replace(program, `"X-Provider-Version", "one"`, `"X-Provider-Version", "two"`, 1))
	changed := w.build(of(label))
	expectRetainedBuildSucceeded(changed)
	spec.Expect(changed.cameFrom(label)).To(Equal("executed"))
	spec.Expect(changed.cameFrom("//web:npm.install")).To(Equal("grid_cache"))
	spec.Expect(changed.printed(label)).To(ContainSubstring("retained-provider=two"))
	validated := spec.retainedRunStages(collector, changed)
	spec.Expect(spec.requiredSpanBool(validated.evaluate, "retained")).To(BeTrue())
	spec.Expect(spec.requiredSpanBool(validated.plan, "retained")).To(BeTrue())
}

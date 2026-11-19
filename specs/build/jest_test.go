package build_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/testsupport/testpins"

	. "github.com/onsi/gomega"
)

// Jest owns its configured suite, setup, workers and retries. The adapter
// prepares locked npm dependencies and tests offline as one atom.
// Jest test atoms / npm verifies an empty native lock without claiming an installation it does not produce.
func TestNpmVerifiesAnEmptyNativeLockWithoutClaimingAnInstallationItDoesNotProduce(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.jestWorkspace()
	workspace.write("frontseat.yaml", "plugins: {npm: {}, backstage: {}}\n")
	workspace.write("web/package.json", `{"name":"empty-package","version":"1.0.0"}`)
	workspace.write("web/package-lock.json", `{"name":"empty-package","version":"1.0.0","lockfileVersion":3,"packages":{"":{"name":"empty-package","version":"1.0.0"}}}`)
	built := workspace.build(of("//web:npm.install"))
	spec.Expect(built.says()).To(BeEmpty())
	spec.Expect(built.outcome()).To(Equal("SUCCESS"), "%s\n%s", built.diagnostics(), built.printed("//web:npm.install"))
	workspace.write("web/package.json", `{"name":"empty-package","version":"1.0.0","dependencies":{"is-number":"7.0.0"}}`)
	mismatch := workspace.build(of("//web:npm.install"))
	spec.Expect(mismatch.outcome()).To(Equal("BUILD_FAILURE"), mismatch.diagnostics())
	spec.Expect(mismatch.printed("//web:npm.fetch")).To(ContainSubstring("Missing: is-number"))
	spec.Expect(mismatch.outcomeOf("//web:npm.install")).To(Equal("skipped"))
}

// Jest test atoms / npm owns whether optional platform dependencies produce an installation.
func TestNpmOwnsWhetherOptionalPlatformDependenciesProduceAnInstallation(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.jestWorkspace()
	workspace.write("frontseat.yaml", "plugins: {npm: {}, backstage: {}}\n")
	workspace.write("web/package.json", `{"name":"platform-package","version":"1.0.0","optionalDependencies":{"is-number":"7.0.0"}}`)
	lock := `{"name":"platform-package","version":"1.0.0","lockfileVersion":3,"packages":{"":{"name":"platform-package","version":"1.0.0","optionalDependencies":{"is-number":"7.0.0"}},"node_modules/is-number":{"version":"7.0.0","resolved":"https://registry.npmjs.org/is-number/-/is-number-7.0.0.tgz","integrity":"sha512-41Cifkg6e8TylSpdtTpeLVMqvSBEVzTttHvERD741+pnZ8ANv0004MRL43QKPDlK9cGvNp6NZWZUBlbGXYxxng==","optional":true,"os":["frontseat-unsupported"]}}}`
	workspace.write("web/package-lock.json", lock)
	absent := workspace.build(of("//web:npm.install"))
	spec.Expect(absent.says()).To(BeEmpty())
	spec.Expect(absent.outcome()).To(Equal("SUCCESS"), "%s\n%s", absent.diagnostics(), absent.printed("//web:npm.install"))
	// The native control installs the same archive when no OS is excluded.
	workspace.write("web/package-lock.json", strings.Replace(lock, `,"os":["frontseat-unsupported"]`, "", 1))
	present := workspace.build(of("//web:npm.install"))
	spec.Expect(present.outcome()).To(Equal("SUCCESS"), present.diagnostics())
	spec.Expect(present.printed("//web:npm.install")).To(ContainSubstring("added 1 package"))
}

// Jest test atoms / A configured Jest suite keeps native setup and retries, caches, and reports a changed test failure.
func TestAConfiguredJestSuiteKeepsNativeSetupAndRetriesCachesAndReportsAChangedTestFailure(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.jestWorkspace()
	workspace.write("frontseat.yaml", "plugins: {npm: {}, jest: {}, backstage: {}}\n")
	const label = "//web:jest.test"
	first := workspace.build(of(label))
	spec.Expect(first.says()).To(BeEmpty())
	spec.Expect(first.outcome()).To(Equal("SUCCESS"), "%s\n%s\n%s", first.diagnostics(), first.printed("//web:npm.fetch"), first.printed(label))
	spec.Expect(first.outcomeOf("//web:npm.install")).To(Equal("passed"))
	spec.Expect(first.cameFrom(label)).To(Equal("executed"))
	spec.Expect(first.printed(label)).To(ContainSubstring("jest-suite-once"))
	spec.Expect(first.printed(label)).To(ContainSubstring("native-retry-passed"))
	spec.Expect(workspace.build(of(label)).cameFrom(label)).NotTo(Equal("executed"))
	workspace.write("web/value.cjs", "module.exports = 42;\n")
	changed := workspace.build(of(label))
	spec.Expect(changed.cameFrom(label)).To(Equal("executed"))
	spec.Expect(changed.cameFrom("//web:npm.install")).NotTo(Equal("executed"))
	spec.Expect(changed.outcome()).To(Equal("TESTS_FAILED"), changed.diagnostics())
	spec.Expect(changed.printed(label)).To(ContainSubstring("Expected: 41"))
	workspace.write("web/value.cjs", "module.exports = 41;\n")
	workspace.write("web/jest.config.cjs", "module.exports = {testMatch:['**/absent/*.test.cjs']};\n")
	empty := workspace.build(of(label))
	spec.Expect(empty.cameFrom(label)).To(Equal("executed"))
	spec.Expect(empty.outcome()).To(Equal("TESTS_FAILED"), empty.diagnostics())
	spec.Expect(empty.printed(label)).To(ContainSubstring("No tests found"))
}

// Jest test atoms / Jest requires its package manager and npm can prepare a package without a test runner.
func TestJestRequiresItsPackageManagerAndNpmCanPrepareAPackageWithoutATestRunner(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.jestWorkspace()
	workspace.write("web/package.json", `{"name":"a-library","version":"1.0.0","dependencies":{"is-number":"7.0.0"}}`)
	workspace.write("web/package-lock.json", `{"name":"a-library","version":"1.0.0","lockfileVersion":3,"packages":{"":{"name":"a-library","version":"1.0.0","dependencies":{"is-number":"7.0.0"}},"node_modules/is-number":{"version":"7.0.0","resolved":"https://registry.npmjs.org/is-number/-/is-number-7.0.0.tgz","integrity":"sha512-41Cifkg6e8TylSpdtTpeLVMqvSBEVzTttHvERD741+pnZ8ANv0004MRL43QKPDlK9cGvNp6NZWZUBlbGXYxxng=="}}}`)
	workspace.write("frontseat.yaml", "plugins: {npm: {}, backstage: {}}\n")
	prepared := workspace.build(of("//web:npm.install"))
	spec.Expect(prepared.says()).To(BeEmpty())
	spec.Expect(prepared.outcome()).To(Equal("SUCCESS"), "%s\n%s", prepared.diagnostics(), prepared.printed("//web:npm.install"))
	spec.addJestSuite(workspace)
	workspace.write("frontseat.yaml", "plugins: {jest: {}, backstage: {}}\n")
	refused := workspace.build(of("//web:jest.test"))
	spec.Expect(refused.says()).To(ContainSubstring("Jest requires the installed dependencies of this package at its own root"))
	workspace.write("frontseat.yaml", "plugins: {npm: {}, jest: {}, backstage: {}}\n")
	control := workspace.build(of("//web:jest.test"))
	spec.Expect(control.says()).To(BeEmpty())
	spec.Expect(control.outcome()).To(Equal("SUCCESS"), control.diagnostics())
}

// Jest test atoms / A Jest suite consumes an API whose application provider owns its infrastructure.
func TestAJestSuiteConsumesAnAPIWhoseApplicationProviderOwnsItsInfrastructure(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.applicationServiceWorkspace()
	testpins.Node(spec.TB, workspace.directory)
	spec.addJestSuite(workspace)
	workspace.write("frontseat.yaml", "plugins: {go: {}, npm: {}, jest: {}, backstage: {}, container: {}}\n")
	workspace.write("web/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: web}\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n  consumesApis: [api:default/orders]\n")
	workspace.write("web/service.test.cjs", `const net = require('node:net');
test('only the consumed API is reachable', async () => {
  expect(process.env.GREETER_ADDR).toBeUndefined();
  expect(process.env.ADMIN_URL).toBeUndefined();
  expect(process.env.ORDERS_URL).toBeDefined();
  const response = await fetch(process.env.ORDERS_URL, {signal: AbortSignal.timeout(5000)});
  expect((await response.text()).trim()).toBe('hello');
  console.log('jest-api-provider=' + response.headers.get('x-provider-version'));
  for (const port of [7000, 9000]) {
    await new Promise((resolve, reject) => {
      const socket = net.connect(port, '127.0.0.1');
      socket.on('connect', () => {socket.destroy(); reject(new Error('ungranted endpoint answered'));});
      socket.on('error', error => {socket.destroy(); error.code === 'ECONNREFUSED' ? resolve() : reject(error);});
      socket.setTimeout(2000, () => {socket.destroy(); reject(new Error('endpoint refusal timed out'));});
    });
  }
});
`)
	const label = "//web:jest.test"
	first := workspace.build(of(label))
	spec.Expect(first.says()).To(BeEmpty())
	spec.Expect(first.outcomeOf(label)).To(Equal("passed"), "%s\n%s", first.saysOf(label), first.printed(label))
	spec.Expect(first.cameFrom(label)).To(Equal("executed"))
	spec.Expect(first.printed(label)).To(ContainSubstring("jest-api-provider=one"))
	spec.Expect(workspace.build(of(label)).cameFrom(label)).NotTo(Equal("executed"))
	// Native Jest configuration remains input to a service-backed atom too.
	workspace.write("web/jest.config.cjs", "module.exports = {testMatch:['**/service.test.cjs'], maxWorkers:1};\n")
	changed := workspace.build(of(label))
	spec.Expect(changed.outcomeOf(label)).To(Equal("passed"), "%s\n%s", changed.saysOf(label), changed.printed(label))
	spec.Expect(changed.cameFrom(label)).To(Equal("executed"))
}

// Jest test atoms / npm refuses a registry archive whose bytes differ from the committed lock.
func TestNpmRefusesARegistryArchiveWhoseBytesDifferFromTheCommittedLock(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.jestWorkspace()
	const label = "//web:npm.fetch"
	control := workspace.build(of(label))
	spec.Expect(control.says()).To(BeEmpty())
	spec.Expect(control.outcomeOf(label)).To(Equal("passed"), "%s\n%s", control.saysOf(label), control.printed(label))
	raw, err := os.ReadFile(filepath.Join(workspace.directory, "web/package-lock.json"))
	spec.Expect(err).NotTo(HaveOccurred())
	var lock map[string]any
	spec.Expect(json.Unmarshal(raw, &lock)).To(Succeed())
	packages := lock["packages"].(map[string]any)
	packages["node_modules/jest"].(map[string]any)["integrity"] = "sha512-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="
	changed, err := json.Marshal(lock)
	spec.Expect(err).NotTo(HaveOccurred())
	workspace.write("web/package-lock.json", string(changed))
	refused := workspace.build(of("//web:jest.test"))
	spec.Expect(refused.cameFrom(label)).To(Equal("executed"))
	spec.Expect(refused.outcomeOf(label)).NotTo(Equal("passed"))
	spec.Expect(refused.printed(label)).To(ContainSubstring("EINTEGRITY"))
	spec.Expect(refused.outcomeOf("//web:jest.test")).To(Equal("skipped"))
}

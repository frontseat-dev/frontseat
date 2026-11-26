package build_test

import (
	"testing"

	. "github.com/onsi/gomega"
)

// Backstage / The CLI builds, lints and tests each package of a
// repository as its role says, from the Yarn install, and builds none
// again where nothing changed.
func TestTheBackstageCLIBuildsLintsAndTestsEachPackageAsItsRoleSays(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	// The archive's backstage-hello, as `backstage-cli new` lays one out:
	// a common library, a frontend plugin and a backend plugin linking
	// it, and a backend packing the backend plugin, with better-sqlite3,
	// whose native module the install builds from source.
	workspace := spec.yarnWorkspace("yarn: {}, backstage: {}", "backstage", "")
	kinds := map[string]string{}
	for _, project := range workspace.projects() {
		kinds[project.GetRef().GetRoot()+" "+project.GetKind()] = project.GetName()
	}
	spec.Expect(kinds).To(Equal(map[string]string{
		" yarn-install":                           "root",
		" backstage-repository":                   "root",
		"plugins/hello backstage-package":         "@hello/common",
		"plugins/hello-react backstage-package":   "@hello/react",
		"plugins/hello-backend backstage-package": "@hello/backend",
		"packages/backend backstage-package":      "backend",
	}))
	labels := []string{
		"//:backstage.compile/types",
		"//plugins/hello:backstage.compile",
		"//plugins/hello-react:backstage.compile",
		"//plugins/hello-backend:backstage.compile",
		"//packages/backend:backstage.compile",
		"//plugins/hello-react:backstage.lint",
	}
	built := workspace.build(of(labels...))
	spec.Expect(built.says()).To(BeEmpty())
	spec.Expect(built.outcome()).To(Equal("SUCCESS"), "%s\n%s", built.diagnostics(), built.printed("//:yarn.install"))
	// The backend packs the plugin as the plugin's own build left it.
	spec.Expect(built.printed("//packages/backend:backstage.compile")).To(ContainSubstring("Moving @hello/backend into dist workspace"))
	spec.Expect(workspace.build(of(labels...)).cameFrom("//packages/backend:backstage.compile")).NotTo(Equal("executed"))
	// Each package's tests, the one that reads the shared contract by a
	// path its Component's catalog file names among them.
	tests := []string{
		"//plugins/hello:backstage.test",
		"//plugins/hello-react:backstage.test",
		"//plugins/hello-backend:backstage.test",
		"//packages/backend:backstage.test",
	}
	tested := workspace.build(of(tests...))
	spec.Expect(tested.outcome()).To(Equal("SUCCESS"), tested.diagnostics())
	for _, label := range tests {
		spec.Expect(tested.outcomeOf(label)).To(Equal("passed"), "%s\n%s", label, tested.printed(label))
	}
	// A change to the library builds it and what links it again, and the
	// backend's test that opens the database it built is run again.
	workspace.write("plugins/hello/src/index.ts", workspace.file("plugins/hello/src/index.ts")+"\nexport const again = true;\n")
	changed := workspace.build(of(labels...))
	spec.Expect(changed.outcome()).To(Equal("SUCCESS"), changed.diagnostics())
	spec.Expect(changed.cameFrom("//plugins/hello:backstage.compile")).To(Equal("executed"))
	spec.Expect(changed.cameFrom("//packages/backend:backstage.compile")).To(Equal("executed"))
}

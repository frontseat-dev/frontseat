package build_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"

	. "github.com/onsi/gomega"
)

// Frontseat builds itself. The workspace is what a clone of this repository
// holds, and nothing is written into it for the spec: its committed
// frontseat.yaml says which plugins build it, its .frontseatignore what
// they must not see, and its catalog files what it is: the domain
// frontseat, its systems, and a component for each area that holds
// projects, every entity in the namespace frontseat.
// Everything frontseat finds here is built through the API on the grid,
// the way any repository is built.
//
// What is built is what the plugins can build today: the Go modules, with
// Buf generating the API's Go and Connect source for the Go SDK and the
// plugin contract's Go for the server, each into the Component that says
// so, the plugin SDK and the plugins as Cargo packages, each plugin to
// wasm, and the catalog,
// whose files are held to what the source says each component depends
// on. Its tests are run by the spec after this one.
// Frontseat builds itself / builds this repository's modules and API from its committed configuration.
func TestBuildsThisRepositorySModulesAndAPIFromItsCommittedConfiguration(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.ownCheckout()
	started := time.Now()
	built := workspace.buildFor(30*time.Minute, only("compile", "lint", "generate-sdk", "generate-daemon", "generate-runner", "generate-source", "wasm", "depends-on"))
	spec.Logf("the self-build ended %s after %s\n", built.outcome(), time.Since(started).Round(time.Second))
	spec.Expect(built.outcome()).To(Equal("SUCCESS"), built.diagnostics())
	spec.Expect(built.actions()).To(ContainElements(
		"//cli:go.compile", "//cli:go.lint",
		"//server:go.compile", "//server:go.lint",
		"//local:go.compile",
		"//sdk/go:go.compile", "//sdk/go:go.lint",
		"//api:buf.lint", "//api:buf.generate-sdk", "//api:buf.generate-daemon", "//api:buf.generate-runner", "//api:buf.generate-source",
		"//sdk/plugin:cargo.compile", "//sdk/plugin:cargo.lint",
		"//plugins/go:cargo.compile", "//plugins/go:cargo.wasm", "//plugins/cargo:cargo.wasm",
		"//plugins/npm:cargo.compile", "//plugins/npm:cargo.wasm",
		"//plugins/jest:cargo.compile", "//plugins/jest:cargo.wasm",
		"//specs:catalog.depends-on", "//cli:catalog.depends-on",
		"//server:catalog.depends-on", "//local:catalog.depends-on",
	))
}

// Frontseat tests itself: what `frontseat test` asks of this repository,
// every test its plugins find, Go's by package and Cargo's by crate, run
// on the grid as actions, each given its program, the repository as one
// tree, the modules its compile is given and the tools its Component's
// catalog file names. A test that needs the local stack itself says so
// and is skipped there, as `mise run check` skips it (its Component's
// catalog file tells it there is none); `mise run test` runs it.
// Frontseat builds itself / runs this repository's own tests on the grid.
func TestRunsThisRepositorysOwnTestsOnTheGrid(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.ownCheckout()
	started := time.Now()
	tested := workspace.buildFor(45*time.Minute, testsOnly())
	spec.Logf("the self-test ended %s after %s\n", tested.outcome(), time.Since(started).Round(time.Second))
	spec.Expect(tested.outcome()).To(Equal("SUCCESS"), tested.diagnostics())
	// Tests that passed, not a build that ended well: a build whose tests
	// were all skipped by the build or idle ends SUCCESS too. (A test
	// program that skips every case of its own passes, to the go command
	// as here: what holds a test's cases to running is its own package.) Each of Go's and Cargo's,
	// of a package that tests itself alone and of one that reads beyond
	// it (its Component's test.inputs), and the repository's rules.
	for _, label := range []string{
		"//sdk/go:go.test/.", "//server:go.test/internal/pluginhost", "//server:go.test/internal/pattern",
		"//server:go.test/internal/confine", "//local:go.test/stack", "//specs:go.test/architecture",
		"//cli:go.test/internal/cmd", "//sdk/plugin/generate:cargo.test", "//plugins/go:cargo.test",
	} {
		spec.Expect(tested.outcomeOf(label)).To(Equal("passed"), label)
	}
	// Every test action passed, and there are as many as were measured:
	// a plugin that found fewer would leave fewer.
	var tests, passed int
	for _, result := range tested.results() {
		if name := result.GetName(); name == "test" || strings.HasPrefix(name, "test/") {
			tests++
			if result.GetOutcome() == buildv1.Outcome_OUTCOME_PASSED {
				passed++
			}
		}
	}
	spec.Expect(passed).To(Equal(tests))
	// Measured 2026-11-18: 91 test actions.
	spec.Expect(tests).To(BeNumerically(">=", 91))
}

// Frontseat builds itself / is the domain its catalog files declare, whose components own every project.
func TestIsTheDomainItsCatalogFilesDeclareWhoseComponentsOwnEveryProject(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.ownCheckout()
	entities := workspace.catalogEntities()
	spec.Expect(entities).To(HaveKey("domain:frontseat/frontseat"))
	systems := []string{"system:frontseat/build", "system:frontseat/plugins", "system:frontseat/workspaces", "system:frontseat/grid", "system:frontseat/clients"}
	for _, system := range systems {
		spec.Expect(entities).To(HaveKey(system))
		spec.Expect(relatedBy(entities[system], "partOf")).To(ConsistOf("domain:frontseat/frontseat"), system)
	}
	// Every entity is in the namespace frontseat, and a component is part
	// of one of the domain's systems or, released by none, of no system:
	// the specs.
	shared := []string{"component:frontseat/specs"}
	var components []string
	for ref, described := range entities {
		spec.Expect(entity.FromProto(described.GetRef()).Namespace).To(Equal("frontseat"), ref)
		if !strings.HasPrefix(ref, "component:") {
			continue
		}
		components = append(components, ref)
		var in []string
		for _, target := range relatedBy(described, "partOf") {
			if strings.HasPrefix(target, "system:") {
				in = append(in, target)
			}
		}
		if slices.Contains(shared, ref) {
			spec.Expect(in).To(BeEmpty(), ref)
			continue
		}
		spec.Expect(in).To(HaveLen(1), ref)
		spec.Expect(systems).To(ContainElement(in[0]), ref)
	}
	spec.Expect(components).To(ContainElements(append(shared, "component:frontseat/api", "component:frontseat/cli", "component:frontseat/server", "component:frontseat/local", "component:frontseat/go-sdk", "component:frontseat/plugin-sdk", "component:frontseat/go-plugin", "component:frontseat/backstage-plugin")))
	// The APIs, the Resources and the Location are entities that own no
	// source, and what is said of the APIs comes out as Backstage's
	// relations, both ways.
	for _, ref := range []string{"api:frontseat/build-api", "api:frontseat/workspace-api", "api:frontseat/sandbox-api", "api:frontseat/credential-api", "api:frontseat/plugin-api", "resource:frontseat/buildbarn", "resource:frontseat/asset-service", "resource:frontseat/artifact-proxy", "resource:frontseat/openshell-gateway", "location:frontseat/frontseat-catalog"} {
		spec.Expect(entities).To(HaveKey(ref))
		spec.Expect(entities[ref].GetRoots()).To(BeEmpty(), ref)
	}
	for _, edge := range [][3]string{
		{"component:frontseat/server", "providesApi", "api:frontseat/build-api"},
		{"api:frontseat/workspace-api", "apiProvidedBy", "component:frontseat/server"},
		{"component:frontseat/server", "consumesApi", "api:frontseat/plugin-api"},
		{"component:frontseat/cli", "consumesApi", "api:frontseat/credential-api"},
		{"api:frontseat/credential-api", "apiProvidedBy", "component:frontseat/server"},
		{"api:frontseat/plugin-api", "apiProvidedBy", "component:frontseat/go-plugin"},
		{"component:frontseat/server", "partOf", "system:frontseat/build"},
		{"component:frontseat/api", "partOf", "system:frontseat/clients"},
		{"resource:frontseat/buildbarn", "partOf", "system:frontseat/grid"},
		{"resource:frontseat/openshell-gateway", "partOf", "system:frontseat/workspaces"},
		{"system:frontseat/build", "partOf", "domain:frontseat/frontseat"},
	} {
		spec.Expect(spec.catalogRelation(entities[edge[0]], edge[1], edge[2])).To(BeTrue(), "%v", edge)
	}
	for _, project := range workspace.projects() {
		named := project.GetRepository() + "/" + project.GetRoot()
		spec.Expect(components).To(ContainElement(workspace.ownerOf(named)), named)
	}
}

// relatedBy names the targets of an entity's relations of one type.
func relatedBy(described *buildv1.Entity, relation string) []string {
	var targets []string
	for _, each := range described.GetRelations() {
		if each.GetType() == relation {
			targets = append(targets, entity.FromProto(each.GetTarget()).String())
		}
	}
	return targets
}

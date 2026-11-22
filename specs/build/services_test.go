package build_test

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"
)

// A test's services are written by hand: a Component's dependsOn names
// the Resources its tests need, and a Resource says how it is started
// in spec.frontseat.service. Each test action of the component runs in a
// runner sandbox of its own, its services beside it on a network that
// holds a loopback and nothing else, and is given what they export.
// A test's services / A component's tests reach the service its Resource starts.
func TestAComponentSTestsReachTheServiceItsResourceStarts(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.greeterWorkspace(greeterComponent(true), greeterResource(greeterService))

	build := workspace.build()

	spec.Expect(build.outcomeOf("//svc:go.test/.")).To(Equal("passed"), build.printed("//svc:go.test/."))
}

// A test's services / How a Resource is started is part of its tests' key: the same is answered again, a change runs them again.
func TestHowAResourceIsStartedIsPartOfItsTestsKeyTheSameIsAnsweredAgainAChangeRunsThemAgain(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.greeterWorkspace(greeterComponent(true), greeterResource(greeterService))
	spec.Expect(workspace.build().outcomeOf("//svc:go.test/.")).To(Equal("passed"))

	again := workspace.build()
	spec.Expect(again.cameFrom("//svc:go.test/.")).NotTo(Equal("executed"))

	workspace.write("catalog-info.yaml", greeterComponent(true)+"---\n"+greeterResource(greeterService+"      env: {CHANGED: \"1\"}\n"))
	changed := workspace.build()
	spec.Expect(changed.outcomeOf("//svc:go.test/.")).To(Equal("passed"))
	spec.Expect(changed.cameFrom("//svc:go.test/.")).To(Equal("executed"))
}

// A test's services / A component that names no Resource has its tests run without it.
func TestAComponentThatNamesNoResourceHasItsTestsRunWithoutIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.greeterWorkspace(greeterComponent(false), greeterResource(greeterService))

	build := workspace.build()

	spec.Expect(build.outcomeOf("//svc:go.test/.")).To(Equal("failed"))
	spec.Expect(build.printed("//svc:go.test/.")).To(ContainSubstring("no GREETER_ADDR"))
}

// A test's services / An export the repository's mise.toml sets too is refused by name, not replaced.
func TestAnExportTheRepositorySMiseTomlSetsTooIsRefusedByNameNotReplaced(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.greeterWorkspace(greeterComponent(true), greeterResource(greeterService))
	workspace.write("mise.toml", workspace.file("mise.toml")+"\n[env]\nGREETER_ADDR = \"the repository's\"\n")

	build := workspace.build()

	spec.Expect(build.outcomeOf("//svc:go.test/.")).To(Equal("error"))
	spec.Expect(build.saysOf("//svc:go.test/.")).To(ContainSubstring("must be given GREETER_ADDR as it is, and mise sets GREETER_ADDR"))
}

// A test's services / An export named as what runs a test sets itself is refused by name.
func TestAnExportNamedAsWhatRunsATestSetsItselfIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	home := strings.Replace(greeterService, "GREETER_ADDR:", "HOME:", 1)
	workspace := spec.greeterWorkspace(greeterComponent(true), greeterResource(home))

	build := workspace.build()

	spec.Expect(build.outcomeOf("//svc:go.test/.")).To(Equal("error"))
	spec.Expect(build.saysOf("//svc:go.test/.")).To(ContainSubstring("must be given HOME as it is, and what runs and confines it sets HOME itself"))
}

// A test's services / An export that is no name the shell can export is refused by name.
func TestAnExportThatIsNoNameTheShellCanExportIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	dashed := strings.Replace(greeterService, "GREETER_ADDR:", "GREETER-ADDR:", 1)
	workspace := spec.greeterWorkspace(greeterComponent(true), greeterResource(dashed))

	spec.Expect(workspace.build().says()).To(ContainSubstring("service.exports: a name the shell can export is letters, digits and underscores, beginning with no digit"))
}

// A test's services / An image named without its registry is refused by name.
func TestAnImageNamedWithoutItsRegistryIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	short := strings.Replace(greeterService, "docker.io/library/busybox", "busybox", 1)
	workspace := spec.greeterWorkspace(greeterComponent(true), greeterResource(short))

	spec.Expect(workspace.build().says()).To(ContainSubstring("does not name its registry"))
}

// A test's services / A key service does not take is refused by name.
func TestAKeyServiceDoesNotTakeIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	privileged := greeterService + "      privileged: true\n"
	workspace := spec.greeterWorkspace(greeterComponent(true), greeterResource(privileged))

	spec.Expect(workspace.build().says()).To(ContainSubstring("service takes no key privileged"))
}

// A test's services / A Resource started as postgres is a database its component's tests reach, with nothing more written.
func TestAResourceStartedAsPostgresIsADatabaseItsComponentSTestsReachWithNothingMoreWritten(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.databaseWorkspace("      as: postgres\n")

	build := workspace.build()

	spec.Expect(build.outcomeOf("//svc:go.test/.")).To(Equal("passed"), build.printed("//svc:go.test/."))
}

// A test's services / What is written beside as is kept: the tests are told what the Resource exports, and not what its plugin would have.
func TestWhatIsWrittenBesideAsIsKeptTheTestsAreToldWhatTheResourceExportsAndNotWhatItsPluginWouldHave(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.databaseWorkspace("      as: postgres\n      exports: {DATABASE: \"{host}:{port.sql}\"}\n")

	build := workspace.build()

	// Its test reaches the database by the name written, and fails
	// if it is told the plugin's names beside it.
	spec.Expect(build.outcomeOf("//svc:go.test/.")).To(Equal("passed"), build.printed("//svc:go.test/."))
}

// A test's services / A port written beside as is the port the database serves on.
func TestAPortWrittenBesideAsIsThePortTheDatabaseServesOn(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.databaseWorkspace("      as: postgres\n      ports: {sql: 6543}\n      exports: {DATABASE: \"{host}:6543\"}\n")

	build := workspace.build()

	spec.Expect(build.outcomeOf("//svc:go.test/.")).To(Equal("passed"), build.printed("//svc:go.test/."))
}

// A test's services / What a plugin cannot make fit what is written is refused where it is written.
func TestWhatAPluginCannotMakeFitWhatIsWrittenIsRefusedWhereItIsWritten(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.databaseWorkspace("      as: postgres\n      environment: {TZ: UTC}\n")

	spec.Expect(workspace.build().says()).To(And(
		ContainSubstring("catalog-info.yaml"),
		ContainSubstring("resource:default/db, started as postgres: the environment written gives PostgreSQL no POSTGRES_PASSWORD"),
	))
}

// A test's services / A service that does not say what it is started as is refused.
func TestAServiceThatDoesNotSayWhatItIsStartedAsIsRefused(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	bare := strings.Replace(greeterService, "      as: container\n", "", 1)
	workspace := spec.greeterWorkspace(greeterComponent(true), greeterResource(bare))

	spec.Expect(workspace.build().says()).To(ContainSubstring("service does not say what it is: write as: container"))
}

// A test's services / A service as something no plugin of the repository starts is refused by name, with what is started.
func TestAServiceAsSomethingNoPluginOfTheRepositoryStartsIsRefusedByNameWithWhatIsStarted(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	unknown := strings.Replace(greeterService, "as: container", "as: mainframe", 1)
	workspace := spec.greeterWorkspace(greeterComponent(true), greeterResource(unknown))

	spec.Expect(workspace.build().says()).To(ContainSubstring("started as mainframe, which no plugin of the repository starts: container, postgres"))
}

// A test's services / Only a Resource is started.
func TestOnlyAResourceIsStarted(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	component := strings.Replace(greeterComponent(true), "  owner: team\n", "  owner: team\n  frontseat:\n    service:\n      as: container\n      image: x\n", 1)
	workspace := spec.greeterWorkspace(component, greeterResource(greeterService))

	spec.Expect(workspace.build().says()).To(ContainSubstring("only a Resource is started"))
}

// A test's services were once said under spec.frontseat.launch: a
// catalog file that still says so is refused by name, saying where it is
// said now, before anything is built. The control: the same written
// under service is started, and the test reaches it.
// A test's services / A Resource that says launch is refused by name; the same said as service is started.
func TestAResourceThatSaysLaunchIsRefusedByNameTheSameSaidAsServiceIsStarted(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	launched := strings.Replace(greeterResource(greeterService), "    service:\n", "    launch:\n", 1)
	refused := spec.greeterWorkspace(greeterComponent(true), launched).build()
	spec.Expect(refused.says()).To(And(
		ContainSubstring("catalog-info.yaml"),
		ContainSubstring("spec.frontseat takes no key launch; it takes actions, generated, platforms, plugins, release, service and test"),
	))
	spec.Expect(refused.actions()).To(BeEmpty())

	started := spec.greeterWorkspace(greeterComponent(true), greeterResource(greeterService)).build()
	spec.Expect(started.outcomeOf("//svc:go.test/.")).To(Equal("passed"), started.printed("//svc:go.test/."))
}

// A test reaches a service where it is told to: each endpoint a service
// says is filled in where the test reaches it, and told to the test in
// FRONTSEAT_ENDPOINTS, by the service's entity, with nothing exported by
// hand. The control: the same service with no endpoint tells the test
// nothing, and the test that reads the variable fails.
// A test's services / A test reaches a service at the endpoint it is told.
func TestATestReachesAServiceAtTheEndpointItIsTold(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	plain := strings.Replace(greeterService, "      exports: {GREETER_ADDR: \"{host}:{port.greet}\"}\n", "", 1)
	told := plain + "      endpoints: {greet: \"{host}:{port.greet}\"}\n"

	workspace := spec.greeterWorkspace(greeterComponent(true), greeterResource(told))
	workspace.write("svc/greeter_test.go", endpointTest)
	build := workspace.build()
	spec.Expect(build.outcomeOf("//svc:go.test/.")).To(Equal("passed"), build.printed("//svc:go.test/."))

	control := spec.greeterWorkspace(greeterComponent(true), greeterResource(plain))
	control.write("svc/greeter_test.go", endpointTest)
	untold := control.build()
	spec.Expect(untold.outcomeOf("//svc:go.test/.")).To(Equal("failed"))
	spec.Expect(untold.printed("//svc:go.test/.")).To(ContainSubstring("no FRONTSEAT_ENDPOINTS"))
}

// endpointTest is greeterTest told where the greeter is by
// FRONTSEAT_ENDPOINTS alone, and by nothing exported by hand.
var endpointTest = strings.NewReplacer(
	"import (\n", "import (\n\t\"encoding/json\"\n",
	`	addr := os.Getenv("GREETER_ADDR")
	if addr == "" {
		t.Fatal("no GREETER_ADDR: the service was not given")
	}
`, `	told := os.Getenv("FRONTSEAT_ENDPOINTS")
	if told == "" {
		t.Fatal("no FRONTSEAT_ENDPOINTS: the test was told no endpoint")
	}
	var endpoints map[string]map[string]string
	if err := json.Unmarshal([]byte(told), &endpoints); err != nil {
		t.Fatalf("FRONTSEAT_ENDPOINTS=%s: %v", told, err)
	}
	addr := endpoints["resource:default/greeter"]["greet"]
	if addr == "" || len(endpoints) != 1 {
		t.Fatalf("FRONTSEAT_ENDPOINTS=%s names no greet endpoint of the greeter alone", told)
	}
`).Replace(greeterTest)

// The service: busybox answering whoever connects with hello.
const greeterService = `      as: container
      image: docker.io/library/busybox:1.37@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec
      arguments: [nc, -lk, -p, "7000", -e, echo, hello]
      ports: {greet: 7000}
      ready: [{port: greet}]
      exports: {GREETER_ADDR: "{host}:{port.greet}"}
`

func greeterComponent(depends bool) string {
	component := `apiVersion: backstage.io/v1alpha1
kind: Component
metadata: {name: svc}
spec:
  type: service
  lifecycle: production
  owner: team
`
	if depends {
		component += "  dependsOn: [resource:greeter]\n"
	}
	return component
}

func greeterResource(service string) string {
	return `apiVersion: backstage.io/v1alpha1
kind: Resource
metadata: {name: greeter}
spec:
  type: service
  owner: team
  frontseat:
    service:
` + service
}

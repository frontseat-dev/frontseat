package build_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

// A module's tests are not one action. The go command's own unit of
// testing is the package, so each package that has tests is tested by an
// action of its own, named "test", a slash and the package's directory in
// the module. Nobody writes the split: the plugin reads which directories
// hold test files.
//
// Which packages have tests on a worker is the go command's to say
// there, by each test file's constraints and the worker's platform: a
// test whose program the go command did not build has nothing to do, is
// not run, and fails nothing.
//
// Each such action is kept by the grid as any action is, and what it
// reads is its own package's test program and its fixtures, and nothing
// of another package. So a change is tested
// where it can matter: in the package changed and in the packages that
// import it, and nowhere else.
//
// A package's test is run as the go command runs it: in the package's
// directory, and held to have failed where the program ends before its
// tests have.
// A Go module's tests, one action for each package / Each package that has tests is tested by an action of its own, named for it.
func TestEachPackageThatHasTestsIsTestedByAnActionOfItsOwnNamedForIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.testedPackages().build(only("test"))

	spec.Expect(build.outcome()).To(Equal("SUCCESS"), build.diagnostics())
	spec.Expect(build.actions()).To(ContainElements(
		"//lab/fixtures:go.test/ledger",
		"//lab/fixtures:go.test/billing",
		"//lab/fixtures:go.test/orders",
	))
	// There is no action that tests the module as a whole beside them.
	spec.Expect(build.actions()).NotTo(ContainElement("//lab/fixtures:go.test"))
}

// A test of what holds no tests ends as Bazel's does: NO_TESTS_FOUND,
// exit code 4, having run nothing, and says so. What is a test is what
// its plugin says, not what its action is named. The control: the same
// workspace's tests, pointed at, are run.
// frontseat test / A test of what holds no tests ends NO_TESTS_FOUND, 4; a test of what holds them runs them.
func TestATestOfWhatHoldsNoTestsEndsNoTestsFoundAndOfWhatHoldsThemRunsThem(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)
	workspace := spec.testedPackages()

	none := workspace.build(of("//lab/fixtures:go.compile"), testsOnly())
	spec.Expect(none.outcome()).To(Equal("NO_TESTS_FOUND"), none.diagnostics())
	spec.Expect(none.run.GetExitCode().GetCode()).To(BeEquivalentTo(4))
	spec.Expect(none.run.GetFailure()).To(ContainSubstring("nothing //lab/fixtures:go.compile points at runs tests"))
	spec.Expect(none.actions()).To(BeEmpty())

	ran := workspace.build(of("fixtures"), testsOnly())
	spec.Expect(ran.outcome()).To(Equal("SUCCESS"), ran.diagnostics())
	spec.Expect(testsOf(ran)).To(ContainElements(
		"//lab/fixtures:go.test/ledger",
		"//lab/fixtures:go.test/billing",
		"//lab/fixtures:go.test/orders",
	))
}

// A Go module's tests, one action for each package / A package whose tests changed is tested again, and no other package is.
func TestAPackageWhoseTestsChangedIsTestedAgainAndNoOtherPackageIs(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.testedPackages()
	workspace.build(only("test")).succeeded()

	// What the test is changed to, no build before this one tested.
	second := workspace.write("lab/fixtures/ledger/ledger_test.go", ledgerTest(time.Now().Format(time.RFC3339Nano))).build(only("test")).succeeded()

	spec.Expect(second.cameFrom("//lab/fixtures:go.test/ledger")).To(Equal("executed"))
	// billing imports ledger, and not ledger's tests.
	spec.Expect(second.cameFrom("//lab/fixtures:go.test/billing")).To(Equal("grid_cache"))
	spec.Expect(second.cameFrom("//lab/fixtures:go.test/orders")).To(Equal("grid_cache"))
}

// A Go module's tests, one action for each package / A package is tested again when a package it imports changed, and one that imports neither is not.
func TestAPackageIsTestedAgainWhenAPackageItImportsChangedAndOneThatImportsNeitherIsNot(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.testedPackages()
	workspace.build(only("test")).succeeded()

	second := workspace.write("lab/fixtures/ledger/ledger.go", ledger(time.Now().Format(time.RFC3339Nano))).build(only("test")).succeeded()

	spec.Expect(second.cameFrom("//lab/fixtures:go.test/ledger")).To(Equal("executed"))
	spec.Expect(second.cameFrom("//lab/fixtures:go.test/billing")).To(Equal("executed"))
	spec.Expect(second.cameFrom("//lab/fixtures:go.test/orders")).To(Equal("grid_cache"))
}

// A Go module's tests, one action for each package / A test that fails fails its own package's action, and the other packages are tested.
func TestATestThatFailsFailsItsOwnPackageSActionAndTheOtherPackagesAreTested(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.testedPackages()

	build := workspace.write("lab/fixtures/orders/orders_test.go", "package orders\n\nimport \"testing\"\n\nfunc TestCount(t *testing.T) { t.Fatal(\"counted wrong\") }\n").build(only("test"))

	spec.Expect(build.outcome()).To(Equal("TESTS_FAILED"))
	spec.Expect(build.failed()).To(Equal([]string{"//lab/fixtures:go.test/orders"}))
	spec.Expect(build.printed("//lab/fixtures:go.test/orders")).To(ContainSubstring("counted wrong"))
	spec.Expect(build.outcomeOf("//lab/fixtures:go.test/ledger")).To(Equal("passed"))
}

// A Go module's tests, one action for each package / A test that ends its program as if all had passed has not passed, as the go command has it.
func TestATestThatEndsItsProgramAsIfAllHadPassedHasNotPassedAsTheGoCommandHasIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.testedPackages()

	// The first test ends the program with 0; the one after it, which
	// fails, is never run.
	build := workspace.write("lab/fixtures/orders/orders_test.go", "package orders\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestCount(t *testing.T) { os.Exit(0) }\n\nfunc TestNeverRun(t *testing.T) { t.Fatal(\"never run\") }\n").build(only("test"))

	spec.Expect(build.outcome()).To(Equal("TESTS_FAILED"))
	spec.Expect(build.failed()).To(Equal([]string{"//lab/fixtures:go.test/orders"}))
}

// A Go module's tests, one action for each package / Two packages the go command would name alike are each tested.
func TestTwoPackagesTheGoCommandWouldNameAlikeAreEachTested(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource()
	for _, directory := range []string{"near/util", "far/util"} {
		source.says("lab", "fixtures/"+directory+"/util.go", "package util\n\nfunc Where() string { return \""+directory+"\" }\n")
		source.says("lab", "fixtures/"+directory+"/util_test.go", "package util\n\nimport \"testing\"\n\nfunc TestWhere(t *testing.T) {\n\tif Where() != \""+directory+"\" {\n\t\tt.Fatal(Where())\n\t}\n}\n")
	}

	build := source.workspaceOf("fixtures").build(only("test")).succeeded()

	spec.Expect(build.outcomeOf("//lab/fixtures:go.test/near/util")).To(Equal("passed"))
	spec.Expect(build.outcomeOf("//lab/fixtures:go.test/far/util")).To(Equal("passed"))
	// Each program is built by an action of its own: the go command
	// writes two of one name into no one directory.
	spec.Expect(build.actions()).To(ContainElements("//lab/fixtures:go.compile-tests", "//lab/fixtures:go.compile-tests/1"))
}

// A Go module's tests, one action for each package / A package the go command builds no tests for on a worker has nothing to do there, and fails nothing.
func TestAPackageTheGoCommandBuildsNoTestsForOnAWorkerHasNothingToDoThereAndFailsNothing(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource()
	// Tests for another system alone, beside source for every system;
	// and a package with no file for a worker at all.
	source.says("lab", "fixtures/elsewhere/elsewhere.go", "package elsewhere\n")
	source.says("lab", "fixtures/elsewhere/elsewhere_windows_test.go", "package elsewhere\n\nimport \"testing\"\n\nfunc TestThere(t *testing.T) { t.Fatal(\"this is no Windows\") }\n")
	source.says("lab", "fixtures/nowhere/nowhere_windows.go", "package nowhere\n")
	source.says("lab", "fixtures/nowhere/nowhere_windows_test.go", "package nowhere\n\nimport \"testing\"\n\nfunc TestThere(t *testing.T) { t.Fatal(\"this is no Windows\") }\n")
	source.says("lab", "fixtures/tagged/tagged.go", "package tagged\n")
	source.says("lab", "fixtures/tagged/tagged_test.go", "//go:build integration\n\npackage tagged\n\nimport \"testing\"\n\nfunc TestTagged(t *testing.T) { t.Fatal(\"nobody gave the tag\") }\n")
	// And a directory of nothing but tests behind a tag, which the go
	// command, were it named to it outright, would fail on.
	source.says("lab", "fixtures/e2e/e2e_test.go", "//go:build e2e\n\npackage e2e\n\nimport \"testing\"\n\nfunc TestEndToEnd(t *testing.T) { t.Fatal(\"nobody gave the tag\") }\n")

	build := source.workspaceOf("fixtures").build(only("test")).succeeded()

	for _, idle := range []string{"elsewhere", "nowhere", "tagged", "e2e"} {
		spec.Expect(build.outcomeOf("//lab/fixtures:go.test/"+idle)).To(Equal("idle"), idle)
		spec.Expect(build.saysOf("//lab/fixtures:go.test/" + idle)).To(ContainSubstring("left no _tests/0/" + idle + ".test"))
	}

	// The control: the module's own package has tests for a worker,
	// and they are run.
	spec.Expect(build.outcomeOf("//lab/fixtures:go.test/.")).To(Equal("passed"))
}

// A Go module's tests, one action for each package / A module whose every test is behind a tag nobody gave has nothing to test, and its build passes.
func TestAModuleWhoseEveryTestIsBehindATagNobodyGaveHasNothingToTestAndItsBuildPasses(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource()
	// The module greet has no test of its own: these are its only ones.
	source.says("services", "greet/tagged/tagged.go", "package tagged\n")
	source.says("services", "greet/tagged/tagged_test.go", "//go:build integration\n\npackage tagged\n\nimport \"testing\"\n\nfunc TestTagged(t *testing.T) { t.Fatal(\"nobody gave the tag\") }\n")

	build := source.workspaceOf("greet").build(only("test"))

	spec.Expect(build.outcome()).To(Equal("SUCCESS"), build.diagnostics())
	spec.Expect(build.outcomeOf("//services/greet:go.compile-tests")).To(Equal("passed"))
	spec.Expect(build.outcomeOf("//services/greet:go.test/tagged")).To(Equal("idle"))
}

// A Go module's tests, one action for each package / Two packages of one name, one of them with no tests, do not stand in each other's way.
func TestTwoPackagesOfOneNameOneOfThemWithNoTestsDoNotStandInEachOtherSWay(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource()
	source.says("lab", "fixtures/near/util/util.go", "package util\n")
	source.says("lab", "fixtures/near/util/util_test.go", "package util\n\nimport \"testing\"\n\nfunc TestNear(t *testing.T) {}\n")
	source.says("lab", "fixtures/far/util/util.go", "package util\n")

	build := source.workspaceOf("fixtures").build(only("test")).succeeded()

	spec.Expect(build.outcomeOf("//lab/fixtures:go.test/near/util")).To(Equal("passed"))
}

// A Go module's tests, one action for each package / A package in a directory named vendor is tested, as the go command tests it.
func TestAPackageInADirectoryNamedVendorIsTestedAsTheGoCommandTestsIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource()
	source.says("lab", "fixtures/internal/vendor/vendor.go", "package vendor\n")
	source.says("lab", "fixtures/internal/vendor/vendor_test.go", "package vendor\n\nimport \"testing\"\n\nfunc TestSupplier(t *testing.T) {}\n")

	build := source.workspaceOf("fixtures").build(only("test")).succeeded()

	spec.Expect(build.outcomeOf("//lab/fixtures:go.test/internal/vendor")).To(Equal("passed"))
}

// A Go module's tests, one action for each package / A package is tested where the repository builds with the tag its tests ask for.
func TestAPackageIsTestedWhereTheRepositoryBuildsWithTheTagItsTestsAskFor(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource().sets("lab", "tags", "[integration]")
	source.says("lab", "fixtures/tagged/tagged.go", "package tagged\n")
	source.says("lab", "fixtures/tagged/tagged_test.go", "//go:build integration\n\npackage tagged\n\nimport \"testing\"\n\nfunc TestTagged(t *testing.T) {}\n")

	build := source.workspaceOf("fixtures").build(only("test")).succeeded()

	spec.Expect(build.outcomeOf("//lab/fixtures:go.test/tagged")).To(Equal("passed"))
}

// A Go module's tests, one action for each package / A package whose tests do not compile fails what builds the tests, and no package of the module is tested.
func TestAPackageWhoseTestsDoNotCompileFailsWhatBuildsTheTestsAndNoPackageOfTheModuleIsTested(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.testedPackages()

	build := workspace.write("lab/fixtures/orders/orders_test.go", "package orders\n\nimport \"testing\"\n\nfunc TestCount(t *testing.T) { undefined() }\n").build(only("test"))

	spec.Expect(build.outcome()).To(Equal("BUILD_FAILURE"))
	spec.Expect(build.failed()).To(Equal([]string{"//lab/fixtures:go.compile-tests"}))
	spec.Expect(build.outcomeOf("//lab/fixtures:go.test/ledger")).To(Equal("skipped"))
}

// A Go module's tests, one action for each package / Pointing at a module's tests is every package's, and one package's is pointed at alone.
func TestPointingAtAModuleSTestsIsEveryPackageSAndOnePackageSIsPointedAtAlone(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.testedPackages()

	all := workspace.build(of("//lab/fixtures:go.test"))
	spec.Expect(all.outcome()).To(Equal("SUCCESS"), all.diagnostics())
	spec.Expect(testsOf(all)).To(ContainElements(
		"//lab/fixtures:go.test/ledger",
		"//lab/fixtures:go.test/billing",
		"//lab/fixtures:go.test/orders",
	))

	one := workspace.build(of("//lab/fixtures:go.test/orders"))
	spec.Expect(one.outcome()).To(Equal("SUCCESS"), one.diagnostics())
	spec.Expect(testsOf(one)).To(Equal([]string{"//lab/fixtures:go.test/orders"}))
}

// testedPackages is a workspace of the module "fixtures" with three
// packages beneath it that have tests: ledger, billing, which imports
// ledger, and orders, which imports neither.
func (spec *scenario) testedPackages() *Workspace {
	spec.Helper()
	source := spec.changedSource()
	source.says("lab", "fixtures/ledger/ledger.go", ledger("as it was"))
	source.says("lab", "fixtures/ledger/ledger_test.go", ledgerTest("as it was"))
	source.says("lab", "fixtures/billing/billing.go", "package billing\n\nimport \"example.com/fixtures/ledger\"\n\nfunc Owed() int { return ledger.Balance() + 1 }\n")
	source.says("lab", "fixtures/billing/billing_test.go", "package billing\n\nimport \"testing\"\n\nfunc TestOwed(t *testing.T) {\n\tif Owed() != 2 {\n\t\tt.Fatal(Owed())\n\t}\n}\n")
	source.says("lab", "fixtures/orders/orders.go", "package orders\n\nfunc Count() int { return 0 }\n")
	source.says("lab", "fixtures/orders/orders_test.go", "package orders\n\nimport \"testing\"\n\nfunc TestCount(t *testing.T) {\n\tif Count() != 0 {\n\t\tt.Fatal(Count())\n\t}\n}\n")
	return source.workspaceOf("fixtures")
}

// ledger is the package ledger, saying what it is told to: a change to
// what it says is a change to what is built from it.
func ledger(says string) string {
	return fmt.Sprintf("package ledger\n\nconst Says = %q\n\nfunc Balance() int { return 1 }\n", says)
}

// ledgerTest is ledger's test, logging what it is told to.
func ledgerTest(says string) string {
	return fmt.Sprintf("package ledger\n\nimport \"testing\"\n\nfunc TestBalance(t *testing.T) {\n\tt.Log(%q)\n\tif Balance() != 1 {\n\t\tt.Fatal(Balance())\n\t}\n}\n", says)
}

// testsOf are the build's actions that test one package of a module.
func testsOf(build *Build) []string {
	var tests []string
	for _, label := range build.actions() {
		if strings.Contains(label, ":go.test/") {
			tests = append(tests, label)
		}
	}
	return tests
}

// A package's tests are cut into shards where a repository or an entity
// says how many at most (test-shards): each shard is an action that runs
// the package's one test program over a share of its tests, so they run
// beside each other on the grid. The shares are of the tests the plugin
// read from the package's files, and the last shard runs whatever the
// others were not told to, so a test is run once however it is written.
// A Go package's tests, cut into shards / Each test of a package is run by one shard and no more, one the plugin did not read among them.
func TestEachTestOfAPackageIsRunByOneShardAndNoMoreOneThePluginDidNotReadAmongThem(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.shardedPackage("")

	build := workspace.build(only("test"))

	spec.Expect(build.outcome()).To(Equal("SUCCESS"), build.diagnostics())
	first, last := "//lab/fixtures:go.test/many@1of2", "//lab/fixtures:go.test/many@2of2"
	spec.Expect(build.actions()).To(ContainElements(first, last))
	spec.Expect(build.actions()).NotTo(ContainElement("//lab/fixtures:go.test/many"))
	ran := build.printed(first) + "\n" + build.printed(last)
	// What an example prints is the testing package's to compare, and
	// is not printed: the example is not counted here.
	for _, test := range []string{"TestA", "TestB", "TestC", "TestD", "TestUnread"} {
		spec.Expect(strings.Count(ran, "ran "+test+"\n")).To(Equal(1), "%s, in what the two shards printed:\n%s", test, ran)
	}
	// Each shard ran some and not all.
	spec.Expect(build.printed(first)).To(And(ContainSubstring("ran TestA"), Not(ContainSubstring("ran TestB"))))
	// A package of one test is one action, whatever is asked.
	spec.Expect(build.actions()).To(ContainElement("//lab/fixtures:go.test/."))
}

// A Go package's tests, cut into shards / A test that fails fails the shard that runs it, and the other shard passes.
func TestATestThatFailsFailsTheShardThatRunsItAndTheOtherShardPasses(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.shardedPackage("TestC").build(only("test"))

	spec.Expect(build.outcome()).To(Equal("TESTS_FAILED"))
	spec.Expect(build.failed()).To(Equal([]string{"//lab/fixtures:go.test/many@1of2"}))
	spec.Expect(build.outcomeOf("//lab/fixtures:go.test/many@2of2")).To(Equal("passed"))
}

// A Go package's tests, cut into shards / Pointing at a module's tests is every shard of every package.
func TestPointingAtAModuleSTestsIsEveryShardOfEveryPackage(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.shardedPackage("").build(of("//lab/fixtures:go.test"))

	spec.Expect(build.outcome()).To(Equal("SUCCESS"), build.diagnostics())
	spec.Expect(testsOf(build)).To(ContainElements("//lab/fixtures:go.test/many@1of2", "//lab/fixtures:go.test/many@2of2", "//lab/fixtures:go.test/."))
}

// A Go package's tests, cut into shards / Pointing at a package that is cut into shards is each of its shards.
func TestPointingAtAPackageThatIsCutIntoShardsIsEachOfItsShards(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.shardedPackage("").build(of("//lab/fixtures:go.test/many"))

	spec.Expect(build.outcome()).To(Equal("SUCCESS"), build.diagnostics())
	spec.Expect(testsOf(build)).To(Equal([]string{"//lab/fixtures:go.test/many@1of2", "//lab/fixtures:go.test/many@2of2"}))
}

// A Go package's tests, cut into shards / An entity says how many shards its own packages are cut into, and no other's.
func TestAnEntitySaysHowManyShardsItsOwnPackagesAreCutIntoAndNoOtherS(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource().entitySets("greet", "test-shards", "2")
	for _, module := range []string{"greet", "hello"} {
		source.says("services", module+"/many/many.go", "package many\n")
		source.says("services", module+"/many/many_test.go", "package many\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\nfunc TestB(t *testing.T) {}\n")
	}

	build := source.workspaceOf("hello").build(only("test"))

	spec.Expect(build.outcome()).To(Equal("SUCCESS"), build.diagnostics())
	spec.Expect(build.actions()).To(ContainElements("//services/greet:go.test/many@1of2", "//services/greet:go.test/many@2of2"))
	// hello, which calls greet and sets nothing, is cut into none.
	spec.Expect(build.actions()).To(ContainElement("//services/hello:go.test/many"))
}

// A Go package's tests, cut into shards / Fewer shards than one is refused, naming the option.
func TestFewerShardsThanOneIsRefusedNamingTheOption(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource().sets("lab", "test-shards", "0")

	build := source.workspaceOf("fixtures").build()

	spec.Expect(build.outcome()).To(Equal("BUILD_FAILURE"))
	spec.Expect(build.says()).To(ContainSubstring("test-shards"))
}

// shardedPackage is a workspace of the module "fixtures" whose repository
// asks for two shards at most, with a package "many" of four tests the
// plugin reads, one it does not (it is not at the start of its line) and
// an example. Each says that it ran. The test named fails, where one is.
func (spec *scenario) shardedPackage(failing string) *Workspace {
	spec.Helper()
	source := spec.changedSource().sets("lab", "test-shards", "2")
	file := "package many\n\nimport (\n\t\"fmt\"\n\t\"testing\"\n)\n\nfunc ran(t *testing.T) {\n\tfmt.Println(\"ran \" + t.Name())\n\tif t.Name() == \"" + failing + "\" {\n\t\tt.Fatal(\"it fails\")\n\t}\n}\n\n"
	for _, test := range []string{"TestA", "TestB", "TestC", "TestD"} {
		file += "func " + test + "(t *testing.T) { ran(t) }\n"
	}
	file += "\tfunc TestUnread(t *testing.T) { ran(t) }\n\nfunc Example() {\n\tfmt.Println(\"ran Example\")\n\t// Output: ran Example\n}\n"
	source.says("lab", "fixtures/many/many.go", "package many\n")
	source.says("lab", "fixtures/many/many_test.go", file)
	return source.workspaceOf("fixtures")
}

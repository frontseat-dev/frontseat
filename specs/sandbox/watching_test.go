package sandbox_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
	"github.com/frontseat-dev/frontseat/testsupport/testpins"

	. "github.com/onsi/gomega"
)

// tested is a test file of a package of the module, which passes or
// fails saying words of this run's own: what it says was never run
// before, so a result of it is of this run.
func tested(pkg, said string, passes bool) string {
	fails := ""
	if !passes {
		fails = "\n\tt.Fatal(\"it does not hold\")"
	}
	return fmt.Sprintf("package %s\n\nimport \"testing\"\n\nfunc TestIt(t *testing.T) {\n\tt.Log(%q)%s\n}\n", pkg, said, fails)
}

// aWatch is one `frontseat test --watch`, started in a workspace from a
// shell that ends: it goes on, and says how it exited once it has. The
// shell that runs it says its process, and it is ended by that process
// alone, and never by a name.
type aWatch struct {
	spec      *scenario
	workspace *Workspace
	builds    buildv1alpha1connect.BuildServiceClient
}

// watching starts `frontseat test --watch` in a workspace's checkout.
func (spec *scenario) watching(workspace *Workspace) *aWatch {
	spec.Helper()
	spec.Expect(workspace.exits("cd " + code + " && (nohup sh -c 'frontseat test --watch & echo $! > /tmp/watch.pid; wait $!; echo $? > /tmp/watch.exit' > /tmp/watch.log 2>&1 &)")).To(Equal(0))
	return &aWatch{spec: spec, workspace: workspace, builds: buildsAt(workspace)}
}

// log is what the watch printed so far.
func (w *aWatch) log() string { return w.workspace.holds("/tmp/watch.log") }

// ended is the builds the workspace's daemon was asked for, oldest
// first, once the newest has ended.
func (w *aWatch) ended(g Gomega) []*buildv1.Build {
	listed, err := w.builds.ListBuilds(context.Background(), &buildv1.ListBuildsRequest{PageSize: 100})
	g.Expect(err).NotTo(HaveOccurred())
	all := listed.GetBuilds()
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	if len(all) > 0 {
		g.Expect(all[len(all)-1].GetState()).NotTo(Equal(buildv1.Build_STATE_RUNNING))
	}
	return all
}

// round waits for the watch's build of a number to have ended, and
// returns it with where each of its actions came from.
func (w *aWatch) round(number int) (*buildv1.Build, map[string]buildv1.Provenance) {
	w.spec.Helper()
	var build *buildv1.Build
	w.spec.Eventually(func(g Gomega) {
		all := w.ended(g)
		g.Expect(all).To(HaveLen(number), w.log())
		build = all[number-1]
	}, 5*time.Minute, time.Second).Should(Succeed())
	actions, err := w.builds.ListBuildActions(context.Background(), &buildv1.ListBuildActionsRequest{PageSize: 1000, BuildId: build.GetId()})
	w.spec.Expect(err).NotTo(HaveOccurred())
	from := map[string]buildv1.Provenance{}
	for _, action := range actions.GetActions() {
		from[action.GetLabel()] = action.GetProvenance()
	}
	return build, from
}

// asksForNoMore holds that, with nothing written, the watch asks for no
// build beyond those it has.
func (w *aWatch) asksForNoMore(builds int) {
	w.spec.Helper()
	w.spec.Consistently(func(g Gomega) { g.Expect(w.ended(g)).To(HaveLen(builds)) }, 5*time.Second, time.Second).Should(Succeed())
}

// stopped ends the watch (SIGTERM), and holds it to say so by its exit
// code, 130.
func (w *aWatch) stopped() {
	w.spec.Helper()
	w.spec.Expect(w.workspace.exits("kill -TERM \"$(cat /tmp/watch.pid)\"")).To(Equal(0))
	w.spec.Eventually(func() string { return strings.TrimSpace(w.workspace.prints("cat /tmp/watch.exit 2>/dev/null")) }, time.Minute, time.Second).Should(Equal("130"), w.log())
}

// In a workspace, one `frontseat test --watch` tests, and tests again
// each time a file is written, from another shell, until it is ended:
// with nothing written it asks for no build; a test file written is
// tested again, executed and not answered from the cache, and the test
// of the package beside it, which the write does not reach, is not
// executed again; a write that breaks a test shows the failure, and the
// command goes on; the write that puts it right shows it passing. Ended
// (SIGTERM), it exits 130.
//
// Not proven here: a write the watch could miss (a file written again
// at its size with its times set back, which
// TestAWaitNamesWhatTheBuildListedAndWhatItIsMadeOf holds of the
// daemon's wait), and a daemon
// replaced between rounds
// (TestAWatchGoesOnWithTheDaemonThereIsForEachRound,
// TestAWaitTheDaemonCouldNotAnswerIsBuiltAgainOfTheDaemonThereIsThen,
// TestARoundADaemonRefusedAsItWasEndingIsAskedOfTheNext).
// Building on every change / A watched test runs again what a write reaches, and nothing else.
func TestAWatchedTestRunsAgainWhatAWriteReachesAndNothingElse(t *testing.T) {
	spec := newScenario(t)
	run := fmt.Sprintf("of-run-%d", time.Now().UnixNano())
	const beside, other = "//code/hello:go.test/.", "//code/hello:go.test/other"
	workspace := spec.aModule("package main\n\nfunc main() {}\n",
		"hello/main_test.go", tested("main", "beside "+run, true),
		"hello/other/other.go", "package other\n",
		"hello/other/other_test.go", tested("other", "first "+run, true))
	watch := spec.watching(workspace)
	first, from := watch.round(1)
	spec.Expect(first.GetExitCode()).To(Equal(buildv1.ExitCode_EXIT_CODE_SUCCESS), watch.log())
	spec.Expect(from).To(HaveKeyWithValue(beside, buildv1.Provenance_PROVENANCE_EXECUTED))
	spec.Expect(from).To(HaveKeyWithValue(other, buildv1.Provenance_PROVENANCE_EXECUTED))
	// Nothing is written: nothing is built.
	watch.asksForNoMore(1)

	// A test file is written, from another shell: it is tested again,
	// and the test beside it is not.
	workspace.copyIn(code+"/hello/other/other_test.go", tested("other", "second "+run, true))
	second, from := watch.round(2)
	spec.Expect(second.GetExitCode()).To(Equal(buildv1.ExitCode_EXIT_CODE_SUCCESS), watch.log())
	spec.Expect(from).To(HaveKeyWithValue(other, buildv1.Provenance_PROVENANCE_EXECUTED))
	spec.Expect(from).To(HaveKeyWithValue(beside, buildv1.Provenance_PROVENANCE_GRID_CACHE), "the write does not reach the test beside it")
	spec.Expect(watch.log()).To(ContainSubstring("code/hello/other/other_test.go changed: building again"))

	// A write that breaks the test: the failure is shown, and the
	// command goes on.
	workspace.copyIn(code+"/hello/other/other_test.go", tested("other", "third "+run, false))
	third, from := watch.round(3)
	spec.Expect(third.GetExitCode()).To(Equal(buildv1.ExitCode_EXIT_CODE_TESTS_FAILED), watch.log())
	spec.Expect(from).To(HaveKeyWithValue(other, buildv1.Provenance_PROVENANCE_EXECUTED))
	spec.Eventually(watch.log, time.Minute, time.Second).Should(ContainSubstring("it does not hold"))
	spec.Expect(workspace.exits("test -e /tmp/watch.exit")).NotTo(Equal(0), "a test that failed ended the watch: %s", watch.log())

	// The write that puts it right: it passes, in the same command.
	workspace.copyIn(code+"/hello/other/other_test.go", tested("other", "fourth "+run, true))
	fourth, from := watch.round(4)
	spec.Expect(fourth.GetExitCode()).To(Equal(buildv1.ExitCode_EXIT_CODE_SUCCESS), watch.log())
	spec.Expect(from).To(HaveKeyWithValue(other, buildv1.Provenance_PROVENANCE_EXECUTED))
	spec.Expect(strings.Count(watch.log(), "building again")).To(Equal(3), watch.log())

	watch.stopped()
}

// A workspace of Cargo's is configured in .cargo/config.toml, which the
// Cargo plugin reads by name, beneath a directory whose name begins
// with a dot that no listing takes. `frontseat test --watch` tests it,
// and, with nothing written, asks for no build; written again, and
// nothing else, the configuration has it test again, saying that it
// changed. A file of the same kind of directory that no build reads
// (.idea/workspace.xml), written, starts no round: the control. Ended
// (SIGTERM), it exits 130.
// Building on every change / A watched test runs again when only what a plugin reads by name beneath a dot directory changes.
func TestAWatchedTestRunsAgainWhenOnlyCargosConfigurationChanges(t *testing.T) {
	spec := newScenario(t)
	checkout := spec.aRepository()
	testfiles.Tree(spec.TB, checkout, map[string]string{
		"Cargo.toml":          "[workspace]\nresolver = \"2\"\nmembers = [\"app\"]\n",
		"Cargo.lock":          "# This file is automatically @generated by Cargo.\n# It is not intended for manual editing.\nversion = 4\n\n[[package]]\nname = \"app\"\nversion = \"0.1.0\"\n",
		"app/Cargo.toml":      "[package]\nname = \"app\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		"app/src/lib.rs":      "#[cfg(test)]\nmod tests {\n    #[test]\n    fn it_holds() {}\n}\n",
		".cargo/config.toml":  "[term]\nverbose = false\n",
		".idea/workspace.xml": "<project/>\n",
		"frontseat.yaml":      "plugins:\n  cargo: {}\n",
	})
	testpins.Own(spec.TB, checkout)
	workspace := spec.inASandboxOfItsOwn(spec.cloning(checkout))
	watch := spec.watching(workspace)
	first, _ := watch.round(1)
	spec.Expect(first.GetExitCode()).To(Equal(buildv1.ExitCode_EXIT_CODE_SUCCESS), watch.log())
	watch.asksForNoMore(1)

	// A file no build reads, beneath a directory of a tool's: no round.
	workspace.copyIn(code+"/.idea/workspace.xml", "<project version=\"2\"/>\n")
	watch.asksForNoMore(1)

	// Cargo's configuration alone, written again: one round, which says
	// so.
	workspace.copyIn(code+"/.cargo/config.toml", "[term]\nverbose = false\nquiet = false\n")
	second, _ := watch.round(2)
	spec.Expect(second.GetExitCode()).To(Equal(buildv1.ExitCode_EXIT_CODE_SUCCESS), watch.log())
	spec.Expect(watch.log()).To(ContainSubstring("code/.cargo/config.toml changed: building again"))
	watch.asksForNoMore(2)

	watch.stopped()
}

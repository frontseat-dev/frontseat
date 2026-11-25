package sandbox_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"

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
// TestAFileRewrittenAtItsSizeWithItsTimesSetBackLooksOtherwise holds of
// the look; a file made and removed again during a round,
// TestAChangeDuringARoundIsBuiltByOneRoundAfterIt), and a daemon
// replaced between rounds
// (TestAWatchGoesOnWithTheDaemonThereIsForEachRound,
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
	builds := buildsAt(workspace)
	// The builds the workspace's daemon was asked for, oldest first,
	// once the newest has ended.
	ended := func(g Gomega) []*buildv1.Build {
		listed, err := builds.ListBuilds(context.Background(), &buildv1.ListBuildsRequest{PageSize: 100})
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
	log := func() string { return workspace.holds("/tmp/watch.log") }
	// round waits for the watch's build of a number to have ended, and
	// returns it with where each of its actions came from.
	round := func(number int) (*buildv1.Build, map[string]buildv1.Provenance) {
		spec.Helper()
		var build *buildv1.Build
		spec.Eventually(func(g Gomega) {
			all := ended(g)
			g.Expect(all).To(HaveLen(number), log())
			build = all[number-1]
		}, 5*time.Minute, time.Second).Should(Succeed())
		actions, err := builds.ListBuildActions(context.Background(), &buildv1.ListBuildActionsRequest{PageSize: 1000, BuildId: build.GetId()})
		spec.Expect(err).NotTo(HaveOccurred())
		from := map[string]buildv1.Provenance{}
		for _, action := range actions.GetActions() {
			from[action.GetLabel()] = action.GetProvenance()
		}
		return build, from
	}

	// It goes on when the shell that started it has ended, and says how
	// it exited once it has.
	// The shell that runs it says its process and, once it has ended, how
	// it exited: it is ended by that process alone, and never by a name.
	spec.Expect(workspace.exits("cd " + code + " && (nohup sh -c 'frontseat test --watch & echo $! > /tmp/watch.pid; wait $!; echo $? > /tmp/watch.exit' > /tmp/watch.log 2>&1 &)")).To(Equal(0))
	first, from := round(1)
	spec.Expect(first.GetExitCode()).To(Equal(buildv1.ExitCode_EXIT_CODE_SUCCESS), log())
	spec.Expect(from).To(HaveKeyWithValue(beside, buildv1.Provenance_PROVENANCE_EXECUTED))
	spec.Expect(from).To(HaveKeyWithValue(other, buildv1.Provenance_PROVENANCE_EXECUTED))
	// Nothing is written: nothing is built.
	spec.Consistently(func(g Gomega) { g.Expect(ended(g)).To(HaveLen(1)) }, 5*time.Second, time.Second).Should(Succeed())

	// A test file is written, from another shell: it is tested again,
	// and the test beside it is not.
	workspace.copyIn(code+"/hello/other/other_test.go", tested("other", "second "+run, true))
	second, from := round(2)
	spec.Expect(second.GetExitCode()).To(Equal(buildv1.ExitCode_EXIT_CODE_SUCCESS), log())
	spec.Expect(from).To(HaveKeyWithValue(other, buildv1.Provenance_PROVENANCE_EXECUTED))
	spec.Expect(from).To(HaveKeyWithValue(beside, buildv1.Provenance_PROVENANCE_GRID_CACHE), "the write does not reach the test beside it")
	spec.Expect(log()).To(ContainSubstring("code/hello/other/other_test.go changed: building again"))

	// A write that breaks the test: the failure is shown, and the
	// command goes on.
	workspace.copyIn(code+"/hello/other/other_test.go", tested("other", "third "+run, false))
	third, from := round(3)
	spec.Expect(third.GetExitCode()).To(Equal(buildv1.ExitCode_EXIT_CODE_TESTS_FAILED), log())
	spec.Expect(from).To(HaveKeyWithValue(other, buildv1.Provenance_PROVENANCE_EXECUTED))
	spec.Eventually(log, time.Minute, time.Second).Should(ContainSubstring("it does not hold"))
	spec.Expect(workspace.exits("test -e /tmp/watch.exit")).NotTo(Equal(0), "a test that failed ended the watch: %s", log())

	// The write that puts it right: it passes, in the same command.
	workspace.copyIn(code+"/hello/other/other_test.go", tested("other", "fourth "+run, true))
	fourth, from := round(4)
	spec.Expect(fourth.GetExitCode()).To(Equal(buildv1.ExitCode_EXIT_CODE_SUCCESS), log())
	spec.Expect(from).To(HaveKeyWithValue(other, buildv1.Provenance_PROVENANCE_EXECUTED))
	spec.Expect(strings.Count(log(), "building again")).To(Equal(3), log())

	// Ended, it says so by its exit code.
	spec.Expect(workspace.exits("kill -TERM \"$(cat /tmp/watch.pid)\"")).To(Equal(0))
	spec.Eventually(func() string { return strings.TrimSpace(workspace.prints("cat /tmp/watch.exit 2>/dev/null")) }, time.Minute, time.Second).Should(Equal("130"), log())
}

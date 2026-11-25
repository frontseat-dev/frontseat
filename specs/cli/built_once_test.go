package e2e

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/sandbox/testsupport/testbuild"
	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
	"github.com/frontseat-dev/frontseat/testsupport/testforge"
	"github.com/frontseat-dev/frontseat/testsupport/testmodule"
)

// builtCommand builds this checkout into dist/, as a job does before it
// builds with it, and returns the repository's root and the command line
// built there.
func builtCommand(t *testing.T) (root, command string) {
	t.Helper()
	root, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	ran(t, root, 20*time.Minute, "mise", "run", "build")
	return root, filepath.Join(root, "dist", "bin", "frontseat")
}

// endedIn asks the build daemon of a box for a build, of the tests alone
// or of everything, follows it to its end, and returns what it says of
// its actions. One that did not succeed fails the test.
func endedIn(t *testing.T, box testbuild.Box, tests bool) testbuild.Results {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	here, err := box.Daemon(ctx, t).Here()
	if err != nil {
		t.Fatal(err)
	}
	started, err := here.Build(ctx, frontseat.BuildRequest{Tests: tests})
	if err != nil {
		t.Fatal(err)
	}
	ended, err := started.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	results, err := testbuild.ResultsOf(ctx, started)
	if err != nil {
		t.Fatal(err)
	}
	if ended.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS {
		t.Fatalf("the workspace's build (tests alone: %v) ended %s: %s\n%v", tests, ended.GetExitCode(), ended.GetFailure(), results)
	}
	return results
}

// CI of a commit a workspace built executes nothing. A workspace, a
// sandbox that cloned the commit, tests and builds it on the grid. A
// job then builds and tests a fresh checkout of the same commit outside
// any sandbox, as .github/workflows/frontseat-job.yml does: a settings
// file names the grid and its workers' platform (build.yaml), and
// `frontseat build` and `frontseat test` are run in the checkout, each
// through the build daemon the command line starts for itself. No
// worker runs anything for the job: each of its actions is one the
// workspace sent, by its digest, answered from what the grid kept.
//
// What differs between the two, as it does between a workspace and a
// job: the directory (/work/code in the box, a directory of this
// machine here), that the workspace holds its repository under a name
// and the job's checkout is the directory built, the user and the
// environment the daemon runs with, the system it runs on, where its
// plugins are (the box's, dist/plugins here) and the program (the box's,
// dist/bin/frontseat here, both built from this checkout). The settings
// name the same platform and no instance, as the box's do.
//
// The control is the workspace's own builds, where a worker runs each
// action of what no run had built. Not proven here: a job on another
// architecture than the workspace's, a grid that asks who is asking,
// and a job told another instance (the spec beside this one).
func TestACheckoutOfACommitAWorkspaceBuiltIsBuiltByTheCommandLineWithNothingExecuted(t *testing.T) {
	grid, assets := testgrid.Address(t), testgrid.Assets(t)
	image := testbox.Image(t)
	root, command := builtCommand(t)

	source := filepath.Join(t.TempDir(), "code")
	testforge.Greeting(t, source, fmt.Sprintf("word-%d", time.Now().UnixNano()))

	at := local(t)
	made, status := madeWorkspace(t, sdkClient(t, at.URL, at.Owner), frontseat.WorkspaceSpec{Image: image, Repositories: cloned(t, source)})
	box := testbuild.Workspace(made)
	before := endedIn(t, box, true)
	for label, result := range endedIn(t, box, false) {
		// One action of both builds is one action: the build after the
		// tests was answered it from what the grid kept, and the tests
		// say whether a worker ran it.
		if _, tested := before[label]; !tested {
			before[label] = result
		}
	}
	inWorkspace := func(label string) string { return "//code/" + strings.TrimPrefix(label, "//") }
	// The control: a worker ran each action of what no run had built.
	ranThere := strings.Join(before.Executed(), " ")
	for _, label := range testforge.GreetingActions.Changed {
		if !strings.Contains(ranThere+" ", inWorkspace(label)+" ") {
			t.Errorf("in the workspace a worker ran %q, want %s among them: it is of a word no run had built", ranThere, inWorkspace(label))
		}
	}

	// The job's checkout: a clone of its own, at the same commit.
	checkout := filepath.Join(t.TempDir(), "checkout")
	ran(t, root, time.Minute, "git", "clone", "--quiet", source, checkout)
	held := status.GetStatus().GetRepositories()
	if commit := strings.TrimSpace(ran(t, checkout, time.Minute, "git", "rev-parse", "HEAD")); len(held) != 1 || held[0].GetCommit() != commit {
		t.Fatalf("the workspace holds %v and the job's checkout is at %s, want both of one commit", held, commit)
	}
	job := aMachine(t, command)
	job.toldItsGrid(root, grid, assets, "")
	job.does(checkout, "build")
	job.does(checkout, "test")

	builds := job.builds()
	if len(builds) != 2 {
		t.Fatalf("the job's build daemon holds %d builds, want its build and its test", len(builds))
	}
	for _, results := range builds {
		if len(results) == 0 {
			t.Errorf("a build of the job's has no action")
		}
		for label := range results {
			if _, built := before[inWorkspace(label)]; !built {
				t.Errorf("the job has an action %s that the workspace's builds had none of (%s)", label, inWorkspace(label))
			}
		}
		if again := results.Rebuilt(before, inWorkspace); len(again) != 0 {
			t.Errorf("a job on the commit a workspace built had a worker run %d of its %d actions, want none:\n%s", len(again), len(results), strings.Join(again, "\n"))
		}
	}
}

// What a branch's builds and its workspaces built, a build on main that
// reads their instance does not build again. Two builds of one commit
// name two instances of one grid, as a job on a protected branch asks
// frontseat/p and every other build frontseat/b: the second, told to
// read the first's instance after its own (grid.reads), has no worker
// run anything. The grid keeps an action's result by the instance and
// the digest together, and what the action read and left by digest
// alone, and its workers take the actions of every instance: the daemon
// asks each instance it reads in turn, and takes what one kept.
//
// The control is a third build, of another checkout of the commit,
// under the second instance and told to read nothing else: a worker
// runs each action again, under the very digest it ran it under before,
// since that instance kept nothing of its own.
func TestABuildThatReadsAnotherInstanceRunsNothingThatInstanceBuilt(t *testing.T) {
	// Skipped before anything is built, where no grid is up.
	testgrid.Address(t)
	root, command := builtCommand(t)

	source := aNewCommit(t)
	built := func(instance string, reads ...string) testbuild.Results {
		t.Helper()
		return builtUnder(t, root, command, source, instance, reads...)
	}
	all := helloActions

	branch := built("frontseat/b")
	if got := strings.Join(branch.Executed(), " "); got != strings.Join(all, " ") {
		t.Fatalf("under frontseat/b a worker ran %q, want %q: the commit is one no run had built", got, all)
	}

	protected := built("frontseat/p", "frontseat/b")
	if again := protected.Rebuilt(branch, nil); len(again) != 0 {
		t.Errorf("under frontseat/p, told to read frontseat/b after frontseat/b built the commit, a worker ran what frontseat/b built:\n%s", strings.Join(again, "\n"))
	}
	for _, label := range all {
		if was, is := branch[label].GetActionDigest().GetHash(), protected[label].GetActionDigest().GetHash(); was == "" || was != is {
			t.Errorf("%s is %.12s under frontseat/b and %.12s under frontseat/p, want one action under both: the instance is no part of an action's digest", label, was, is)
		}
	}

	// The control: the second instance, told to read nothing else.
	if got := strings.Join(built("frontseat/p").Executed(), " "); got != strings.Join(all, " ") {
		t.Errorf("under frontseat/p, told to read nothing else, a worker ran %q, want %q: what frontseat/b kept is no answer to an instance that does not read it", got, all)
	}
}

// helloActions are the actions a build of helloModule runs.
var helloActions = []string{"//hello:go.compile", "//hello:go.format", "//hello:go.lint"}

// aNewCommit is a repository of helloModule at a commit of its own, of
// a word no build has built.
func aNewCommit(t *testing.T) string {
	t.Helper()
	source := helloModule(t)
	testfiles.Write(t, filepath.Join(source, "hello", "main.go"), fmt.Sprintf("package main\n\nfunc main() { println(%q) }\n", fmt.Sprintf("word-%d", time.Now().UnixNano())))
	testforge.Committed(t, source)
	return source
}

// builtUnder is what the one build of a checkout of a repository's
// commit says of its actions, on a machine of its own told the local
// stack's grid, an instance, and the other instances it reads.
func builtUnder(t *testing.T, root, command, source, instance string, reads ...string) testbuild.Results {
	t.Helper()
	grid, assets := testgrid.Address(t), testgrid.Assets(t)
	checkout := filepath.Join(t.TempDir(), "checkout")
	ran(t, root, time.Minute, "git", "clone", "--quiet", source, checkout)
	machine := aMachine(t, command)
	machine.toldItsGrid(root, grid, assets, instance, reads...)
	machine.does(checkout, "build")
	builds := machine.builds()
	if len(builds) != 1 {
		t.Fatalf("the build daemon told the instance %s holds %d builds, want its one", instance, len(builds))
	}
	return builds[0]
}

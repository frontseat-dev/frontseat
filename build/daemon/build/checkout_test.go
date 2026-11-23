package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/testsupport/commits"
)

// givenACheckout is a daemon given a checkout of a repository to
// release from, as a job's is: cloned by something else, at a branch.
// It returns the daemon's clients, the checkout and its remote.
func givenACheckout(t *testing.T, branch string, how ...string) (clients, string, string) {
	t.Helper()
	return givenACheckoutWith(t, scripted{}, branch, nil, 3, how...)
}

// givenACheckoutWith is givenACheckout run by an executor, of a remote
// written further where written is given, with so many of the plugins
// loaded, the first of them.
func givenACheckoutWith(t *testing.T, executor exec.Executor, branch string, written func(remote string), plugins int, how ...string) (clients, string, string) {
	t.Helper()
	remote := releasingRemote(t, written)
	checkout := filepath.Join(t.TempDir(), "checkout")
	commits.Git(t, filepath.Dir(checkout), append(append([]string{"clone", "--quiet", "--branch", branch}, how...), "file://"+remote, checkout)...)
	return serveFirst(t, configuration{Workspace: checkout, Mounted: true, Executor: executor}, plugins), checkout, remote
}

// A daemon given a checkout releases from it as from one it brought: it
// plans from the checkout's history, tags the commit there, and pushes
// the tag to where the checkout was brought from, which it reads of the
// checkout. A second release finds the unit released.
func TestADaemonGivenACheckoutReleasesFromIt(t *testing.T) {
	c, checkout, remote := givenACheckout(t, "release/1.0")
	head := commits.Git(t, checkout, "rev-parse", "HEAD")
	s := &releaseService{graph: c.daemon.releases.graph, releases: c.daemon.releases}
	release := releaseEnded(t, s, releaseBegun(t, s, "greet").GetId())
	if release.GetState() != buildv1.Release_STATE_SUCCEEDED || len(release.GetUnits()) != 1 {
		t.Fatalf("the release ended %v (%s) with %v", release.GetState(), release.GetFailure(), release.GetUnits())
	}
	if unit := release.GetUnits()[0]; unit.GetVersion() != "1.0.0" || len(unit.GetTags()) != 1 || unit.GetTags()[0].GetName() != "greet/v1.0.0" || unit.GetTags()[0].GetCommit() != head {
		t.Errorf("the unit was released as %v, want greet/v1.0.0 at %s", unit, head)
	}
	if at := commits.Git(t, remote, "rev-parse", "greet/v1.0.0^{commit}"); at != head || tagsOf(t, checkout) != "greet/v1.0.0" {
		t.Errorf("the remote has greet/v1.0.0 at %s, and the checkout the tags %q; want it at %s", at, tagsOf(t, checkout), head)
	}
	again := releaseEnded(t, s, releaseBegun(t, s, "greet").GetId())
	if again.GetState() != buildv1.Release_STATE_SUCCEEDED || len(again.GetUnits()) != 1 || !again.GetUnits()[0].GetAlreadyReleased() {
		t.Errorf("released again, it ended %v (%s) with %v", again.GetState(), again.GetFailure(), again.GetUnits())
	}
}

// What a checkout says of itself is input from outside, to a daemon
// given one as to one that brought it: a checkout that names another
// address to push to or a program to run is not
// followed: the tag reaches where the checkout was brought from and
// nowhere else, and the release repeats nothing planted. Git asked
// plainly follows each, which is the control.
func TestWhatAGivenCheckoutSaysOfItselfIsNotFollowed(t *testing.T) {
	for name, plant := range map[string][]string{
		"another address to push to": {"remote.origin.pushurl", "ELSEWHERE"},
		"a hook":                     {"core.hooksPath", "HOOKS"},
	} {
		t.Run(name, func(t *testing.T) {
			c, checkout, remote := givenACheckout(t, "release/1.0")
			elsewhere := filepath.Join(t.TempDir(), "elsewhere.git")
			commits.Git(t, t.TempDir(), "init", "--quiet", "--bare", elsewhere)
			hooks, ran := t.TempDir(), filepath.Join(t.TempDir(), "ran")
			if err := os.WriteFile(filepath.Join(hooks, "pre-push"), []byte("#!/bin/sh\ntouch "+ran+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			value := strings.NewReplacer("ELSEWHERE", elsewhere, "HOOKS", hooks).Replace(plant[1])
			commits.Git(t, checkout, "config", plant[0], value)
			s := &releaseService{graph: c.daemon.releases.graph, releases: c.daemon.releases}
			release := releaseEnded(t, s, releaseBegun(t, s, "greet").GetId())
			if strings.Contains(release.String(), "c2VjcmV0") {
				t.Errorf("the release repeats what the checkout planted: %s", release)
			}
			if tagsOf(t, elsewhere) != "" {
				t.Errorf("the tag reached the address the checkout named: %q", tagsOf(t, elsewhere))
			}
			if exists(ran) {
				t.Error("the checkout's hook ran")
			}
			// The tag reaches where the checkout was brought from.
			if release.GetState() != buildv1.Release_STATE_SUCCEEDED || tagsOf(t, remote) != "greet/v1.0.0" {
				t.Errorf("it ended %v (%s), and the remote has the tags %q", release.GetState(), release.GetFailure(), tagsOf(t, remote))
			}
			// Control: git asked plainly follows what the checkout says.
			commits.Git(t, checkout, "push", "--quiet", "origin", "refs/tags/greet/v1.0.0")
			switch name {
			case "another address to push to":
				if tagsOf(t, elsewhere) != "greet/v1.0.0" {
					t.Errorf("control: git asked plainly did not push where the checkout says, and the fixture plants nothing: %q", tagsOf(t, elsewhere))
				}
			case "a hook":
				if !exists(ran) {
					t.Error("control: git asked plainly did not run the checkout's hook, and the fixture plants nothing")
				}
			}
		})
	}
}

// A checkout that holds part of its history, as a job's does unless it
// asks for all of it, is not released from: its tags and commits cannot
// be read whole, and the release would be planned as a first one. The
// plan is refused, saying how the whole is brought. The control is the
// checkout of all of it, which TestADaemonGivenACheckoutReleasesFromIt
// releases from.
func TestACheckoutOfPartOfItsHistoryIsNotReleasedFrom(t *testing.T) {
	c, checkout, remote := givenACheckout(t, "release/1.0", "--depth", "1")
	s := &releaseService{graph: c.daemon.releases.graph, releases: c.daemon.releases}
	_, err := s.PlanRelease(context.Background(), releaseOf("greet"))
	if err == nil || !strings.Contains(err.Error(), "holds part of its history") || !strings.Contains(err.Error(), "a checkout of depth 0") {
		t.Errorf("a plan from a checkout of depth 1: %v", err)
	}
	if tagsOf(t, checkout) != "" || tagsOf(t, remote) != "" {
		t.Errorf("it left the tags %q here and %q there", tagsOf(t, checkout), tagsOf(t, remote))
	}
}

// A checkout that could not be pushed from is not planned from, so
// that nothing is built and tagged that cannot be sent: one that keeps
// a credential of its own for an address, as a job's checkout does
// unless it is told not to, one whose origin holds a password, and one of
// no origin. Each says why, and how
// it is mended where a job can, and repeats nothing the checkout holds.
// The control is the checkout as it was cloned, which
// TestADaemonGivenACheckoutReleasesFromIt releases from.
func TestACheckoutThatCouldNotBePushedFromIsNotPlannedFrom(t *testing.T) {
	const secret = "c2VjcmV0LW9mLXRoZS10ZXN0"
	for name, c := range map[string]struct {
		plant func(t *testing.T, checkout string) string
		says  string
	}{
		"a credential of its own for an address": {func(t *testing.T, checkout string) string {
			commits.Git(t, checkout, "config", "http.https://github.com/.extraheader", "AUTHORIZATION: basic "+secret)
			return checkout
		}, "persist-credentials: false"},
		"a credential of its own in a file it includes": {func(t *testing.T, checkout string) string {
			kept := filepath.Join(t.TempDir(), "credentials")
			write(t, filepath.Dir(kept), "credentials", "[http \"https://github.com/\"]\n\textraheader = AUTHORIZATION: basic "+secret+"\n")
			commits.Git(t, checkout, "config", "include.path", kept)
			return checkout
		}, "persist-credentials: false"},
		"an origin with a password": {func(t *testing.T, checkout string) string {
			commits.Git(t, checkout, "config", "remote.origin.url", "https://x-access-token:"+secret+"@forge.example/acme/code")
			return checkout
		}, "written with a password"},
		"no origin": {func(t *testing.T, checkout string) string {
			commits.Git(t, checkout, "remote", "remove", "origin")
			return checkout
		}, "says no origin it was brought from"},
	} {
		t.Run(name, func(t *testing.T) {
			remote := releasingRemote(t, nil)
			checkout := filepath.Join(t.TempDir(), "checkout")
			commits.Git(t, filepath.Dir(checkout), "clone", "--quiet", "--branch", "release/1.0", "file://"+remote, checkout)
			given := c.plant(t, checkout)
			served := serveAs(t, configuration{Workspace: given, Mounted: true, Executor: scripted{}})
			s := &releaseService{graph: served.daemon.releases.graph, releases: served.daemon.releases}
			_, err := s.PlanRelease(context.Background(), releaseOf("greet"))
			if err == nil || !strings.Contains(err.Error(), c.says) {
				t.Errorf("a plan from it: %v, want it refused saying %q", err, c.says)
			}
			if err != nil && strings.Contains(err.Error(), secret) {
				t.Errorf("the refusal repeats what the checkout holds: %v", err)
			}
			if tagsOf(t, checkout) != "" || tagsOf(t, remote) != "" {
				t.Errorf("it left the tags %q here and %q there", tagsOf(t, checkout), tagsOf(t, remote))
			}
		})
	}
}

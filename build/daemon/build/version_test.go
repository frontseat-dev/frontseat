package build

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/testsupport/commits"
)

// noting is a grid that notes what each action was sent as, by its
// label, each time it was sent.
type noting struct {
	scripted
	mu   *sync.Mutex
	sent map[string][]string
}

func (n noting) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	n.mu.Lock()
	n.sent[action.Label] = append(n.sent[action.Label], strings.Join(action.Arguments, " "))
	n.mu.Unlock()
	return n.scripted.Execute(ctx, action)
}

// last is what the action of a label was last sent as.
func (n noting) last(t *testing.T, label string) string {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	sent := n.sent[label]
	if len(sent) == 0 {
		t.Fatalf("no action %s was sent; sent were %v", label, slices.Sorted(func(yield func(string) bool) {
			for each := range n.sent {
				if !yield(each) {
					return
				}
			}
		}))
	}
	return sent[len(sent)-1]
}

// versionedSource writes, into a remote of releasingClients, a Go
// plugin told which variables say a version, and an action of each
// entity's own that is given it.
func versionedSource(t *testing.T) func(remote string) {
	return func(remote string) {
		t.Helper()
		versionedInto(t, remote)
	}
}

func versionedInto(t *testing.T, remote string) {
	t.Helper()
	write(t, remote, "frontseat.yaml", "plugins:\n  go:\n    version: [\"example.com/greet.Version=version\", \"example.com/greet.Commit=commit\"]\n  backstage: {}\n  release: {}\n")
	for _, name := range []string{"greet", "log"} {
		write(t, remote, name+"/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: "+name+"\nspec:\n  type: library\n  lifecycle: production\n  owner: team\n  frontseat:\n    actions:\n      says:\n        command: [echo, \"{frontseat.version}\", \"{frontseat.tag}\", \"{frontseat.commit}\", \"{frontseat.unit}\"]\n        versioned: true\n")
	}
}

// What a release builds is given the version it is released at: a
// versioned action of a unit that moves is sent with what was planned
// for the unit, a tool no plugin knows (an entity's own action) and a
// plugin's tool (a Go module's programs) alike. The control is every
// other build, before the release and after it, which is sent the same
// each time, since what is kept of an evaluation is of no release; and
// the unit beside it, which the release does not move.
func TestWhatAReleaseBuildsIsGivenItsVersion(t *testing.T) {
	grid := noting{mu: &sync.Mutex{}, sent: map[string][]string{}}
	c, clone := releasingClients(t, grid, "release/1.0", versionedSource(t))
	s := &releaseService{graph: c.daemon.releases.graph, releases: c.daemon.releases}
	head := commits.Git(t, clone, "rev-parse", "HEAD")
	const says, compile = "//services/greet:custom.says", "//services/greet:go.compile"
	development := func(when string) {
		t.Helper()
		made, err := c.runs.CreateBuild(context.Background(), &buildv1.CreateBuildRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if ended := wait(t, c.runs, made.GetBuild().GetId()); ended.GetState() != buildv1.Build_STATE_SUCCEEDED || ended.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS {
			t.Fatalf("%s: a build ended %v", when, ended.GetState())
		}
		if sent := grid.last(t, says); !strings.HasSuffix(sent, "echo 0.0.0-dev   ") {
			t.Errorf("%s: a build that is no release sent %q", when, sent)
		}
		if sent := grid.last(t, compile); !strings.Contains(sent, "-ldflags=-X example.com/greet.Version=0.0.0-dev -X example.com/greet.Commit= ./...") {
			t.Errorf("%s: a build that is no release sent %q", when, sent)
		}
	}
	development("before the release")
	release := releaseEnded(t, s, releaseBegun(t, s, "greet").GetId())
	if release.GetState() != buildv1.Release_STATE_SUCCEEDED {
		t.Fatalf("the release ended %v (%s)", release.GetState(), release.GetFailure())
	}
	if sent, want := grid.last(t, says), "echo 1.0.0 greet/v1.0.0 "+head+" component:default/greet"; !strings.HasSuffix(sent, want) {
		t.Errorf("the release sent %q, want it to end %q", sent, want)
	}
	if sent, want := grid.last(t, compile), "-ldflags=-X example.com/greet.Version=1.0.0 -X example.com/greet.Commit="+head+" ./..."; !strings.Contains(sent, want) {
		t.Errorf("the release sent %q, want %q in it", sent, want)
	}
	// The unit beside it does not move, and nothing of it was built.
	grid.mu.Lock()
	for label, sent := range grid.sent {
		if strings.HasPrefix(label, "//services/log:") && strings.Contains(strings.Join(sent, "\n"), "1.0.0") {
			t.Errorf("%s, of a unit the release does not move, was sent %q", label, sent)
		}
	}
	grid.mu.Unlock()
	development("after the release")
}

// A prerelease is built at its version, as a release is, and is not
// tagged: a branch that is no release branch releases one, named by the
// branch and by when its commit was made, and what it builds says so.
// Nothing of it is in the repository afterwards, and the release has
// nothing left to publish. The control is the release of a release
// branch, which TestWhatAReleaseBuildsIsGivenItsVersion tags.
func TestAPrereleaseIsBuiltAtItsVersionAndNotTagged(t *testing.T) {
	grid := noting{mu: &sync.Mutex{}, sent: map[string][]string{}}
	c, clone := releasingClients(t, grid, "main", versionedSource(t))
	s := &releaseService{graph: c.daemon.releases.graph, releases: c.daemon.releases}
	head := commits.Git(t, clone, "rev-parse", "HEAD")
	committed, err := strconv.ParseInt(commits.Git(t, clone, "show", "--no-patch", "--format=%ct", "HEAD"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	version := "0.1.0-main-" + time.Unix(committed, 0).UTC().Format("20060102150405")
	release := releaseEnded(t, s, releaseBegun(t, s, "greet").GetId())
	if release.GetState() != buildv1.Release_STATE_SUCCEEDED || tagsOf(t, clone) != "" {
		t.Fatalf("the prerelease ended %v (%s) with the tags %q; want it made, and no tag", release.GetState(), release.GetFailure(), tagsOf(t, clone))
	}
	unit := release.GetPlan().GetUnits()[0]
	if !unit.GetPrerelease() || unit.GetNextVersion() != version || unit.GetTag() != "" || unit.GetChannel() != "main" {
		t.Errorf("it was planned as %q, a prerelease: %v, tagged %q, of the channel %q; want %q, untagged, of main", unit.GetNextVersion(), unit.GetPrerelease(), unit.GetTag(), unit.GetChannel(), version)
	}
	if made := release.GetUnits(); len(made) != 1 || made[0].GetVersion() != version || len(made[0].GetTags()) != 0 {
		t.Errorf("it made %v; want greet at %s and no tag", made, version)
	}
	if sent, want := grid.last(t, "//services/greet:custom.says"), "echo "+version+"  "+head+" component:default/greet"; !strings.HasSuffix(sent, want) {
		t.Errorf("the prerelease sent %q, want it to end %q", sent, want)
	}
	if sent, want := grid.last(t, "//services/greet:go.compile"), "-ldflags=-X example.com/greet.Version="+version+" -X example.com/greet.Commit="+head+" ./..."; !strings.Contains(sent, want) {
		t.Errorf("the prerelease sent %q, want %q in it", sent, want)
	}
}

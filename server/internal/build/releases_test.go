package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/tools/toolstest"
	"github.com/frontseat-dev/frontseat/server/testsupport/commits"
	"github.com/frontseat-dev/frontseat/server/testsupport/testpins"
)

// releasing is a daemon that holds one repository, brought from a remote
// on its branch release/1.0: two Go modules, greet and log, each a
// component that is released alone. It returns the daemon's release
// service and the directory of the daemon's clone.
func releasingDaemon(t *testing.T, executor exec.Executor) (*releaseService, *Daemon, string) {
	t.Helper()
	c, clone := releasingClients(t, executor, nil)
	graph := c.daemon.releases.graph
	return &releaseService{graph: graph, releases: c.daemon.releases}, c.daemon, clone
}

// releasingClients is what serves releasingDaemon's daemon, and the
// directory of its clone; written, where given, writes more into the
// remote before its first commit.
func releasingClients(t *testing.T, executor exec.Executor, written func(remote string)) (clients, string) {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "services")
	if err := os.MkdirAll(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	testpins.Go(t, remote)
	write(t, remote, "frontseat.yaml", "plugins:\n  go: {}\n  backstage: {}\n  release: {}\n")
	for _, name := range []string{"greet", "log"} {
		write(t, remote, name+"/go.mod", "module example.com/"+name+"\n\ngo 1.27\n")
		write(t, remote, name+"/"+name+".go", "package "+name+"\n")
		write(t, remote, name+"/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: "+name+"\nspec:\n  type: library\n  lifecycle: production\n  owner: team\n")
	}
	if written != nil {
		written(remote)
	}
	commits.Git(t, remote, "init", "--quiet", "--initial-branch", "main")
	commits.Git(t, remote, "add", "--all")
	commits.Git(t, remote, "commit", "--quiet", "-m", "feat: begin")
	commits.Git(t, remote, "branch", "release/1.0")
	workspace := t.TempDir()
	c := serveAs(t, configuration{Workspace: workspace, Executor: executor})
	populated(t, c, &workspacev1.WorkspaceSpec{Repositories: []*workspacev1.RepositorySource{{Remote: remote, Ref: "release/1.0"}}})
	return c, filepath.Join(workspace, "services")
}

func releaseOf(names ...string) *buildv1.PlanReleaseRequest {
	request := &buildv1.PlanReleaseRequest{}
	for _, name := range names {
		request.Entities = append(request.Entities, entity.Proto(entity.New("", "", name)))
	}
	return request
}

// begun plans and begins a release, and returns it as it was made.
func releaseBegun(t *testing.T, s *releaseService, names ...string) *buildv1.Release {
	t.Helper()
	ctx := context.Background()
	planned, err := s.PlanRelease(ctx, releaseOf(names...))
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateRelease(ctx, &buildv1.CreateReleaseRequest{Entities: releaseOf(names...).GetEntities(), Etag: planned.GetPlan().GetEtag()})
	if err != nil {
		t.Fatal(err)
	}
	return created.GetRelease()
}

func releaseEnded(t *testing.T, s *releaseService, id string) *buildv1.Release {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	got, err := s.WaitRelease(ctx, &buildv1.WaitReleaseRequest{ReleaseId: id})
	if err != nil {
		t.Fatalf("the release %s did not end: %v", id, err)
	}
	return got.GetRelease()
}

func tagsOf(t *testing.T, clone string) string {
	t.Helper()
	return strings.Join(strings.Fields(commits.Git(t, clone, "tag", "--list")), " ")
}

func TestAReleaseBuildsThenTagsWhatMoves(t *testing.T) {
	s, _, clone := releasingDaemon(t, scripted{})
	begun := releaseBegun(t, s, "greet")
	release := releaseEnded(t, s, begun.GetId())
	// The build is named by the release as it is made: whoever made it
	// follows the build without asking the release again.
	if begun.GetBuildId() == "" || begun.GetBuildId() != release.GetBuildId() {
		t.Errorf("the release was made with the run %q and ended with %q; want the build's run from the first", begun.GetBuildId(), release.GetBuildId())
	}
	if release.GetState() != buildv1.Release_STATE_UNPUBLISHED || release.GetBuildId() == "" || tagsOf(t, clone) != "greet/v1.0.0" {
		t.Fatalf("the release ended %v (%s), run %q, tags %q", release.GetState(), release.GetFailure(), release.GetBuildId(), tagsOf(t, clone))
	}
	if at := commits.Git(t, clone, "rev-parse", "greet/v1.0.0^{commit}"); at != commits.Git(t, clone, "rev-parse", "HEAD") || release.GetUnits()[0].GetTags()[0].GetCommit() != at {
		t.Errorf("the tag names %s", at)
	}
}

// A unit whose build fails is not tagged, and the control beside it,
// whose build passes, is.
func TestABuildThatFailsTagsNothing(t *testing.T) {
	s, _, clone := releasingDaemon(t, scripted{fails: []string{"greet"}})
	failed := releaseEnded(t, s, releaseBegun(t, s, "greet").GetId())
	if failed.GetState() != buildv1.Release_STATE_FAILED || !strings.Contains(failed.GetFailure(), "the production build "+failed.GetBuildId()) || tagsOf(t, clone) != "" {
		t.Errorf("a release whose build failed ended %v (%s) with the tags %q", failed.GetState(), failed.GetFailure(), tagsOf(t, clone))
	}
	if passed := releaseEnded(t, s, releaseBegun(t, s, "log").GetId()); passed.GetState() != buildv1.Release_STATE_UNPUBLISHED || tagsOf(t, clone) != "log/v1.0.0" {
		t.Errorf("the control ended %v (%s) with the tags %q", passed.GetState(), passed.GetFailure(), tagsOf(t, clone))
	}
}

// A daemon makes one release at a time: the same plan asked again is the
// release being made, and another plan is refused until it has ended.
func TestADaemonMakesOneReleaseAtATime(t *testing.T) {
	gate := make(chan struct{})
	s, _, clone := releasingDaemon(t, scripted{gate: gate})
	first := releaseBegun(t, s, "greet")
	if first.GetState() != buildv1.Release_STATE_BUILDING {
		t.Fatalf("a release begins %v", first.GetState())
	}
	// Asked several times at once, it is one release.
	ids := make(chan string, 8)
	for range 8 {
		go func() { ids <- releaseBegun(t, s, "greet").GetId() }()
	}
	for range 8 {
		if id := <-ids; id != first.GetId() {
			t.Errorf("the same plan asked again made another release, %s beside %s", id, first.GetId())
		}
	}
	planned, err := s.PlanRelease(context.Background(), releaseOf("log"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateRelease(context.Background(), &buildv1.CreateReleaseRequest{Entities: releaseOf("log").GetEntities(), Etag: planned.GetPlan().GetEtag()})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), first.GetId()+" is being made") {
		t.Errorf("another plan, while one is released: %v", err)
	}
	if tagsOf(t, clone) != "" {
		t.Errorf("a release that has not built wrote the tags %q", tagsOf(t, clone))
	}
	close(gate)
	if release := releaseEnded(t, s, first.GetId()); release.GetState() != buildv1.Release_STATE_UNPUBLISHED {
		t.Fatalf("the release ended %v: %s", release.GetState(), release.GetFailure())
	}
	if after := releaseEnded(t, s, releaseBegun(t, s, "log").GetId()); after.GetState() != buildv1.Release_STATE_UNPUBLISHED || tagsOf(t, clone) != "greet/v1.0.0 log/v1.0.0" {
		t.Errorf("the next release ended %v (%s) with the tags %q", after.GetState(), after.GetFailure(), tagsOf(t, clone))
	}
}

// A daemon that ends leaves no release going, and one it ended while it
// built is tagged nothing.
func TestADaemonThatEndsLeavesNoReleaseGoing(t *testing.T) {
	s, daemon, clone := releasingDaemon(t, scripted{hang: true})
	going := releaseBegun(t, s, "greet")
	closed := make(chan struct{})
	go func() { daemon.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(30 * time.Second):
		t.Fatal("the daemon did not end while a release was building")
	}
	release := releaseEnded(t, s, going.GetId())
	if release.GetState() != buildv1.Release_STATE_FAILED || tagsOf(t, clone) != "" {
		t.Errorf("a release the daemon ended is %v (%s) with the tags %q", release.GetState(), release.GetFailure(), tagsOf(t, clone))
	}
	if _, err := s.CreateRelease(context.Background(), &buildv1.CreateReleaseRequest{Entities: releaseOf("greet").GetEntities(), Etag: going.GetPlan().GetEtag()}); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("a daemon that has ended made a release: %v", err)
	}
}

// entering is an executor that says when the first action of a build
// reaches it, past the reading of its inputs, and holds every action at
// the gate, as scripted does, until the test lets it go on.
type entering struct {
	scripted
	once    *sync.Once
	entered chan struct{}
}

func (e entering) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	if !toolstest.Asks(action) && !installs(action) {
		e.once.Do(func() { close(e.entered) })
	}
	return e.scripted.Execute(ctx, action)
}

// What was built is what is tagged: a repository that moved while its
// units were built is tagged nothing. The order is the test's: the
// repository moves once the production build holds an action at the
// executor, and the build goes on after. A commit that changes no file
// the build reads moves the repository alone, and the release refuses
// it at tagging, saying so; one that changes a file the build reads is
// refused too, by the build or at tagging, whichever sees it first. The
// control is a release nothing moved under, which tags
// (TestAReleaseBuildsThenTagsWhatMoves).
func TestASourceThatMovedWhileItWasBuiltIsNotTagged(t *testing.T) {
	for name, moved := range map[string]func(t *testing.T, clone string){
		"by a commit of no file": func(t *testing.T, clone string) {
			commits.Git(t, clone, "commit", "--quiet", "--allow-empty", "-m", "chore: later")
		},
		"by a commit of a file it builds": func(t *testing.T, clone string) {
			write(t, clone, "greet/greet.go", "package greet\n\n// Later.\n")
			commits.Git(t, clone, "commit", "--quiet", "--all", "-m", "fix: later")
		},
	} {
		t.Run(name, func(t *testing.T) {
			gate, entered := make(chan struct{}), make(chan struct{})
			s, d, clone := releasingDaemon(t, entering{scripted: scripted{gate: gate}, once: &sync.Once{}, entered: entered})
			going := releaseBegun(t, s, "greet")
			select {
			case <-entered:
			case <-time.After(time.Minute):
				t.Fatal("the production build never reached the executor")
			}
			moved(t, clone)
			close(gate)
			release := releaseEnded(t, s, going.GetId())
			if release.GetState() != buildv1.Release_STATE_FAILED || tagsOf(t, clone) != "" {
				t.Fatalf("a release whose source moved ended %v (%s) with the tags %q", release.GetState(), release.GetFailure(), tagsOf(t, clone))
			}
			if name == "by a commit of no file" && !strings.Contains(release.GetFailure(), "is no longer the commit") {
				built, err := (&runService{runs: d.runs}).DescribeBuild(context.Background(), &buildv1.DescribeBuildRequest{BuildId: release.GetBuildId()})
				t.Errorf("a release whose repository alone moved failed %q (its build: %v, %v), want it refused at tagging", release.GetFailure(), built.GetBuild().GetFailure(), err)
			}
		})
	}
}

// A plan of several units has the same etag once it is carried out, and
// carried out again it builds nothing and says each was there already.
func TestAPlanOfSeveralUnitsIsCompletedByReleasingItAgain(t *testing.T) {
	s, _, clone := releasingDaemon(t, scripted{})
	first := releaseEnded(t, s, releaseBegun(t, s, "greet", "log").GetId())
	if first.GetState() != buildv1.Release_STATE_UNPUBLISHED || tagsOf(t, clone) != "greet/v1.0.0 log/v1.0.0" {
		t.Fatalf("the release ended %v (%s) with the tags %q", first.GetState(), first.GetFailure(), tagsOf(t, clone))
	}
	again := releaseEnded(t, s, releaseBegun(t, s, "greet", "log").GetId())
	if again.GetPlan().GetEtag() != first.GetPlan().GetEtag() {
		t.Error("the plan has another etag once it is carried out")
	}
	if again.GetBuildId() != "" || len(again.GetUnits()) != 2 || !again.GetUnits()[0].GetAlready() || !again.GetUnits()[1].GetAlready() {
		t.Errorf("released again it is %v", again)
	}
}

// What a build reads that is not of the commit is refused: a file that
// is not committed, and a file git is told to ignore, unless frontseat
// is told to ignore it too.
func TestWhatABuildReadsThatIsNotCommittedIsRefused(t *testing.T) {
	s, _, clone := releasingDaemon(t, scripted{})
	planned, err := s.PlanRelease(context.Background(), releaseOf("greet"))
	if err != nil {
		t.Fatal(err)
	}
	create := func() error {
		// The plan is planned again each time: an ignore file that is
		// committed moves the commit.
		planned, err = s.PlanRelease(context.Background(), releaseOf("greet"))
		if err != nil {
			t.Fatal(err)
		}
		_, err := s.CreateRelease(context.Background(), &buildv1.CreateReleaseRequest{Entities: releaseOf("greet").GetEntities(), Etag: planned.GetPlan().GetEtag()})
		return err
	}
	write(t, clone, "greet/new.go", "package greet\n")
	if err := create(); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "greet/new.go") {
		t.Errorf("a file that is not committed: %v", err)
	}
	if err := os.Remove(filepath.Join(clone, "greet/new.go")); err != nil {
		t.Fatal(err)
	}
	// A file git ignores is read by a build all the same.
	write(t, clone, ".git/info/exclude", "gen.go\n")
	write(t, clone, "greet/gen.go", "package greet\n")
	if err := create(); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "greet/gen.go") || !strings.Contains(err.Error(), ".frontseatignore") {
		t.Errorf("a file git ignores that a build reads: %v", err)
	}
	// Named in the repository's own ignore file, it is no part of a run.
	write(t, clone, ".frontseatignore", "gen.go\n")
	commits.Git(t, clone, "add", ".frontseatignore")
	commits.Git(t, clone, "commit", "--quiet", "-m", "chore: ignore what is generated")
	if err := create(); err != nil {
		t.Errorf("a file frontseat is told to ignore refused the release: %v", err)
	}
}

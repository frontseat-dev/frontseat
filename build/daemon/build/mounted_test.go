package build

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	connect "connectrpc.com/connect/v2"

	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	"github.com/frontseat-dev/frontseat/common/record"
	"github.com/frontseat-dev/frontseat/common/record/manifest"
	"github.com/frontseat-dev/frontseat/core/refusal"
	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/testsupport/testpins"
)

// mountedBeside lays out, in a daemon's directory, a repository as
// whatever mounts one for a workspace leaves it (Mounted, in
// core/ports), and records it in the directory's manifest as mounted,
// at a changeset, beside what is recorded there, with the one entity in
// it: the repository stored, a Go module that is the component hello,
// and its layer, beneath layers, with a stamp. The layer is a directory
// a test may write, and not one mounted read-only.
func mountedBeside(t *testing.T, workspace, layers string) {
	t.Helper()
	stored := filepath.Join(workspace, "stored")
	write(t, stored, "frontseat.yaml", "plugins:\n  go: {}\n  backstage: {}\n")
	write(t, stored, "hello/go.mod", "module example.com/hello\n\ngo 1.27\n")
	write(t, stored, "hello/main.go", "package main\n\nfunc main() {}\n")
	write(t, stored, "hello/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: hello\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n")
	testpins.Go(t, stored)
	write(t, layers, "stored/stamp", "2026-11-24T10:00:00.123456789Z\n")
	held, _, err := manifest.Read(workspace, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	held.Repositories = append(held.Repositories, record.Repository{Name: "stored", Commit: "changeset-9", Mounted: true})
	held.Known = append(held.Known, manifest.Entity{Ref: component("hello", "stored").Ref, Repository: "stored", Root: "hello"})
	if err := manifest.Write(workspace, held); err != nil {
		t.Fatal(err)
	}
}

// A build of a workspace that holds a clone and a repository something
// else mounted reads both: the mounted one is a repository of the
// workspace as its manifest records it, listed and read as a clone is,
// an entity in it is built, and where a commit is asked of it the
// changeset it stands at is said. Its layer is read where the daemon
// was told where layers are, and says a stamp only where it is mounted
// read-only, which a test's directory is not: so every file of it is
// read, and taken to hold what it holds. The control is the workspace before the
// mounted repository is recorded, which builds the clone alone.
func TestABuildOfAWorkspaceReadsAMountedRepositoryBesideAClone(t *testing.T) {
	layers := t.TempDir()
	c := serveAs(t, configuration{Workspace: t.TempDir(), Executor: scripted{}, Layers: layers})
	workspace := brought(t, c, nil, record.Checkout{Name: "services", Remote: releasingRemote(t, nil), Ref: "main"})
	if labels := planned(t, c); !strings.Contains(labels, "//services/greet:go.compile") || strings.Contains(labels, "//stored/") {
		t.Fatalf("the control, the clone alone, plans %q; want the clone's modules and nothing of a repository not recorded", labels)
	}
	mountedBeside(t, workspace, layers)
	if labels := planned(t, c); !strings.Contains(labels, "//services/greet:go.compile") || !strings.Contains(labels, "//stored/hello:go.compile") {
		t.Fatalf("a build plans %q; want the modules of the clone and of the mounted repository", labels)
	}

	tree, entities, err := sourceOf(c.daemon.runs.workspace, layers)
	if err != nil {
		t.Fatal(err)
	}
	if mount, mounted := tree.Mounted["stored"]; !mounted || mount.Stamped || len(tree.Mounted) != 1 || tree.Paths["stored"] != "stored" || tree.Paths["services"] != "services" || len(entities) != 1 {
		t.Errorf("the run's tree is %+v with the entities %v; want both repositories, the stored one alone said to be mounted, and no stamp taken from a layer a test may write", tree, entities)
	}
	// As a daemon that knows nothing of the file yet takes it.
	run, err := (&source.Keeper{}).Take(context.Background(), tree)
	if err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(workspace, "stored", "hello", "main.go")
	if hash, _, _, err := run.File(main); err != nil || hash != digestOf("package main\n\nfunc main() {}\n").GetHash() || run.Hashed() != 1 {
		t.Errorf("the mounted repository's file is taken to hold %s, %d files read (%v); want what it holds, read", hash, run.Hashed(), err)
	}

	ctx := context.Background()
	built, err := c.runs.CreateBuild(ctx, &buildv1.CreateBuildRequest{Targets: []string{"component:default/hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if ended := wait(t, c.runs, built.GetBuild().GetId()); ended.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS || ended.GetCounts().GetPassed() == 0 {
		t.Errorf("the build of the entity in the mounted repository ended %s with %v (%s); want it built", ended.GetExitCode(), ended.GetCounts(), ended.GetFailure())
	}
	listed, err := c.graph.ListEntities(ctx, &buildv1.ListEntitiesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	commits := map[string]string{}
	for _, each := range listed.GetEntities() {
		commits[each.GetRef().GetName()] = each.GetCommit()
	}
	if commits["hello"] != "changeset-9" || len(commits["greet"]) != 40 {
		t.Errorf("the entities stand at %v; want hello at its repository's changeset and greet at its clone's commit", commits)
	}
}

// A workspace that holds a repository something else mounted releases
// nothing: a plan is of every repository's commits and tags them, and a
// mounted repository has neither. Planning and releasing are refused by
// name, UNIMPLEMENTED and RELEASE_NOT_SERVED, naming the repository,
// before anything is asked of a history, built or tagged. The control
// is the same workspace before the mounted repository is recorded,
// which plans the release of the clone's unit.
func TestAReleaseFromAWorkspaceThatHoldsAMountedRepositoryIsRefusedByName(t *testing.T) {
	c, clone := releasingClients(t, scripted{}, "release/1.0", nil)
	s := &releaseService{graph: c.daemon.releases.graph, releases: c.daemon.releases}
	ctx := context.Background()
	planned, err := s.PlanRelease(ctx, releaseOf("greet"))
	if err != nil || len(planned.GetPlan().GetUnits()) != 1 {
		t.Fatalf("the control, a workspace of clones: planned %v (%v); want the unit planned", planned.GetPlan(), err)
	}
	mountedBeside(t, filepath.Dir(clone), t.TempDir())

	refused := func(what string, err error) {
		t.Helper()
		if reason, domain := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeUnimplemented || reason != "RELEASE_NOT_SERVED" || domain != refusal.Build || !strings.Contains(err.Error(), "the repository stored mounted") {
			t.Errorf("%s from a workspace that holds a mounted repository: %v (%s, %s), want UNIMPLEMENTED, RELEASE_NOT_SERVED, naming the repository", what, err, reason, domain)
		}
	}
	_, err = s.PlanRelease(ctx, releaseOf("greet"))
	refused("a plan", err)
	_, err = s.CreateRelease(ctx, &buildv1.CreateReleaseRequest{Entities: releaseOf("greet").GetEntities(), Etag: planned.GetPlan().GetEtag()})
	refused("a release", err)
	if listed, err := c.runs.ListBuilds(ctx, &buildv1.ListBuildsRequest{}); err != nil || len(listed.GetBuilds()) != 0 {
		t.Errorf("the refused release built %v (%v)", listed.GetBuilds(), err)
	}
	if tagsOf(t, clone) != "" || exists(filepath.Dir(clone), "stored", ".git") {
		t.Errorf("the refused release left the tags %q, or a version control system's directory in the mounted repository", tagsOf(t, clone))
	}
}

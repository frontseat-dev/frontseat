package build

import (
	"context"
	"strings"
	"testing"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
)

// leaving is a grid that leaves each output an action asks for as a
// file, of a digest that says which output it is.
type leaving struct{ scripted }

func (l leaving) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	result, err := l.scripted.Execute(ctx, action)
	if err != nil || installs(action) {
		return result, err
	}
	for output := range result.Outputs {
		// What is named a tree is left as a directory, whatever its
		// action said it would be.
		result.Outputs[output] = exec.Held{Digest: exec.Digest{Hash: strings.Repeat("a", 64-len(output)%10) + strings.Repeat("b", len(output)%10), Size: int64(len(output))}, Directory: strings.HasSuffix(output, "tree")}
	}
	return result, nil
}

// packagedSource writes, into a remote of releasingClients, an action of
// each entity's own that makes what it ships, and one beside it that is
// no package.
func packagedSource(t *testing.T, twice bool) func(remote string) {
	return func(remote string) {
		t.Helper()
		for _, name := range []string{"greet", "log"} {
			left := name
			if twice {
				left = "same"
			}
			write(t, remote, name+"/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: "+name+"\nspec:\n  type: library\n  lifecycle: production\n  owner: team\n  frontseat:\n    actions:\n      pack:\n        command: [tool, pack]\n        outputs: [out/"+left+"-linux-amd64, out/"+left+".tgz]\n        kind: package\n      notes:\n        command: [tool, notes]\n        outputs: [out/notes.txt]\n")
		}
	}
}

// A release lists what it built to ship: each file a package of a unit
// that moves left, by its own name, with the sha256 and the size the
// grid holds of it and the action that left it. What an action that is
// no package left is none of it, nor what a unit the release does not
// move would ship.
func TestAReleaseListsWhatItsPackagesLeft(t *testing.T) {
	c, _ := releasingClients(t, leaving{}, "release/1.0", packagedSource(t, false))
	_, release := releasedAlone(t, c, "greet")
	var listed []string
	for _, each := range release.GetUnits()[0].GetArtifacts() {
		if len(each.GetSha256()) != 64 || each.GetSize() == 0 || each.GetLabel() != "//services/greet:custom.pack" {
			t.Errorf("an artifact is listed as %v", each)
		}
		listed = append(listed, each.GetName())
	}
	if got := strings.Join(listed, " "); got != "greet-linux-amd64 greet.tgz" {
		t.Errorf("the release lists %q; want greet's two files, in the order of their names, and nothing of log or of what is no package", got)
	}
}

// Two packages of a release that leave a file of one name cannot both
// be shipped: the release fails saying which, and tags nothing. The
// control is each of them released alone, where the name is its own.
func TestTwoArtifactsOfOneNameFailTheReleaseBeforeItTags(t *testing.T) {
	c, clone := releasingClients(t, leaving{}, "release/1.0", func(remote string) {
		write(t, remote, "catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: System\nmetadata:\n  name: both\nspec:\n  owner: team\n  frontseat:\n    release:\n      grouping: independent\n")
		for _, name := range []string{"greet", "log"} {
			write(t, remote, name+"/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: "+name+"\nspec:\n  type: library\n  lifecycle: production\n  owner: team\n  system: both\n  frontseat:\n    actions:\n      pack:\n        command: [tool, pack]\n        outputs: [out/same.tgz]\n        kind: package\n")
		}
	})
	s := &releaseService{graph: c.daemon.releases.graph, releases: c.daemon.releases}
	ctx := context.Background()
	both := &buildv1.PlanReleaseRequest{Entities: []*typev1.EntityRef{entity.Proto(entity.New("system", "", "both"))}}
	planned, err := s.PlanRelease(ctx, both)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateRelease(ctx, &buildv1.CreateReleaseRequest{Entities: both.GetEntities(), Etag: planned.GetPlan().GetEtag()})
	if err != nil {
		t.Fatal(err)
	}
	together := releaseEnded(t, s, created.GetRelease().GetId())
	if together.GetState() != buildv1.Release_STATE_FAILED || !strings.Contains(together.GetFailure(), "are each left as an artifact named same.tgz, by //services/greet:custom.pack and //services/log:custom.pack; nothing is tagged") || tagsOf(t, clone) != "" {
		t.Errorf("a release of two artifacts of one name ended %v (%s) with the tags %q", together.GetState(), together.GetFailure(), tagsOf(t, clone))
	}
	alone := releaseEnded(t, s, releaseBegun(t, s, "greet").GetId())
	if alone.GetState() != buildv1.Release_STATE_SUCCEEDED || tagsOf(t, clone) != "greet/v1.0.0" {
		t.Errorf("the control ended %v (%s) with the tags %q", alone.GetState(), alone.GetFailure(), tagsOf(t, clone))
	}
}

// What an entity says it ships for reaches the plugin that builds it:
// a release of a Go module with a program builds that program for each
// platform its entity names, and lists each as a file of its own. The
// control is the module beside it, whose entity names none.
func TestAReleaseBuildsAProgramForEachPlatformItsEntityShipsFor(t *testing.T) {
	c, _ := releasingClients(t, leaving{}, "release/1.0", func(remote string) {
		for _, name := range []string{"greet", "log"} {
			write(t, remote, name+"/cmd/hi/main.go", "package main\n\nfunc main() {}\n")
		}
		write(t, remote, "greet/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: greet\nspec:\n  type: library\n  lifecycle: production\n  owner: team\n  frontseat:\n    platforms: [linux/amd64, darwin/arm64]\n")
	})
	s := &releaseService{graph: c.daemon.releases.graph, releases: c.daemon.releases}
	for unit, want := range map[string]string{
		"greet": "hi-darwin-arm64=//services/greet:go.package/hi-darwin-arm64 hi-linux-amd64=//services/greet:go.package/hi-linux-amd64",
		"log":   "",
	} {
		release := releaseEnded(t, s, releaseBegun(t, s, unit).GetId())
		if release.GetState() != buildv1.Release_STATE_SUCCEEDED || len(release.GetUnits()) != 1 {
			built, _ := c.runs.DescribeBuild(context.Background(), &buildv1.DescribeBuildRequest{BuildId: release.GetBuildId()})
			t.Fatalf("the release of %s ended %v (%s) with %v; its build: %s", unit, release.GetState(), release.GetFailure(), release.GetUnits(), built.GetBuild().GetFailure())
		}
		var listed []string
		for _, each := range release.GetUnits()[0].GetArtifacts() {
			listed = append(listed, each.GetName()+"="+each.GetLabel())
		}
		if got := strings.Join(listed, " "); got != want {
			t.Errorf("the release of %s lists %q, want %q", unit, got, want)
		}
	}
}

// What ships is a file, known by its own name: a package that leaves
// two files of one name, and one that leaves a directory where it said
// a file, each fail the release, which says which and tags nothing.
// The control is the package beside them, released alone.
func TestAPackageThatLeavesWhatCannotBeShippedFailsTheRelease(t *testing.T) {
	pack := func(name, outputs string) string {
		return "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: " + name + "\nspec:\n  type: library\n  lifecycle: production\n  owner: team\n  frontseat:\n    actions:\n      pack:\n        command: [tool, pack]\n        outputs: " + outputs + "\n        kind: package\n"
	}
	for name, c := range map[string]struct {
		outputs string
		says    string
	}{
		"two files of one name": {"[out/linux/tool, out/darwin/tool]", "are each left as an artifact named tool, by //services/greet:custom.pack and //services/greet:custom.pack; nothing is tagged"},
		"a directory":           {"[out/tree]", "//services/greet:custom.pack left the directory services/greet/out/tree, and a package leaves files; nothing is tagged"},
	} {
		c2, clone := releasingClients(t, leaving{}, "release/1.0", func(remote string) {
			write(t, remote, "greet/catalog-info.yaml", pack("greet", c.outputs))
			write(t, remote, "log/catalog-info.yaml", pack("log", "[out/log.tgz]"))
		})
		s := &releaseService{graph: c2.daemon.releases.graph, releases: c2.daemon.releases}
		failed := releaseEnded(t, s, releaseBegun(t, s, "greet").GetId())
		if failed.GetState() != buildv1.Release_STATE_FAILED || !strings.Contains(failed.GetFailure(), c.says) || tagsOf(t, clone) != "" {
			t.Errorf("%s: the release ended %v (%s) with the tags %q; want %q", name, failed.GetState(), failed.GetFailure(), tagsOf(t, clone), c.says)
		}
		if control := releaseEnded(t, s, releaseBegun(t, s, "log").GetId()); control.GetState() != buildv1.Release_STATE_SUCCEEDED {
			t.Errorf("%s: the control ended %v (%s)", name, control.GetState(), control.GetFailure())
		}
	}
}

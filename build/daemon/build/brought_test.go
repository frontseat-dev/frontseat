package build

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/common/record"
	"github.com/frontseat-dev/frontseat/common/record/manifest"
	"github.com/frontseat-dev/frontseat/common/vcs"
	"github.com/frontseat-dev/frontseat/common/vcs/chosen"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/testsupport/testforge"
)

// brought fills a daemon's directory as what fills a workspace leaves
// it: each repository cloned by the version control system the build
// reads with, whole or with the directories taken of it, and recorded in
// the directory's manifest once it is there, with the entities known in
// them. Nothing of what fills a workspace runs: a build is of its
// directory, whoever filled it. It returns the directory.
func brought(t testing.TB, c clients, known []record.Located, checkouts ...record.Checkout) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	workspace := c.daemon.runs.workspace.Root()
	recorded := manifest.Manifest{}
	for _, each := range checkouts {
		revision, err := chosen.System().Clone(ctx, filepath.Join(workspace, each.Name), vcs.Source{Remote: each.Remote, Ref: each.Ref, Roots: each.Roots})
		if err != nil {
			t.Fatalf("bringing %s: %v", each.Name, err)
		}
		recorded.Repositories = append(recorded.Repositories, record.Repository{Name: each.Name, Remote: each.Remote, Ref: each.Ref, Commit: revision, Roots: each.Roots})
	}
	for _, each := range known {
		recorded.Known = append(recorded.Known, manifest.Entity{Ref: each.Ref, Repository: each.Repository, Root: each.Root})
	}
	if err := manifest.Write(workspace, recorded); err != nil {
		t.Fatal(err)
	}
	return workspace
}

// component is a component of the default namespace, in a directory of
// its name in a repository.
func component(name, repository string) record.Located {
	return record.Located{Ref: entity.Ref{Kind: "component", Namespace: "default", Name: name}, Repository: repository, Root: name}
}

// taken is a forge's repository, taken whole on its main branch.
func taken(forge testforge.Forge, name string) record.Checkout {
	return record.Checkout{Name: name, Remote: forge.Repository(name), Ref: "main"}
}

// broughtGateway fills a daemon's directory with what a workspace asked
// for the entity gateway holds: gateway, of the forge's platform, and
// greet, of its services, which gateway depends on, and of each
// repository those directories alone.
func broughtGateway(t testing.TB, c clients, forge testforge.Forge) string {
	t.Helper()
	platform, services := taken(forge, "platform"), taken(forge, "services")
	platform.Roots, services.Roots = []string{"gateway"}, []string{"greet"}
	return brought(t, c, []record.Located{component("gateway", "platform"), component("greet", "services")}, platform, services)
}

// ranGateway fills a daemon's directory with gateway and what it depends
// on, and returns a build of it as it ended.
func ranGateway(t *testing.T, c clients, forge testforge.Forge) *buildv1.Build {
	t.Helper()
	broughtGateway(t, c, forge)
	return wait(t, c.runs, create(t, c.runs).GetId())
}

// exists is whether a path, of parts joined, is there.
func exists(parts ...string) bool {
	_, err := os.Stat(filepath.Join(parts...))
	return err == nil
}

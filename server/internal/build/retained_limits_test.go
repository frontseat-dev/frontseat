package build

import (
	"fmt"
	"testing"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/testsupport/testpins"
)

// What a daemon keeps (retained.go) holds a workspace of a thousand Go
// modules, each a Component: asked twice what there is to build, it is
// evaluated once, and what is kept of it is within both limits. A session
// that went over would have dropped what it derived and evaluated again,
// so limits lowered beneath what such a workspace needs fail this.
func TestAThousandModulesAreKeptWithinWhatADaemonKeeps(t *testing.T) {
	const modules = 1000
	workspace := t.TempDir()
	testpins.Go(t, workspace)
	for i := range modules {
		name := fmt.Sprintf("m%04d", i)
		mod := fmt.Sprintf("module example.com/%s\n\ngo 1.27\n", name)
		if first := i - i%10; first != i {
			mod += fmt.Sprintf("\nrequire example.com/m%04d v0.0.0\n", first)
		}
		write(t, workspace, name+"/go.mod", mod)
		write(t, workspace, name+"/main.go", "package main\n\nfunc main() {}\n")
		write(t, workspace, name+"/catalog-info.yaml", fmt.Sprintf("apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: %s\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n", name))
	}
	c, graph := serveWithGraph(t, configuration{Workspace: workspace, Mounted: true, Executor: scripted{}})
	for range 2 {
		got, err := graph.ListProjects(t.Context(), &buildv1.ListProjectsRequest{PageSize: modules})
		if err != nil || len(got.GetProjects()) != modules {
			t.Fatalf("%d projects, %v", len(got.GetProjects()), err)
		}
	}
	stats, err := c.daemon.graph.session.Stats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d modules: %d records and %d bytes kept, of %d and %d allowed", modules, stats.Records, stats.WireBytes, retainedRecords, retainedWireBytes)
	if stats.Evaluations != 1 {
		t.Errorf("asked twice, the workspace was evaluated %d times: what was kept of it was dropped", stats.Evaluations)
	}
	// More than a record and a byte for each module, or nothing was kept
	// that the limits could hold.
	if stats.Records < modules || stats.Records > retainedRecords || stats.WireBytes < modules || stats.WireBytes > retainedWireBytes {
		t.Errorf("%d records and %d bytes are kept, of %d and %d allowed", stats.Records, stats.WireBytes, retainedRecords, retainedWireBytes)
	}
}

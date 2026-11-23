package build

import (
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/catalog/catalogtest"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"google.golang.org/protobuf/proto"
)

// Exercises the project list through the Connect adapter. This
// also verifies that retained graph facts do not introduce build planning.
func TestProjectProjectionServesExistingGraphContract(t *testing.T) {
	root := oneModule(t)
	write(t, root, "hello/catalog-info.yaml", catalogtest.Component("hello", "plugins:\n  go:\n    tags: [owned]\n"+catalogtest.Cycle))
	c, graph := serveWithGraph(t, configuration{Workspace: root, Mounted: true, Executor: scripted{}})
	got, err := graph.ListProjects(t.Context(), &buildv1.ListProjectsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var project *buildv1.Project
	for _, p := range got.GetProjects() {
		if p.GetRef().GetPlugin() == "go" {
			project = p
		}
	}
	if project == nil || project.GetRef().GetRoot() != "hello" || project.GetManifest() != "hello/go.mod" || project.GetOwner().GetName() != "hello" {
		t.Fatalf("missing project identity or owner: %v", project)
	}
	if len(project.GetProvides()) != 1 || project.GetProvides()[0] != "go:example.com/hello" || len(project.GetOptions()) == 0 {
		t.Fatalf("missing provides or options: %v", project)
	}
	// Where an option is set is a location of the workspace's source: the
	// file of the repository that sets it; one nothing sets has none.
	for _, option := range project.GetOptions() {
		switch at := option.GetOrigin(); {
		case option.GetName() == "tags" && (at.GetPath() != "hello/catalog-info.yaml" || at.GetLine() != 0):
			t.Errorf("tags is said to be set at %v, want hello/catalog-info.yaml", at)
		case option.GetName() != "tags" && at != nil:
			t.Errorf("%s, which nothing sets, is said to be set at %v", option.GetName(), at)
		}
	}
	if _, err := graph.ListEntities(t.Context(), &buildv1.ListEntitiesRequest{}); err != nil {
		t.Fatal(err)
	}
	stats := sessionStats(t, c)
	if stats.Plans != 0 {
		t.Fatal("graph request planned a build")
	}
	want := proto.CloneOf(got)
	project.Ref.Root = "caller mutation"
	project.Owner.Name = "caller mutation"
	project.Options[0].Name = "caller mutation"
	next, err := graph.ListProjects(t.Context(), &buildv1.ListProjectsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(next, want) {
		t.Fatal("project query changed across equivalent calls")
	}
}

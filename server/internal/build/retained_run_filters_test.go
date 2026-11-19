package build

import (
	"strings"
	"testing"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/engine/catalog/catalogtest"
)

func TestRetainedRunFiltersAndGlobalRefusalThroughAPI(t *testing.T) {
	root := oneModule(t)
	c, g := serveWithGraph(t, configuration{Workspace: root, Mounted: true, Executor: scripted{}})
	if _, err := g.ListProjects(t.Context(), &buildv1.ListProjectsRequest{}); err != nil {
		t.Fatal(err)
	}
	for _, only := range [][]string{{"compile"}, {"format"}, {"compile", "compile"}, nil, {"unknown"}, {"lint"}} {
		made, err := c.runs.CreateBuild(t.Context(), &buildv1.CreateBuildRequest{Targets: []string{"hello/"}, Only: only})
		if err != nil {
			t.Fatal(err)
		}
		got := wait(t, c.runs, made.Build.Id)
		listed := actions(t, c, got)
		if len(only) > 0 && only[0] == "unknown" {
			if got.GetExitCode().GetName() != "BUILD_FAILURE" || len(listed) != 0 {
				t.Fatalf("unknown filter accepted or executed actions: %v %v", got.GetFailure(), listed)
			}
			continue
		}
		if got.GetExitCode().GetName() != "SUCCESS" {
			t.Fatalf("filter %v: %v", only, got.GetFailure())
		}
		want := 1
		if len(only) == 0 {
			want = 3
		}
		if len(listed) != want {
			t.Fatalf("filter %v ran %d actions, want %d", only, len(listed), want)
		}
		for label := range listed {
			if len(only) > 0 && !strings.HasSuffix(label, ":go."+only[0]) {
				t.Fatalf("filter %v reused action %s", only, label)
			}
		}
		if _, err := g.ListEntities(t.Context(), &buildv1.ListEntitiesRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	write(t, root, "outside/catalog-info.yaml", catalogtest.Component("outside", catalogtest.Cycle))
	if _, err := g.ListProjects(t.Context(), &buildv1.ListProjectsRequest{}); err != nil {
		t.Fatalf("graph query incorrectly planned cycle: %v", err)
	}
	made, err := c.runs.CreateBuild(t.Context(), &buildv1.CreateBuildRequest{Targets: []string{"hello/"}, Only: []string{"compile"}})
	if err != nil {
		t.Fatal(err)
	}
	got := wait(t, c.runs, made.Build.Id)
	if got.GetExitCode().GetName() != "BUILD_FAILURE" || len(actions(t, c, got)) != 0 {
		t.Fatal("retained filtered run hid invalid actions outside target")
	}
	write(t, root, "outside/catalog-info.yaml", `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: outside
spec:
  type: service
  lifecycle: experimental
  owner: team
`)
	repaired := retainedRun(t, c, &buildv1.CreateBuildRequest{Targets: []string{"hello/"}, Only: []string{"compile"}})
	if len(actions(t, c, repaired)) != 1 {
		t.Fatal("repaired run did not select compile")
	}
}

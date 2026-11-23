package build

import (
	connect "connectrpc.com/connect/v2"
	"github.com/frontseat-dev/frontseat/build/engine/engine/catalog/catalogtest"
	"github.com/frontseat-dev/frontseat/build/engine/engine/evaluate"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"google.golang.org/protobuf/proto"
	"testing"
)

// sessionStats is what the session of a daemon's graph says it has done
// and kept.
func sessionStats(t *testing.T, c clients) evaluate.SessionStats {
	t.Helper()
	stats, err := c.daemon.graph.session.Stats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return stats
}

func TestRetainedSessionAPIReusesBothGraphQueries(t *testing.T) {
	root := oneModule(t)
	write(t, root, "hello/catalog-info.yaml", catalogtest.Component("hello", ""))
	c, g := serveWithGraph(t, configuration{Workspace: root, Mounted: true, Executor: scripted{}})
	first, err := g.ListProjects(t.Context(), &buildv1.ListProjectsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	entities, err := g.ListEntities(t.Context(), &buildv1.ListEntitiesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Projects) == 0 || len(entities.Entities) == 0 {
		t.Fatal("empty positive control")
	}
	wantProjects, wantEntities := proto.CloneOf(first), proto.CloneOf(entities)
	first.Projects[0].Ref.Root = "mutated"
	entities.Entities[0].Roots = []string{"mutated"}
	next, err := g.ListProjects(t.Context(), &buildv1.ListProjectsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	nextEntities, err := g.ListEntities(t.Context(), &buildv1.ListEntitiesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(next, wantProjects) || !proto.Equal(nextEntities, wantEntities) {
		t.Fatal("responses differ")
	}
	stats := sessionStats(t, c)
	if stats.Evaluations != 1 || stats.Plans != 0 {
		t.Fatalf("graph requests evaluated %d times, planned %d; want 1, 0", stats.Evaluations, stats.Plans)
	}
}

func TestRetainedSessionAPICloseRefusesBothQueries(t *testing.T) {
	c, g := serveWithGraph(t, configuration{Workspace: oneModule(t), Mounted: true, Executor: scripted{}})
	if _, err := g.ListProjects(t.Context(), &buildv1.ListProjectsRequest{}); err != nil {
		t.Fatal(err)
	}
	c.daemon.Close()
	_, projects := g.ListProjects(t.Context(), &buildv1.ListProjectsRequest{})
	_, entities := g.ListEntities(t.Context(), &buildv1.ListEntitiesRequest{})
	for _, err := range []error{projects, entities} {
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Errorf("closed graph: %v; want unavailable", err)
		}
	}
	stats := sessionStats(t, c)
	if stats.Records != 0 || stats.WireBytes != 0 {
		t.Fatal("closed session retained its answers")
	}
}

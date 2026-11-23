package build

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"google.golang.org/protobuf/proto"
)

func sameGraphError(t *testing.T, got, want error) {
	t.Helper()
	var a, b *connect.Error
	if !errors.As(got, &a) || !errors.As(want, &b) {
		t.Fatalf("expected both calls refused: %v / %v", got, want)
	}
	if a.Code() != b.Code() || a.Message() != b.Message() || len(a.Details()) != len(b.Details()) {
		t.Fatalf("different refusal: %v / %v", a, b)
	}
	for i, d := range a.Details() {
		x, err := connectproto.UnmarshalErrorDetail(d)
		if err != nil {
			t.Fatal(err)
		}
		y, err := connectproto.UnmarshalErrorDetail(b.Details()[i])
		if err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(x, y) {
			t.Fatalf("diagnostic detail %d differs", i)
		}
	}
}

func TestRetainedSessionAPIInvalidatesAndPreservesDiagnostics(t *testing.T) {
	root := oneModule(t)
	write(t, root, "consumer/go.mod", "module example.com/consumer\n\ngo 1.27\n\nrequire example.com/hello v0.0.0\n")
	write(t, root, "consumer/main.go", "package consumer\n")
	c, g := serveWithGraph(t, configuration{Workspace: root, Mounted: true, Executor: scripted{}})
	query := func() *buildv1.ListProjectsResponse {
		t.Helper()
		got, err := g.ListProjects(t.Context(), &buildv1.ListProjectsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		fresh, err := c.daemon.graph.evaluated(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(got, fresh.ProjectList()) {
			t.Fatal("retained API differs from ordinary evaluation")
		}
		return got
	}
	first := query()
	var linked bool
	for _, p := range first.Projects {
		for _, r := range p.Requires {
			if r.GetProviderProject().GetRoot() == "hello" {
				linked = true
			}
		}
	}
	if !linked {
		t.Fatal("control did not infer the dependency from go.mod")
	}
	// Source shape and repository options both reach ordinary plugin inference.
	write(t, root, "hello/main_test.go", "package main\nimport \"testing\"\nfunc TestNew(t *testing.T) {}\n")
	query()
	write(t, root, "frontseat.yaml", "plugins:\n  go: {tags: [changed]}\n  backstage: {}\n")
	changed := query()
	if proto.Equal(first, changed) {
		t.Fatal("option change did not affect graph")
	}
	// A warm previously good result cannot hide malformed input or its diagnostics.
	write(t, root, "hello/go.mod", "this is not a module\n")
	_, got := g.ListProjects(t.Context(), &buildv1.ListProjectsRequest{})
	_, want := c.daemon.graph.evaluated(t.Context())
	sameGraphError(t, got, want)
	_, got = g.ListEntities(t.Context(), &buildv1.ListEntitiesRequest{})
	sameGraphError(t, got, want)
	write(t, root, "hello/go.mod", "module example.com/hello\n\ngo 1.27\n")
	query()
	// Ambiguous providers remain a global refusal, then repair is visible.
	write(t, root, "duplicate/go.mod", "module example.com/hello\n\ngo 1.27\n")
	write(t, root, "duplicate/main.go", "package duplicate\n")
	_, got = g.ListProjects(t.Context(), &buildv1.ListProjectsRequest{})
	_, want = c.daemon.graph.evaluated(t.Context())
	sameGraphError(t, got, want)
	write(t, root, "duplicate/go.mod", "module example.com/other\n\ngo 1.27\n")
	query()
}

func TestRetainedSessionAPIConcurrentRequestsAndDaemonIsolation(t *testing.T) {
	root := oneModule(t)
	c, g := serveWithGraph(t, configuration{Workspace: root, Mounted: true, Executor: scripted{}})
	peer, h := serveWithGraph(t, configuration{Workspace: root, Mounted: true, Executor: scripted{}})
	var group sync.WaitGroup
	for i := range 12 {
		group.Go(func() {
			if i%2 == 0 {
				_, err := g.ListProjects(t.Context(), &buildv1.ListProjectsRequest{})
				if err != nil {
					t.Error(err)
				}
			} else {
				_, err := g.ListEntities(t.Context(), &buildv1.ListEntitiesRequest{})
				if err != nil {
					t.Error(err)
				}
			}
		})
	}
	group.Wait()
	stats, err := c.daemon.graph.session.Stats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Evaluations != 1 {
		t.Fatalf("concurrent requests evaluated %d times", stats.Evaluations)
	}
	want, err := h.ListProjects(t.Context(), &buildv1.ListProjectsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := g.ListProjects(ctx, &buildv1.ListProjectsRequest{}); connect.CodeOf(err) != connect.CodeCanceled {
		t.Fatalf("cancelled caller: %v", err)
	}
	c.daemon.Close()
	got, err := h.ListProjects(t.Context(), &buildv1.ListProjectsRequest{})
	if err != nil || !proto.Equal(got, want) {
		t.Fatalf("closing peer changed graph: %v", err)
	}
	stats, err = peer.daemon.graph.session.Stats(t.Context())
	if err != nil || stats.Evaluations != 1 {
		t.Fatal(fmt.Sprint(stats, err))
	}
}

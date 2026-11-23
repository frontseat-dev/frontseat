package build

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/catalog/catalogtest"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
)

// BenchmarkRetainedSessionAPI measures warm, public Connect graph requests
// over one daemon. The workspace has 100 real Go modules and one catalog
// Component per module. Every group of ten has its first module required by
// the other nine, so Go projects contribute inferred graph dependencies.
func BenchmarkRetainedSessionAPI(b *testing.B) {
	for _, operation := range []string{"ListProjects", "ListEntities"} {
		b.Run(operation, func(b *testing.B) { benchmarkRetainedGraphAPI(b, operation, 1) })
	}
}

// Each operation is one completed burst, including all returned responses.
func BenchmarkRetainedGraphConcurrentAPI(b *testing.B) {
	for _, parallel := range []int{1, 8, 32} {
		b.Run(fmt.Sprintf("requests%d", parallel), func(b *testing.B) {
			benchmarkRetainedGraphAPI(b, "ListProjects", parallel)
		})
	}
}

func benchmarkRetainedGraphAPI(b *testing.B, operation string, parallel int) {
	workspace, names := groupedModules(b)
	for _, name := range names {
		write(b, workspace, name+"/catalog-info.yaml", catalogtest.Component(name, ""))
	}

	_, graph := serveWithGraph(b, configuration{Workspace: workspace, Mounted: true, Executor: scripted{}})
	ctx := context.Background()
	projects := func() error {
		got, err := graph.ListProjects(ctx, &buildv1.ListProjectsRequest{})
		if err == nil && len(got.GetProjects()) != 100 {
			return fmt.Errorf("ListProjects returned %d projects; want 100", len(got.GetProjects()))
		}
		return err
	}
	entities := func() error {
		got, err := graph.ListEntities(ctx, &buildv1.ListEntitiesRequest{})
		if err == nil && len(got.GetEntities()) != 100 {
			return fmt.Errorf("ListEntities returned %d entities; want 100", len(got.GetEntities()))
		}
		return err
	}

	// Exclude daemon/workspace setup and the first request; each timed
	// iteration still includes snapshotting, evaluation, projection,
	// Connect/HTTP encoding, and response allocation.
	var err error
	switch operation {
	case "ListProjects":
		err = projects()
	case "ListEntities":
		err = entities()
	}
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if parallel == 1 {
			switch operation {
			case "ListProjects":
				err = projects()
			case "ListEntities":
				err = entities()
			}
			if err != nil {
				b.Fatal(err)
			}
			continue
		}
		results := make(chan error, parallel)
		var group sync.WaitGroup
		group.Add(parallel)
		for range parallel {
			go func() {
				defer group.Done()
				if operation == "ListProjects" {
					results <- projects()
				} else {
					results <- entities()
				}
			}()
		}
		group.Wait()
		for range parallel {
			if err := <-results; err != nil {
				b.Fatal(err)
			}
		}
	}
}

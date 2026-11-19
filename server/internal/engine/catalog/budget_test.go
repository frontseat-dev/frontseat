package catalog

import (
	"fmt"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbudget"
	"testing"
	"time"
)

// BenchmarkCatalogGraph measures assembly, uniqueness and ownership checks,
// forward/inverse relation resolution and deterministic API ordering together.
func BenchmarkCatalogGraph(b *testing.B) {
	owner := entity.New("group", "default", "team")
	descriptors := []Descriptor{{Entity: Entity{Ref: owner, Manifest: "catalog-info.yaml"}}}
	for i := range 1000 {
		at := fmt.Sprintf("component-%04d", i)
		descriptors = append(descriptors, Descriptor{Entity: Entity{
			Ref: entity.New("component", "default", at), Manifest: at + "/catalog-info.yaml", Roots: []string{at},
		}, Relations: []DeclaredRelation{{Type: "ownedBy", Target: owner, InverseType: "ownerOf"}}})
	}
	for b.Loop() {
		graph, err := Assemble(nil, descriptors)
		if err != nil {
			b.Fatal(err)
		}
		if len(graph.Nodes(nil)) != 1001 || len(graph.Entities()) != 1001 {
			b.Fatal("lost catalog nodes or owners")
		}
	}
}

// Measured 2026-11-07 with `mise exec -- go test -run '^$' -bench
// '^BenchmarkCatalog(Analyze|Graph)$' -benchmem -count=3
// ./server/internal/pluginhost ./server/internal/engine/catalog`:
// 1.858–2.066 ms and 8,116 allocations for 1,001 entities and 2,000 relations.
func TestBudgetOfCatalogGraph(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 19 * time.Millisecond, Allocs: 9000}, BenchmarkCatalogGraph)
}

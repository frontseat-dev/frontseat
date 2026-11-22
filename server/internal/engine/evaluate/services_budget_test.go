package evaluate

import (
	"strconv"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/engine/catalog"
	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbudget"
)

// Measured on 2026-11-14 (Apple M4 Max, go 1.27.3): 2.31–2.70 ms,
// 3,341,444–3,341,526 bytes and 12,608 allocations over three runs of:
// go test -run '^$' -bench '^BenchmarkAPIServiceDependencies$' -benchmem -count=3 ./server/internal/engine/evaluate/
// Retaining each API's identity and port grant adds 2,017 allocations to the
// earlier provider-only projection (10,591). That state prevents a consumed
// API from granting the provider's other endpoints. Time allows machine
// variation; the 13,000 allocation budget allows less than four percent.
func TestBudgetOfAPIServiceDependencies(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 15 * time.Millisecond, Allocs: 13000}, BenchmarkAPIServiceDependencies)
}

// Resolve five hundred consumer/provider/API groups, each provider using a
// shared Resource. The catalog projection is included: that work is paid by
// evaluation too. This measures planning, not runtime service performance.
func BenchmarkAPIServiceDependencies(b *testing.B) {
	database := entity.New("resource", "default", "db")
	descriptors := []catalog.Descriptor{{Entity: catalog.Entity{Ref: database}}}
	started := map[entity.Ref]config.Service{database: {}}
	for i := range 500 {
		name := strconv.Itoa(i)
		consumer := entity.New("component", "default", "consumer-"+name)
		provider := entity.New("component", "default", "provider-"+name)
		api := entity.New("api", "default", "api-"+name)
		descriptors = append(descriptors,
			catalog.Descriptor{Entity: catalog.Entity{Ref: consumer}, Relations: []catalog.DeclaredRelation{{Type: "consumesApi", InverseType: "apiConsumedBy", Target: api}}},
			catalog.Descriptor{Entity: catalog.Entity{Ref: provider}, Relations: []catalog.DeclaredRelation{
				{Type: "providesApi", InverseType: "apiProvidedBy", Target: api},
				{Type: "dependsOn", InverseType: "dependencyOf", Target: database},
			}},
			catalog.Descriptor{Entity: catalog.Entity{Ref: api}},
		)
		started[provider] = config.Service{}
	}
	g, err := catalog.Assemble(nil, descriptors)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		dependencies, _, err := serviceRelationships(g.Nodes(nil), descriptors, started)
		if err != nil || len(dependencies) != 1001 {
			b.Fatalf("invalid runtime projection: %d consumers, %v", len(dependencies), err)
		}
	}
}

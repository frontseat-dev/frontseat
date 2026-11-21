package pluginhost_test

import (
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/conformance"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbudget"
	"testing"
	"time"
)

func backstageFixture(t *testing.T) conformance.Fixture {
	context := &pluginv1.Context{Profile: pluginv1.Profile_PROFILE_DEVELOPMENT}
	return arrangingNothing(context, conformance.Fixture{
		Name: "backstage", Description: &pluginv1.DescribeResponse{Name: "backstage", Abi: 21, Interests: []string{"**/catalog-info.yaml", "!**/testdata/**"}},
		Source: fixtureSource(t, "backstage", "valid"), Absent: files{}, Malformed: fixtureSource(t, "backstage", "malformed"), ErrorCode: "INVALID_CATALOG",
		Analyze: &pluginv1.AnalyzeRequest{Context: context, Roots: []string{"app"}},
		Entities: []*pluginv1.Entity{
			{Ref: &typev1.EntityRef{Kind: "component", Namespace: "edge", Name: "gateway"}, Manifest: "app/catalog-info.yaml", Roots: []string{"app"}, Relations: []*pluginv1.EntityRelation{
				{Type: "ownedBy", Target: &typev1.EntityRef{Kind: "group", Namespace: "edge", Name: "team"}, InverseType: "ownerOf"},
				{Type: "partOf", Target: &typev1.EntityRef{Kind: "system", Namespace: "edge", Name: "shop"}, InverseType: "hasPart"},
			}},
			{Ref: &typev1.EntityRef{Kind: "group", Namespace: "edge", Name: "team"}, Manifest: "app/catalog-info.yaml"},
			// A Location is an entity of its file, with no source and no
			// relation: what it lists is Backstage's to read.
			{Ref: &typev1.EntityRef{Kind: "location", Namespace: "edge", Name: "app"}, Manifest: "app/catalog-info.yaml"},
		},
		Reads: []conformance.Observation{{Import: "read", Argument: "app/catalog-info.yaml", Found: true}},
		// The source says the gateway depends on billing: the file's first
		// document gets spec.dependsOn, written bare as billing is of the
		// gateway's own namespace, and no dependencyOf, which it does not
		// state.
		Reconcile: &pluginv1.ReconcileRequest{Context: context, Root: "app", Entities: []*pluginv1.Resolved{{
			Ref: &typev1.EntityRef{Kind: "component", Namespace: "edge", Name: "gateway"}, Manifest: "app/catalog-info.yaml",
			DependsOn:    []*typev1.EntityRef{{Kind: "component", Namespace: "edge", Name: "billing"}},
			DependencyOf: []*typev1.EntityRef{{Kind: "component", Namespace: "edge", Name: "portal"}},
		}}},
		Edits: []*pluginv1.Edit{{Path: "app/catalog-info.yaml", Document: 0, Keys: []string{"spec", "dependsOn"}, Values: []string{"component:billing"}}},
	})
}

func BenchmarkCatalogAnalyze(b *testing.B) {
	plugin := conformance.Plugin(b, "backstage")
	source := fixtureSource(b, "backstage", "valid")
	request := &pluginv1.AnalyzeRequest{Context: &pluginv1.Context{Profile: pluginv1.Profile_PROFILE_DEVELOPMENT}, Roots: []string{"app"}}
	benchmarkAnalysis(b, plugin, "backstage", source, request)
}

// Measured with `mise exec -- go test -run '^$' -bench '^BenchmarkCatalog(Analyze|Graph)$'
// -benchmem -count=3 ./server/internal/pluginhost
// ./server/internal/engine/catalog` on 2026-11-07:
// 279.5–296.2 microseconds and 825 allocations for two catalog documents,
// including real Wasm instantiation, tracked YAML parsing and host validation.
// On 2026-11-16, with `mise run go:test -- -count=1 -run '^$' -bench
// 'BenchmarkCatalogAnalyze$' -benchmem -benchtime 200x -count 3
// ./server/internal/pluginhost/` (the task prepares the Wasm modules
// the benchmark loads): main took 837 allocations for two documents; the
// Backstage plugin that reads a Location took 840 for the same two, and
// 847 to 853, in 348 to 352 microseconds, for three, the third a
// Location. The budget stands.
func TestBudgetOfCatalogAnalysis(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 2800 * time.Microsecond, Allocs: 865}, BenchmarkCatalogAnalyze)
}

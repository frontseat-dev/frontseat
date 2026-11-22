package evaluate

import (
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/engine/catalog"
	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
)

func TestProductionReviewDuplicateCustomProgramIsNotSelectedByDocumentOrder(t *testing.T) {
	const provider = `apiVersion: backstage.io/v1alpha1
kind: Component
metadata: {name: provider}
spec:
  type: service
  lifecycle: production
  owner: team
  frontseat:
    actions:
      serve:
        kind: program
        tools: [caddy]
        command: [caddy, run, --config, primary.json]
        inputs: [primary.json]
    service:
      program: ":custom.serve"
      ports: {http: 8080}
      ready: [{port: http}]
`
	const other = `apiVersion: backstage.io/v1alpha1
kind: Resource
metadata: {name: other}
spec:
  type: database
  owner: team
  frontseat:
    actions:
      serve:
        kind: program
        tools: [caddy]
        command: [caddy, run, --config, replacement.json]
        inputs: [replacement.json]
`
	owner := entity.New("component", "default", "provider")
	for _, raw := range []string{provider, provider + "---\n" + other, other + "---\n" + provider} {
		src := &counted{files: map[string]string{"frontseat.yaml": "plugins: {}\n", "provider/catalog-info.yaml": raw}}
		read, err := config.Read(t.Context(), "", repositoryView{source: src})
		if err != nil {
			t.Fatal(err)
		}
		out := serviceCatalog(t, owner, []catalog.Descriptor{{Entity: catalog.Entity{Ref: owner, Roots: []string{"provider"}}}})
		out.Actions = nil
		for _, own := range read.Actions() {
			action, err := custom("", own)
			if err != nil {
				t.Fatal(err)
			}
			out.Actions = append(out.Actions, action)
		}
		started := map[entity.Ref]config.Service{owner: read.Services()[0]}
		selected, err := servicePrograms(&out, started)
		if len(read.Actions()) == 1 {
			if err != nil {
				t.Fatalf("single-program control refused: %v", err)
			}
			if !strings.Contains(strings.Join(selected[owner].Declared.GetCommand().GetArguments(), " "), "primary.json") {
				t.Fatal("wrong control program")
			}
			continue
		}
		if err == nil && plan.Check(out.Actions) == nil {
			t.Errorf("two catalog documents declare the same program label and select by document order: %v", selected[owner].Declared.GetCommand().GetArguments())
		}
	}
}

package pluginhost_test

import (
	"context"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/testsupport/testmodule"
	"strings"
	"testing"
)

func TestBackstageBuiltinValidation(t *testing.T) {
	raw, err := testmodule.Plugin("backstage")
	if err != nil {
		t.Fatal(err)
	}
	h, err := pluginhost.New(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	p, err := h.Load(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	valid := "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: app\nspec: {type: service, lifecycle: production, owner: team}\n"
	for _, c := range []struct {
		name, source string
		refuse       bool
	}{
		{"valid", valid, false},
		{"kind_policy", strings.Replace(valid, "kind: Component", "kind: service-blueprint", 1), true},
		{"namespace_policy", strings.Replace(valid, "  name: app", "  name: app\n  namespace: tenant.scope", 1), true},
		{"underscore_namespace", strings.Replace(valid, "  name: app", "  name: app\n  namespace: a_b", 1), true},
		{"name_policy", strings.Replace(valid, "name: app", "name: "+strings.Repeat("a", 100), 1), true},
		{"identity_control", strings.Replace(valid, "  name: app", "  name: "+strings.Repeat("a", 63)+"\n  namespace: team-scope", 1), false},
		{"blank_link_title", strings.Replace(valid, "  name: app", "  name: app\n  links: [{url: 'https://example.com', title: '   '}]", 1), true},
		{"blank_link_icon", strings.Replace(valid, "  name: app", "  name: app\n  links: [{url: 'https://example.com', icon: '   '}]", 1), true},
		{"link_text_control", strings.Replace(valid, "  name: app", "  name: app\n  links: [{url: 'https://example.com', title: ' Documentation ', icon: ' docs '}]", 1), false},
		{"labels_shape", strings.Replace(valid, "  name: app", "  name: app\n  labels: [x]", 1), true},
		{"annotations_value", strings.Replace(valid, "  name: app", "  name: app\n  annotations: {example.com/key: 123}", 1), true},
		{"tags_shape", strings.Replace(valid, "  name: app", "  name: app\n  tags: [123]", 1), true},
		{"links_missing_url", strings.Replace(valid, "  name: app", "  name: app\n  links: [{title: example}]", 1), true},
		{"relative_url", strings.Replace(valid, "  name: app", "  name: app\n  links: [{url: relative/path}]", 1), true},
		{"plain_url", strings.Replace(valid, "  name: app", "  name: app\n  links: [{url: 'plain text'}]", 1), true},
		{"bad_url_port", strings.Replace(valid, "  name: app", "  name: app\n  links: [{url: 'https://example.com:broken'}]", 1), true},
		{"mailto_control", strings.Replace(valid, "  name: app", "  name: app\n  links: [{url: 'mailto:owner@example.com'}]", 1), false},
		{"null_labels", strings.Replace(valid, "  name: app", "  name: app\n  labels: null", 1), true},
		{"null_title", strings.Replace(valid, "  name: app", "  name: app\n  title: null", 1), true},
		{"null_type", "apiVersion: backstage.io/v1alpha1\nkind: System\nmetadata: {name: app}\nspec: {owner: team, type: null}\n", true},
		{"metadata_control", strings.Replace(valid, "  name: app", "  name: app\n  labels: {example.com/key: value}\n  annotations: {example.com/key: '123'}\n  title: Application\n  tags: [c++, 'c#', http:server]\n  links: [{url: 'https://example.com'}]", 1), false},
		{"system_type", "apiVersion: backstage.io/v1alpha1\nkind: System\nmetadata: {name: app}\nspec: {owner: team, type: 123}\n", true},
		{"custom_version", "apiVersion: ' '\nkind: Custom\nmetadata: {name: app}\n", true},
		{"custom_control", "apiVersion: example.com/v1\nkind: Custom\nmetadata: {name: app}\nspec: {owner: {custom: value}}\n", false},
		{"unknown_extension", strings.Replace(valid, "  name: app", "  name: app\n  custom-extension: {arbitrary: [1, 2]}", 1), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := new(pluginv1.AnalyzeResponse)
			_, err := p.Call(t.Context(), files{"app/catalog-info.yaml": c.source}, "Analyze", &pluginv1.AnalyzeRequest{Roots: []string{"app"}, Context: &pluginv1.Context{Profile: typev1.Profile_PROFILE_DEVELOPMENT}}, out)
			if c.refuse && err == nil {
				t.Fatalf("invalid known catalog field accepted: %v", out)
			}
			if !c.refuse && (err != nil || len(out.GetAnalyses()[0].GetEntities()) != 1) {
				t.Fatalf("valid control rejected: %v %v", err, out)
			}
		})
	}
}

func TestPluginCatalogIdentityIsProviderNeutral(t *testing.T) {
	entity := &pluginv1.Entity{Ref: &typev1.EntityRef{Kind: "service-blueprint", Namespace: "tenant.scope", Name: strings.Repeat("a", 100)}, Manifest: "catalog.yaml"}
	if err := pluginhost.CheckAnalysis("catalog", []string{""}, &pluginv1.AnalyzeResponse{Analyses: []*pluginv1.Analysis{{Entities: []*pluginv1.Entity{entity}}}}); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogRootAliasCannotBypassOwnershipUniqueness(t *testing.T) {
	for _, root := range []string{"", "."} {
		entity := &pluginv1.Entity{Ref: &typev1.EntityRef{Kind: "component", Namespace: "default", Name: "app"}, Manifest: "catalog-info.yaml", Roots: []string{root}}
		err := pluginhost.CheckAnalysis("fake", []string{""}, &pluginv1.AnalyzeResponse{Analyses: []*pluginv1.Analysis{{Entities: []*pluginv1.Entity{entity}}}})
		if root == "" && err != nil {
			t.Fatal(err)
		}
		if root == "." && err == nil {
			t.Error("dot accepted as another spelling of the empty repository root")
		}
	}
}

// Each rule an entity is held to is said by its own words, and the
// control, an entity that keeps every rule, is accepted.
func TestAnEntityIsRefusedByTheRuleItBroke(t *testing.T) {
	entity := func(roots []string, relations ...*pluginv1.EntityRelation) *pluginv1.Entity {
		return &pluginv1.Entity{Ref: &typev1.EntityRef{Kind: "component", Namespace: "default", Name: "app"}, Manifest: "app/catalog-info.yaml", Roots: roots, Relations: relations}
	}
	check := func(described *pluginv1.Entity) error {
		return pluginhost.CheckAnalysis("catalog", []string{"app"}, &pluginv1.AnalyzeResponse{Analyses: []*pluginv1.Analysis{{Root: "app", Entities: []*pluginv1.Entity{described}}}})
	}
	owned := &pluginv1.EntityRelation{Type: "ownedBy", Target: &typev1.EntityRef{Kind: "group", Namespace: "default", Name: "team"}, InverseType: "ownerOf"}
	if err := check(entity([]string{"app", "app/web"}, owned)); err != nil {
		t.Fatalf("the control is refused: %v", err)
	}
	for rule, broken := range map[string]*pluginv1.Entity{
		"is said twice":                 entity([]string{"app", "app"}),
		"is not written plainly":        entity([]string{"app/./web"}),
		"the root that was asked about": entity([]string{"lib"}),
		"each lowerCamelCase":           entity([]string{"app"}, &pluginv1.EntityRelation{Type: "Owned-By", Target: owned.GetTarget(), InverseType: "ownerOf"}),
		"a relation's target":           entity([]string{"app"}, &pluginv1.EntityRelation{Type: "ownedBy", Target: &typev1.EntityRef{Kind: "group"}, InverseType: "ownerOf"}),
	} {
		if err := check(broken); err == nil || !strings.Contains(err.Error(), rule) {
			t.Errorf("an entity that breaks the rule %q is refused as %v", rule, err)
		}
	}
}

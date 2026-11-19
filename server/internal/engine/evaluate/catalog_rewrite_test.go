package evaluate

import (
	"github.com/frontseat-dev/frontseat/server/internal/engine/internal/yamlnode"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/conformance"
	"go.yaml.in/yaml/v4"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewRewritePreservesSDKScalarValues(t *testing.T) {
	parse := catalogReader(t)
	for _, value := range []string{"2026-11-07", "0123", "0x12", "'0123'", "'2026-11-07'", "!!str 0123", "{date: 2026-11-07, number: 0123}", "[2026-11-07, 0123]", "!!str \"true\"", "|-\n  0123\n  2026-11-07"} {
		t.Run(value, func(t *testing.T) {
			raw := "kind: Component\nmetadata: {name: a}\nspec: {dependsOn: [component:b]}\nx-source: &value " + value + "\nx-copy: *value\n"
			fixed, err := yamlnode.SetStrings([]byte(raw), 0, []string{"spec", "dependsOn"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			// Ignore the intended dependsOn removal; compare the SDK's parsed values
			// for both independent extension fields, not the native YAML schema.
			extensions := func(parsed string) string {
				at := strings.Index(parsed, "\"x-copy\"")
				if at < 0 {
					t.Fatal("missing extension in ", parsed)
				}
				return parsed[at:]
			}
			before, after := extensions(parse(t, raw)), extensions(parse(t, string(fixed)))
			if before != after {
				t.Fatalf("rewrite changed SDK values\nbefore: %s\nafter: %s\nrewritten:\n%s", before, after, fixed)
			}
		})
	}
}

func TestReviewRewritePreservesSDKMergedExtensions(t *testing.T) {
	read := catalogReader(t)
	for name, raw := range map[string]string{
		"anchored spec":   "kind: Component\nmetadata: {name: a}\nspec: &spec {dependsOn: [component:b], date: 2026-11-07, id: 0123}\nx-copy: *spec\n",
		"spec merge":      "kind: Component\nmetadata: {name: a}\nx-base: &base {dependsOn: [component:b], date: 2026-11-07, id: 0123}\nspec: {<<: *base}\nx-copy: *base\n",
		"root merge":      "x-base: &base {kind: Component, metadata: {name: a}, spec: {dependsOn: [component:b], date: 2026-11-07, id: 0123}}\n<<: *base\nx-copy: *base\n",
		"merged metadata": "x-base: &base {name: a}\nkind: Component\nmetadata: {<<: *base}\nspec: {dependsOn: [component:b]}\nx-copy: *base\n",
	} {
		t.Run(name, func(t *testing.T) {
			fixed, err := yamlnode.SetStrings([]byte(raw), 0, []string{"spec", "dependsOn"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			parse := func(source string) string {
				t.Helper()
				parsed := read(t, source)
				at := strings.Index(parsed, "\"x-copy\"")
				if at < 0 {
					t.Fatal(parsed)
				}
				return parsed[at:]
			}
			before, after := parse(raw), parse(string(fixed))
			if before != after {
				t.Fatalf("unrelated extension changed\nbefore: %s\nafter: %s\n%s", before, after, fixed)
			}
			// The list is gone, or empty where a merge would otherwise
			// let an inherited one show through.
			var document struct {
				Spec struct {
					DependsOn []string `yaml:"dependsOn"`
				}
			}
			if err := yaml.Unmarshal(fixed, &document); err != nil {
				t.Fatal(err)
			}
			if len(document.Spec.DependsOn) != 0 {
				t.Fatalf("the rewrite did not take the list out: %v\n%s", document.Spec.DependsOn, fixed)
			}
		})
	}
}

// catalogReader composes the evaluator's real snapshot view with an SDK reader.
// It checks that a host rewrite retains the guest's interpretation of source.
func catalogReader(t *testing.T) func(*testing.T, string) string {
	t.Helper()
	plugin := conformance.SDKExample(t, "documents")
	return func(t *testing.T, raw string) string {
		t.Helper()
		out := new(pluginv1.ArrangeResponse)
		_, err := plugin.Call(t.Context(), catalogDocument(t, raw), "Arrange", &pluginv1.ArrangeRequest{Context: &pluginv1.Context{Profile: pluginv1.Profile_PROFILE_DEVELOPMENT}, Root: "catalog-info.yaml"}, out)
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Files) != 1 {
			t.Fatalf("SDK reader returned %d files, want one", len(out.Files))
		}
		return string(out.Files[0].Content)
	}
}

func catalogDocument(t *testing.T, raw string) repositoryView {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "catalog-info.yaml"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := new(source.Keeper).Take(t.Context(), source.Tree{Root: root, Paths: map[string]string{"": "."}})
	if err != nil {
		t.Fatal(err)
	}
	return repositoryView{source: snapshot}
}

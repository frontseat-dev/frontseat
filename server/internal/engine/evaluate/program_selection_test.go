package evaluate

import (
	"errors"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// Two documents of a catalog file that declare one action are refused
// by name, as the file's mistake, whichever is written first, in a
// workspace that starts no service: neither is built in the other's
// place. One document that declares it is built.
func TestTwoDocumentsThatDeclareOneActionAreRefused(t *testing.T) {
	document := func(kind, name, config string) string {
		return "apiVersion: backstage.io/v1alpha1\nkind: " + kind + "\nmetadata: {name: " + name + "}\nspec:\n  type: service\n  owner: team\n  frontseat:\n" +
			"    actions:\n      lint:\n        command: [lint, --config, " + config + "]\n        inputs: [" + config + "]\n"
	}
	component, resource := document("Component", "provider", "primary.json"), document("Resource", "other", "replacement.json")
	for name, c := range map[string]struct {
		file   string
		refuse bool
	}{
		"one document":                   {component, false},
		"two documents":                  {component + "---\n" + resource, true},
		"two documents, the other first": {resource + "---\n" + component, true},
	} {
		t.Run(name, func(t *testing.T) {
			src := &counted{files: map[string]string{"frontseat.yaml": "plugins: {}\n", "provider/catalog-info.yaml": c.file}}
			out, err := evaluate(t.Context(), src, nil, &Answers{}, pluginv1.Profile_PROFILE_DEVELOPMENT, nil)
			if !c.refuse {
				if err != nil {
					t.Fatalf("one declaration refused: %v", err)
				}
				if len(out.Actions) != 1 || !strings.Contains(strings.Join(out.Actions[0].Declared.GetCommand().GetArguments(), " "), "primary.json") {
					t.Fatalf("actions = %v, want the one declared", out.Actions)
				}
				return
			}
			var mistake *model.Mistake
			if !errors.As(err, &mistake) {
				t.Fatalf("err = %v, want the file's mistake", err)
			}
			if mistake.File != "provider/catalog-info.yaml" || !strings.Contains(mistake.Message, "//provider:custom.lint is declared by more than one document") {
				t.Fatalf("mistake = %q in %q, want the action named in its file", mistake.Message, mistake.File)
			}
		})
	}
}

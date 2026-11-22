package evaluate

import (
	"context"
	"errors"
	"path"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
)

// Two documents of a catalog file that declare one action are refused
// by name, as the file's mistake, whichever is written first, in a
// workspace that starts no service and in one that starts the action as
// a service: neither is built in the other's place. One document that
// declares it is built, with its service and without.
func TestTwoDocumentsThatDeclareOneActionAreRefused(t *testing.T) {
	document := func(kind, name, config, service string) string {
		return "apiVersion: backstage.io/v1alpha1\nkind: " + kind + "\nmetadata: {name: " + name + "}\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n  frontseat:\n" +
			"    actions:\n      lint:\n        command: [lint, --config, " + config + "]\n        inputs: [" + config + "]\n" + service
	}
	const service = "      serve:\n        program: true\n        tools: [caddy]\n        command: [caddy, run]\n    service:\n      program: \":custom.run/serve\"\n      ports: {http: 8080}\n      ready: [{port: http}]\n"
	component, resource := document("Component", "provider", "primary.json", ""), document("Resource", "other", "replacement.json", "")
	serving := document("Component", "provider", "primary.json", service)
	plugins := []*pluginhost.Plugin{loadedPlugin(t, "backstage")}
	for name, c := range map[string]struct {
		file   string
		refuse bool
	}{
		"one document":                         {component, false},
		"one document, started as a service":   {serving, false},
		"two documents":                        {component + "---\n" + resource, true},
		"two documents, the other first":       {resource + "---\n" + component, true},
		"two documents, one started":           {serving + "---\n" + resource, true},
		"two documents, the started one after": {resource + "---\n" + serving, true},
	} {
		t.Run(name, func(t *testing.T) {
			src := &counted{files: map[string]string{"frontseat.yaml": "plugins: {backstage: {}}\n", "provider/catalog-info.yaml": c.file}}
			out, err := evaluate(t.Context(), src, plugins, &Answers{}, typev1.Profile_PROFILE_DEVELOPMENT, nil)
			if !c.refuse {
				if err != nil {
					t.Fatalf("one declaration refused: %v", err)
				}
				var lints []string
				for _, action := range out.Actions {
					if action.Label.Name == "lint" {
						lints = append(lints, strings.Join(action.Declared.GetCommand().GetArguments(), " "))
					}
				}
				if len(lints) != 1 || !strings.Contains(lints[0], "primary.json") {
					t.Fatalf("lint actions = %v, want the one declared", lints)
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

// analyzing answers a plugin's analysis with the projects it is given,
// each at the root it was asked about, and asks the rest as answers do.
type analyzing struct {
	asking
	projects []*pluginv1.Project
}

func (a analyzing) askObserved(ctx context.Context, plugin *pluginhost.Plugin, view repositoryView, method string, request, response proto.Message) (bool, []pluginhost.Read, error) {
	if method != "Analyze" {
		return a.asking.askObserved(ctx, plugin, view, method, request, response)
	}
	root := request.(*pluginv1.AnalyzeRequest).GetRoots()[0]
	analysis := &pluginv1.Analysis{Root: root}
	for _, project := range a.projects {
		if path.Clean(project.GetRoot()) == path.Clean(root) {
			analysis.Projects = append(analysis.Projects, project)
		}
	}
	response.(*pluginv1.AnalyzeResponse).Analyses = []*pluginv1.Analysis{analysis}
	return false, nil, nil
}

// A label is one action's whatever declared it: a plugin that names
// itself as the host names a catalog file's actions, and declares an
// action of a project where a catalog file declares one of that name, is
// refused by name. The control is the same plugin declaring an action
// of another name.
func TestALabelTwoSourcesDeclareIsRefused(t *testing.T) {
	const catalogFile = "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: provider}\nspec:\n  type: service\n  owner: team\n  frontseat:\n" +
		"    actions:\n      lint:\n        command: [lint]\n"
	project := func(root string, actions ...*pluginv1.Action) *pluginv1.Project {
		return &pluginv1.Project{Root: root, Name: root, Kind: "custom", Manifest: root + "/catalog-info.yaml", Actions: actions}
	}
	action := func(name string) *pluginv1.Action {
		return &pluginv1.Action{Name: name, Command: &pluginv1.Command{Arguments: []string{name}}, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}}}
	}
	for name, c := range map[string]struct {
		plugin   string
		projects []*pluginv1.Project
		twice    string
	}{
		"a plugin's action and a catalog file's":    {customPlugin, []*pluginv1.Project{project("provider", action("lint"))}, "//provider:custom.lint"},
		"a plugin's action beside a catalog file's": {customPlugin, []*pluginv1.Project{project("provider", action("test"))}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			plugins := []*pluginhost.Plugin{{Description: &pluginv1.DescribeResponse{Name: c.plugin, Interests: []string{"**/catalog-info.yaml"}}}}
			src := &counted{files: map[string]string{"frontseat.yaml": "plugins: {" + c.plugin + ": {}}\n", "provider/catalog-info.yaml": catalogFile}}
			_, err := evaluate(t.Context(), src, plugins, analyzing{asking: &Answers{}, projects: c.projects}, typev1.Profile_PROFILE_DEVELOPMENT, nil)
			if c.twice == "" {
				if err != nil {
					t.Fatalf("two labels refused: %v", err)
				}
				return
			}
			var mistake *model.Mistake
			if !errors.As(err, &mistake) || !strings.Contains(mistake.Message, c.twice+" is declared more than once") {
				t.Fatalf("err = %v, want %s refused as declared twice", err, c.twice)
			}
		})
	}
}

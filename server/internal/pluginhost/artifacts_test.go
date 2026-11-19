package pluginhost_test

import (
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
)

func TestAProvidedArtifactNamesAnOutputOfItsOwnProject(t *testing.T) {
	sound := func() *pluginv1.Project {
		return &pluginv1.Project{Root: "app", Kind: "fixture", Name: "app", Manifest: "app/package.json",
			Provides: []*pluginv1.Provide{{Coordinate: &pluginv1.Coordinate{Ecosystem: "fixture", Name: "app"}, Artifacts: []*pluginv1.Produced{{Action: "install", Output: "modules/"}}}},
			Actions:  []*pluginv1.Action{{Name: "install", Command: &pluginv1.Command{Arguments: []string{"fixture"}}, Outputs: []*pluginv1.Output{{Path: "modules/"}}}},
		}
	}
	check := func(p *pluginv1.Project) error {
		return pluginhost.CheckAnalysis("fixture", []string{"app"}, &pluginv1.AnalyzeResponse{Analyses: []*pluginv1.Analysis{{Root: "app", Projects: []*pluginv1.Project{p}}}})
	}
	if err := check(sound()); err != nil {
		t.Fatalf("valid provider refused: %v", err)
	}
	optional := sound()
	optional.Actions[0].Outputs[0].Optional = true
	if err := check(optional); err != nil {
		t.Fatalf("optional output refused: %v", err)
	}
	for name, mutate := range map[string]func(*pluginv1.Project){
		"unknown action": func(p *pluginv1.Project) { p.Provides[0].Artifacts[0].Action = "other" },
		"unknown output": func(p *pluginv1.Project) { p.Provides[0].Artifacts[0].Output = "other/" },
		"empty":          func(p *pluginv1.Project) { p.Provides[0].Artifacts[0] = nil },
		"outside":        func(p *pluginv1.Project) { p.Provides[0].Artifacts[0].Output = "../modules/" },
		"home": func(p *pluginv1.Project) {
			p.Actions[0].Outputs[0].Path = "~/modules/"
			p.Provides[0].Artifacts[0].Output = "~/modules/"
		},
		"duplicate": func(p *pluginv1.Project) {
			p.Provides[0].Artifacts = append(p.Provides[0].Artifacts, p.Provides[0].Artifacts[0])
		},
		"overlap": func(p *pluginv1.Project) {
			p.Actions[0].Outputs = append(p.Actions[0].Outputs, &pluginv1.Output{Path: "modules/nested/"})
			p.Provides[0].Artifacts = append(p.Provides[0].Artifacts, &pluginv1.Produced{Action: "install", Output: "modules/nested/"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := sound()
			mutate(p)
			if err := check(p); err == nil {
				t.Fatal("invalid artifact provider accepted")
			}
		})
	}
}

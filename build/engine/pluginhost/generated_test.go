package pluginhost_test

import (
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
)

func TestGeneratedSourcesNameRequiredDeclaredOutputs(t *testing.T) {
	valid := func() *pluginv1.AnalyzeResponse {
		answer := sound()
		project := answer.Analyses[0].Projects[0]
		project.GeneratedSources = []*pluginv1.GeneratedSource{{Action: "compile", Output: "bin/", Directory: "gen/go", Patterns: []string{"**/*.pb.go"}}}
		return answer
	}
	if err := pluginhost.CheckAnalysis("fixture", []string{"services"}, valid()); err != nil {
		t.Fatalf("valid contribution control: %v", err)
	}
	for name, change := range map[string]func(*pluginv1.Project){
		"unknown action":    func(p *pluginv1.Project) { p.GeneratedSources[0].Action = "unknown" },
		"undeclared output": func(p *pluginv1.Project) { p.GeneratedSources[0].Output = "other/" },
		"optional output":   func(p *pluginv1.Project) { p.Actions[1].Outputs[0].Optional = true },
		"file output": func(p *pluginv1.Project) {
			p.Actions[1].Outputs[0].Path = "file"
			p.GeneratedSources[0].Output = "file"
		},
		"empty directory":         func(p *pluginv1.Project) { p.GeneratedSources[0].Directory = "" },
		"escaped directory":       func(p *pluginv1.Project) { p.GeneratedSources[0].Directory = "../outside" },
		"directory alias":         func(p *pluginv1.Project) { p.GeneratedSources[0].Directory = "gen/../other" },
		"backslash":               func(p *pluginv1.Project) { p.GeneratedSources[0].Directory = `gen\go` },
		"no patterns":             func(p *pluginv1.Project) { p.GeneratedSources[0].Patterns = nil },
		"escaped pattern":         func(p *pluginv1.Project) { p.GeneratedSources[0].Patterns = []string{"../*.go"} },
		"invalid pattern":         func(p *pluginv1.Project) { p.GeneratedSources[0].Patterns = []string{"[invalid"} },
		"input set reference":     func(p *pluginv1.Project) { p.GeneratedSources[0].Patterns = []string{"=sources"} },
		"same contribution twice": func(p *pluginv1.Project) { p.GeneratedSources = append(p.GeneratedSources, p.GeneratedSources[0]) },
	} {
		t.Run(name, func(t *testing.T) {
			answer := valid()
			change(answer.Analyses[0].Projects[0])
			if err := pluginhost.CheckAnalysis("fixture", []string{"services"}, answer); err == nil {
				t.Fatal("accepted invalid generated contribution")
			}
		})
	}
}

// A contribution says where its output goes by its directory beneath the
// project or by its destination in the repository outside it, plainly,
// and never by both. Whether the destination's Component lets it go
// there is the evaluation's to hold (TestAGeneratedDestinationNeedsTheConsentOfItsComponent).
func TestAGeneratedDestinationIsAPlainPlaceOutsideItsProject(t *testing.T) {
	valid := func() *pluginv1.AnalyzeResponse {
		answer := sound()
		project := answer.Analyses[0].Projects[0]
		project.GeneratedSources = []*pluginv1.GeneratedSource{{Action: "compile", Output: "bin/", Destination: "lib/gen", Patterns: []string{"**/*.pb.go"}}}
		return answer
	}
	if err := pluginhost.CheckAnalysis("fixture", []string{"services"}, valid()); err != nil {
		t.Fatalf("a destination outside the project was refused, so this test proves nothing: %v", err)
	}
	for name, destination := range map[string]string{
		"a parent part":           "lib/../gen",
		"leaving the repository":  "../lib/gen",
		"absolute":                "/lib/gen",
		"a dot part":              "lib/./gen",
		"a slash at its end":      "lib/gen/",
		"the repository itself":   ".",
		"a backslash":             `lib\gen`,
		"a NUL":                   "lib/gen\x00",
		"the host's own":          ".frontseat/gen",
		"the project itself":      "services/hello",
		"beneath the project":     "services/hello/gen",
		"a project's prefix only": "services/hello/../hello/gen",
		"holding the project":     "services",
		"the host's, deeper":      "lib/.frontseat/gen",
	} {
		t.Run(name, func(t *testing.T) {
			answer := valid()
			answer.Analyses[0].Projects[0].GeneratedSources[0].Destination = destination
			if err := pluginhost.CheckAnalysis("fixture", []string{"services"}, answer); err == nil {
				t.Fatalf("destination %q accepted", destination)
			}
		})
	}
	// A neighbour whose name begins as the project's is outside it.
	neighbour := valid()
	neighbour.Analyses[0].Projects[0].GeneratedSources[0].Destination = "services/hello-gen"
	if err := pluginhost.CheckAnalysis("fixture", []string{"services"}, neighbour); err != nil {
		t.Fatalf("a neighbour of the project refused: %v", err)
	}
	deep := valid()
	deep.Analyses[0].Projects[0].GeneratedSources[0].Destination = ""
	deep.Analyses[0].Projects[0].GeneratedSources[0].Directory = "gen/.frontseat"
	if err := pluginhost.CheckAnalysis("fixture", []string{"services"}, deep); err == nil {
		t.Fatal("a directory in .frontseat accepted")
	}
	both := valid()
	both.Analyses[0].Projects[0].GeneratedSources[0].Directory = "gen"
	if err := pluginhost.CheckAnalysis("fixture", []string{"services"}, both); err == nil {
		t.Fatal("a contribution naming both a directory and a destination accepted")
	}
	// The same place said once as a directory and once as a destination
	// cannot be: a directory is beneath the project, a destination outside.
	twice := valid()
	twice.Analyses[0].Projects[0].GeneratedSources = append(twice.Analyses[0].Projects[0].GeneratedSources, &pluginv1.GeneratedSource{Action: "compile", Output: "bin/", Destination: "lib/gen", Patterns: []string{"**/*.pb.go"}})
	if err := pluginhost.CheckAnalysis("fixture", []string{"services"}, twice); err == nil {
		t.Fatal("one destination contributed twice accepted")
	}
}

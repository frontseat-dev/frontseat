package build

import (
	"slices"
	"strings"
	"testing"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
)

// What a daemon keeps of an evaluation is its own and ends with it: a
// daemon made anew over the same workspace and the same record of runs
// evaluates with the plugins it was given, and is answered nothing of what
// the one before kept. Given the same plugins it says what the first said,
// having evaluated for itself; given others, with the Go plugin's module
// replaced by none, it says what those say. The control is the first
// daemon, which finds the Go module's project.
func TestADaemonMadeAnewKeepsNothingOfTheOneBefore(t *testing.T) {
	root, runs := oneModule(t), t.TempDir()
	all, err := loaded()
	if err != nil {
		t.Fatal(err)
	}
	asked := func(plugins []*pluginhost.Plugin) (*buildv1.ListProjectsResponse, error, int) {
		t.Helper()
		served, err := newDaemon(configuration{Workspace: root, Mounted: true, Builds: runs, Executor: scripted{}, Plugins: plugins})
		if err != nil {
			t.Fatal(err)
		}
		defer served.Close()
		got, failure := served.graph.session.Projects(t.Context())
		stats, err := served.graph.session.Stats(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return got, failure, stats.Evaluations
	}
	first, err, evaluations := asked(all)
	if err != nil || evaluations != 1 || !slices.ContainsFunc(first.GetProjects(), func(p *buildv1.Project) bool { return p.GetPlugin() == "go" }) {
		t.Fatalf("the first daemon found no project of the Go plugin: %v, %v, evaluated %d times: this test proves nothing", first, err, evaluations)
	}
	same, err, evaluations := asked(all)
	if err != nil || evaluations != 1 || len(same.GetProjects()) != len(first.GetProjects()) {
		t.Fatalf("a daemon made anew with the same plugins says %v, %v, having evaluated %d times; want what the first said, evaluated once", same, err, evaluations)
	}
	without := slices.DeleteFunc(slices.Clone(all), func(p *pluginhost.Plugin) bool { return p.Description.GetName() == "go" })
	if len(without) != len(all)-1 {
		t.Fatalf("the Go plugin is not among the %d loaded", len(all))
	}
	replaced, err, _ := asked(without)
	// The repository names a plugin this daemon was not given: it says so,
	// as a daemon that never saw the workspace would.
	if err == nil || !strings.Contains(err.Error(), "there is no plugin go") || replaced != nil {
		t.Fatalf("a daemon without the Go plugin's module was answered %v, %v", replaced, err)
	}
}

package build

import (
	"context"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/exec/reapi/reapitest"
	"github.com/frontseat-dev/frontseat/common/record"
	"github.com/frontseat-dev/frontseat/common/record/manifest"
	"github.com/frontseat-dev/frontseat/common/vcs/chosen"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/testsupport/testforge"
)

// What a build plans is what its directory holds, repository by
// repository. How a directory comes to hold it, a repository cloned at
// its commit, the directories its entities govern taken alone, a
// workspace widened, is what fills a workspace's to prove
// (workspace/box/source), and the journey whole is the specs'.

// planned returns the labels a build of a daemon's directory plans.
func planned(t *testing.T, c clients) string {
	t.Helper()
	run := create(t, c.runs)
	var labels []string
	for _, e := range events(t, c.runs, run.GetId(), 0) {
		for _, a := range e.GetPlanned().GetActions() {
			labels = append(labels, a.GetLabel())
		}
	}
	return strings.Join(labels, " ")
}

// forged is a forge of the test's own, and a daemon over an empty
// directory, to be filled from it.
func forged(t *testing.T) (testforge.Forge, clients) {
	t.Helper()
	return testforge.Make(t), serve(t, scripted{}, t.TempDir(), false)
}

func TestARepositoryHeldWholeIsBuiltModuleByModule(t *testing.T) {
	forge, c := forged(t)
	brought(t, c, nil, taken(forge, "services"))

	if labels := planned(t, c); !strings.Contains(labels, "//services/hello:go.compile") || !strings.Contains(labels, "//services/greet:go.compile") {
		t.Errorf("a build plans %s, want both modules of the repository", labels)
	}
}

func TestSeveralRepositoriesHeldWholeAreEachBuilt(t *testing.T) {
	forge, c := forged(t)
	brought(t, c, []record.Located{component("gateway", "platform"), component("greet", "services")}, taken(forge, "platform"), taken(forge, "services"))

	if labels := planned(t, c); !strings.Contains(labels, "//platform/gateway:go.compile") || !strings.Contains(labels, "//services/greet:go.compile") {
		t.Errorf("a build plans %s, want modules of both repositories", labels)
	}
}

func TestOnlyTheDirectoriesTakenOfEachRepositoryAreBuilt(t *testing.T) {
	forge, c := forged(t)
	broughtGateway(t, c, forge)

	labels := strings.Fields(planned(t, c))
	if len(labels) != 6 {
		t.Errorf("a build plans %v, want the three actions of gateway and of greet and no others", labels)
	}
}

func TestADirectoryOfEntitiesInSeveralRepositoriesBuildsOnTheGrid(t *testing.T) {
	grid := reapitest.Dial(t)
	forge := testforge.Make(t)
	c := serve(t, grid, t.TempDir(), false)
	ended := ranGateway(t, c, forge)

	if ended.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS || ended.GetCounts().GetPassed() != 6 {
		actions, _ := c.runs.ListBuildActions(context.Background(), &buildv1.ListBuildActionsRequest{BuildId: ended.GetId()})
		t.Fatalf("the run ended %s (%s) with %d passed, want SUCCESS and 6: %v", ended.GetExitCode(), ended.GetFailure(), ended.GetCounts().GetPassed(), actions.GetActions())
	}
}

// A directory that comes to hold more is built as it is then, by the
// daemon that built it before: what its manifest records after a build
// is of the next one, a repository more and the first as it was. The
// control is the build before, of the first repository alone.
func TestADirectoryThatHoldsMoreIsBuiltAsItIsThen(t *testing.T) {
	forge := testforge.Make(t)
	c := serve(t, scripted{}, t.TempDir(), false)
	platform, services := taken(forge, "platform"), taken(forge, "services")
	platform.Roots, services.Roots = []string{"gateway"}, []string{"greet"}
	root := brought(t, c, nil, services)
	first, _, err := manifest.Read(root, chosen.System().Revision)
	if err != nil {
		t.Fatal(err)
	}
	if labels := strings.Fields(planned(t, c)); len(labels) != 3 {
		t.Fatalf("the control: a build of greet alone plans %v, want its three actions", labels)
	}

	brought(t, c, nil, platform)
	more, _, err := manifest.Read(root, chosen.System().Revision)
	if err != nil {
		t.Fatal(err)
	}
	more.Repositories = append(first.Repositories, more.Repositories...)
	if err := manifest.Write(root, more); err != nil {
		t.Fatal(err)
	}
	if labels := strings.Fields(planned(t, c)); len(labels) != 6 {
		t.Errorf("the same daemon, its directory holding gateway too, plans %v, want the three actions of each", labels)
	}
}

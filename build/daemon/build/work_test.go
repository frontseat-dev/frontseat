package build

import (
	"os"
	"path/filepath"
	"testing"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
)

// The work of a fix, its tool trees and copies, is kept beside the runs,
// and whatever a daemon died holding is cleared by the daemon after it;
// a daemon that stops leaves none of its own. The runs beside it are kept.
func TestADaemonClearsTheWorkOfTheDaemonBefore(t *testing.T) {
	config := configuration{Workspace: oneModule(t), Mounted: true, Executor: printing(), Builds: t.TempDir()}
	stale := filepath.Join(config.Builds, "work", "tools", "stale", "bin")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	first := serveAs(t, config)
	if _, err := os.Stat(filepath.Join(config.Builds, "work", "tools", "stale")); !os.IsNotExist(err) {
		t.Fatalf("the work of the daemon before is still there: %v", err)
	}
	run := wait(t, first.runs, create(t, first.runs).GetId())
	first.daemon.Close()
	if _, err := os.Stat(filepath.Join(config.Builds, "work")); !os.IsNotExist(err) {
		t.Fatalf("a daemon that stopped left its work: %v", err)
	}
	second := serveAs(t, config)
	if _, err := second.runs.DescribeBuild(t.Context(), &buildv1.DescribeBuildRequest{BuildId: run.GetId()}); err != nil {
		t.Fatalf("the run beside the work was not kept: %v", err)
	}
}

package build

import (
	"os"
	"path/filepath"
	"testing"
)

// a daemon given its workspace through a link to the directory
// (--workspace /work, where /work is a link) lists nothing of it: the
// build has no action, and passes. The control is the same workspace
// named by its directory.
func TestAWorkspaceGivenThroughALinkIsBuilt(t *testing.T) {
	for name, linked := range map[string]bool{"the control, its directory": false, "a link to its directory": true} {
		t.Run(name, func(t *testing.T) {
			workspace := oneModule(t)
			if linked {
				link := filepath.Join(t.TempDir(), "work")
				if err := os.Symlink(workspace, link); err != nil {
					t.Fatal(err)
				}
				workspace = link
			}
			c := serveAs(t, configuration{Workspace: workspace, Mounted: true, Executor: scripted{}})

			run := wait(t, c.runs, create(t, c.runs).GetId())

			if ran := len(actions(t, c, run)); ran == 0 {
				t.Errorf("the run ended %s (%q) with %d actions; want the module's actions", run.GetExitCode().GetName(), run.GetFailure(), ran)
			}
		})
	}
}

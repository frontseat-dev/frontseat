package tools_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/tools"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/server/testsupport/testpins"
)

// everything a run reads of the source it reads through its
// snapshot. A repository's pins are read from the disk as it is when
// the first action that names a tool is staged: a lock written after
// the run began is what the run's tools are worked out from, and what
// the run says is wrong with it is said of a file the run is not of.
// The installer here answers at once and asks nothing, as a grid that
// holds the pins already does not ask for them.
func TestARunsPinsAreThoseOfItsSnapshot(t *testing.T) {
	for name, edit := range map[string]bool{"the control, the lock as the run found it": false, "the lock written after the run began": true} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			testpins.Go(t, filepath.Join(root, "repo"))
			snapshot, err := new(source.Keeper).Take(context.Background(), source.Tree{Root: root, Paths: map[string]string{"repo": "repo"}})
			if err != nil {
				t.Fatal(err)
			}
			if edit {
				if err := os.WriteFile(filepath.Join(root, "repo", "mise.lock"), []byte("# its author is half way through writing it\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			p := &tools.Provisioner{Executor: installing{}, Platform: testgrid.Platform()}

			_, err = p.Use(context.Background(), exec.Action{Label: "//repo/module:compile", Root: root, Files: snapshot, WorkingDirectory: "repo/module", Arguments: []string{"go", "build", "./..."}, Platform: p.Platform}, "repo", []string{"go"}, false, nil)

			var changed *source.Changed
			switch {
			case !edit && err != nil:
				t.Errorf("err %v; want the action given its tool", err)
			case edit && !errors.As(err, &changed):
				t.Errorf("err %v; want mise.lock named as changed: the run began with a lock that pins go", err)
			}
		})
	}
}

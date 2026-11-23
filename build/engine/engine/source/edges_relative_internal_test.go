package source

import "testing"

// A daemon may be told its workspace as ".", its own directory.
// What the daemon keeps of a file that has gone is then never let go:
// the keeper knows a file by "name" and looks for those beneath "./".
func TestWhatIsKnownOfAFileThatIsGoneIsLetGoWhereverTheWorkspaceIs(t *testing.T) {
	for name, relative := range map[string]bool{"the control, a workspace named in full": false, "the workspace named as the daemon's own directory": true} {
		t.Run(name, func(t *testing.T) {
			tree, file := one(t)
			if relative {
				t.Chdir(tree.Root)
				tree.Root, file = ".", "file"
			}
			forgetsWhatIsGone(t, tree, file)
		})
	}
}

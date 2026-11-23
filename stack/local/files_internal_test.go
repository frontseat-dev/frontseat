package local

import (
	"bytes"
	"github.com/frontseat-dev/frontseat/testsupport/testmodule"
	"os"
	"path/filepath"
	"testing"
)

// What frontseat-server carries of the stack is what the box reads of it
// from a checkout: Read names the stack's files by their paths from the
// repository's root, and this package embeds them by its own. Were either
// moved or renamed without the other, one reader would read another file,
// or none; this holds the two to the same file, byte for byte, read by
// the path Read names it by.
func TestTheFilesCarriedAreTheFilesTheRepositoryHolds(t *testing.T) {
	root, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	read, err := os.ReadFile(filepath.Join(root, runnerImage))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(read, Embedded.Runner) {
		t.Error("the files embedded are not the files Read reads")
	}
}

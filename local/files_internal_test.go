package local

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// What frontseat-local carries of the stack is what the box reads of it
// from a checkout: Read names the stack's files by their paths from the
// repository's root, and this package embeds them by its own. Were either
// moved or renamed without the other, one reader would read another file,
// or none; this holds the two to the same file, byte for byte, read from
// this package's own directory, which is the module's.
func TestTheFilesCarriedAreTheFilesTheRepositoryHolds(t *testing.T) {
	own, found := strings.CutPrefix(runnerImage, "local/")
	if !found {
		t.Fatalf("%s is not beneath this module, which embeds it", runnerImage)
	}
	read, err := os.ReadFile(own)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(read, Embedded.Runner) {
		t.Error("the files embedded are not the files Read reads")
	}
}

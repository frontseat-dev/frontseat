package download_test

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/exec/download"
)

// Where a part of a download is not what the lock says, the action
// fails and leaves nothing: neither that part, nor one cut and checked
// before it, nor the directories made for either.
func TestCuttingLeavesNothingWhereAPartIsNotWhatTheLockSays(t *testing.T) {
	directory, whole := downloadedWhole(t)
	sum := sha1.Sum(whole[10:25])
	control := hex.EncodeToString(sum[:])
	command := download.Cutting(
		"whole", "10", "15", "sha1", control, "kept/first", "https://example.com/x.apk",
		"whole", "0", "3", "sha1", control, "kept/second", "https://example.com/x.apk",
	)
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = directory
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("the control: a part that is not what its checksum says passed: %s", out)
	}
	if _, err := os.Stat(filepath.Join(directory, "kept/first")); err == nil {
		t.Error("kept/first is left, though the fetch failed at a later part")
	}
}

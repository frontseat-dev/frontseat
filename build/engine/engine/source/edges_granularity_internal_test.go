package source

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// a file written, read by a run, and written again at once with
// content of the same size. How a file looks tells the two apart only
// where the file system's clock moved between the writes. Where it did
// not, the daemon goes by what it kept (the next run is sent the file
// as it was), and the run under way does not see the change.
//
// REVIEW_DIR names the directory to write in, so that a file system can
// be chosen; the test's own otherwise.
func TestAFileWrittenAgainAtOnceIsSeenToHaveChanged(t *testing.T) {
	root := os.Getenv("REVIEW_DIR")
	if root == "" {
		root = t.TempDir()
	} else {
		var err error
		if root, err = os.MkdirTemp(root, "granularity-"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(root) })
	}
	file := filepath.Join(root, "file")
	tree := Tree{Root: root, Paths: map[string]string{"": "."}}
	keeper := &Keeper{}
	const rounds = 3000
	kept, unseen := 0, 0
	for i := range rounds {
		first, second := fmt.Sprintf("round %06d, as the run found it", i), fmt.Sprintf("round %06d, as it was left since", i)
		if err := os.WriteFile(file, []byte(first), 0o644); err != nil {
			t.Fatal(err)
		}
		run := take(t, keeper, tree)
		if _, _, _, err := run.File(file); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(second), 0o644); err != nil {
			t.Fatal(err)
		}
		// The run under way: the file is no longer what it read.
		var changed *Changed
		if _, _, _, err := run.File(file); !errors.As(err, &changed) {
			unseen++
		}
		// The run after: it is sent the file as it is.
		next := take(t, keeper, tree)
		sum := sha256.Sum256([]byte(second))
		if hash, _, _, err := next.File(file); err != nil || hash != hex.EncodeToString(sum[:]) {
			kept++
		}
	}
	t.Logf("%s: %d rounds", runtime.GOOS, rounds)
	if unseen > 0 {
		t.Errorf("in %d of %d rounds the run under way did not see that the file had changed", unseen, rounds)
	}
	if kept > 0 {
		t.Errorf("in %d of %d rounds the run after was told the file holds what it held before it was written again", kept, rounds)
	}
}

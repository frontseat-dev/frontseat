// Package testcommits makes the history a test is given: it runs git in a
// directory as the one who writes a fixture's commits, and fails the test
// where git fails.
package testcommits

import (
	"os/exec"
	"strings"
	"testing"
)

// Git runs git in a directory, as whoever made the repository would, and
// returns what it printed, its surrounding space trimmed.
func Git(t testing.TB, directory string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.com"}, args...)...)
	cmd.Dir = directory
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// First makes a directory a git repository with one commit on main, of
// everything it holds.
func First(t testing.TB, directory string) {
	t.Helper()
	Git(t, directory, "init", "--quiet", "--initial-branch", "main")
	Git(t, directory, "add", ".")
	Git(t, directory, "commit", "--quiet", "-m", "first")
}

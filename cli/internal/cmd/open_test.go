package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// What a checkout's remote does not hold is said in a person's words,
// and nothing is said where it holds it all.
func TestWhatTheWorkspaceDoesNotHoldIsSaidInWords(t *testing.T) {
	for _, c := range []struct {
		unpushed, uncommitted int
		want                  string
	}{
		{0, 0, ""},
		{1, 0, "1 commit here is not pushed"},
		{3, 0, "3 commits here are not pushed"},
		{0, 1, "1 file is not committed"},
		{0, 4, "4 files are not committed"},
		{2, 1, "2 commits here are not pushed, and 1 file is not committed"},
	} {
		if got := notHeld(c.unpushed, c.uncommitted); got != c.want {
			t.Errorf("%d unpushed, %d uncommitted: %q, want %q", c.unpushed, c.uncommitted, got, c.want)
		}
	}
}

// A shell opened on a workspace begins in the repository's directory
// where the daemon's name for it is one directory's, written plainly; a
// name that is anything else, which a shell would read as more than a
// name, is gone to by no one.
func TestAShellOpenedOnAWorkspaceBeginsInItsRepository(t *testing.T) {
	const login = `if command -v bash >/dev/null 2>&1; then exec bash -l; else exec sh -l; fi`
	// A name that begins with a dash, or is one, is a directory's name:
	// it is written from where the shell is, and after what ends options.
	for _, name := range []string{"frontseat-examples", "gtk+", "-", "-x", "--", "a.b_c"} {
		if got, want := entering(name), "cd -- ./"+name+" && "+login; got != want {
			t.Errorf("%q: %q, want %q", name, got, want)
		}
	}
	for _, name := range []string{"", ".", "..", "a/b", "/etc", "a b", "a;id", "$(id)", "a`id`", "a'b", `a"b`, "a\nb", "-x y", "a&&id", "~"} {
		if got := entering(name); got != login {
			t.Errorf("%q: the shell would run %q, want it to go nowhere", name, got)
		}
	}
}

// No catalog is named on the command line: the control plane resolves
// entities through its own. --catalog is an unknown flag of open and of
// add; the control is --whole, a flag of both.
func TestACatalogIsNamedOnTheCommandLineNoMore(t *testing.T) {
	for _, command := range []*cobra.Command{openCmd, addCmd} {
		if flag := command.Flags().Lookup("catalog"); flag != nil {
			t.Errorf("%s takes --catalog", command.Name())
		}
		if err := command.Flags().Parse([]string{"--catalog", "https://example.com/catalog-info.yaml"}); err == nil || !strings.Contains(err.Error(), "unknown flag: --catalog") {
			t.Errorf("%s --catalog: %v, want it an unknown flag", command.Name(), err)
		}
		if command.Flags().Lookup("whole") == nil {
			t.Errorf("the control: %s takes no --whole", command.Name())
		}
	}
}

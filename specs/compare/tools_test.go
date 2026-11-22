package compare_test

// What the comparison holds of the tools it compares, proven without
// running it: none of these runs the comparison or touches the grid.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/testsupport/testworkload"
)

// pretended is a tool whose builds a test decides: each build prints how
// many actions it ran.
func pretended(ran func(build int) int) tool {
	build := 0
	return tool{
		name:  "pretended",
		build: func(*testing.T) string { build++; return fmt.Sprint(ran(build)) },
		ran:   func(_ *testing.T, printed string) string { return printed },
	}
}

// A tool that never sees the change (it kept what the file held and did
// not look again) runs nothing in the build timed nor in the one after,
// and would be the quickest in the table: it is said to have missed the
// change, as one that builds it a build late is. The control builds it
// at once, and is not.
func TestAToolThatNeverBuildsTheChangeIsSaidToHaveMissedIt(t *testing.T) {
	for name, c := range map[string]struct {
		ran  func(build int) int
		want bool
	}{
		// Builds come in pairs: the one timed, and the one after it.
		"control: it builds the change at once":      {func(build int) int { return 3 * (build % 2) }, false},
		"control: it builds the change a build late": {func(build int) int { return 3 * ((build + 1) % 2) }, true},
		"it never builds the change":                 {func(int) int { return 0 }, true},
	} {
		t.Run(name, func(t *testing.T) {
			changes := 0
			row := said(pretended(c.ran).several(t, "3", func(int) { changes++ }))
			if got := strings.Contains(row, "MISSED"); got != c.want {
				t.Errorf("%d changes were made; the table says %q; want it to say the change was missed: %v", changes, row, c.want)
			}
		})
	}
}

// fake writes a program that says a version, records where and how it
// was run, and prints nothing a count can be read from.
func fake(t *testing.T, name string) (program, log string) {
	t.Helper()
	dir := t.TempDir()
	program, log = filepath.Join(dir, name), filepath.Join(dir, "log")
	script := "#!/bin/sh\necho \"$PWD|$*\" >> " + log + "\n[ \"$1\" = --version ] && echo " + name + " 0.0.0\nexit 0\n"
	if err := os.WriteFile(program, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return program, log
}

// When the comparison is over, Bazel's output is expunged (`bazel clean
// --expunge`) and its server shut down in the workspace the comparison
// wrote, where it built, and never in the directory the test runs in:
// there Bazel would refuse and the output base of every comparison stay
// on the machine, or, beneath a Bazel workspace, that workspace's output
// would be expunged.
func TestBazelIsCleanedWhereItBuilt(t *testing.T) {
	program, log := fake(t, "bazel")
	t.Setenv("COMPARE_BAZEL", program)
	t.Run("the comparison", func(t *testing.T) {
		tl := bazel(t, testworkload.Shape{Modules: 1, Group: 1})
		tl.build(t)
	})
	ran, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	where := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(ran)), "\n") {
		dir, args, _ := strings.Cut(line, "|")
		// One directory, whatever link it was reached through (macOS).
		dir = strings.TrimPrefix(dir, "/private")
		where[args] = dir
	}
	built := where["build //..."]
	if built == "" {
		t.Fatalf("no build was run:\n%s", ran)
	}
	// The control: the server is shut down where it built.
	if where["shutdown"] != built {
		t.Errorf("bazel shutdown ran in %s; it built in %s", where["shutdown"], built)
	}
	if where["clean --expunge"] != built {
		t.Errorf("bazel clean --expunge ran in %s; it built in %s", where["clean --expunge"], built)
	}
}

// What a tool printed that no count can be read from is never read as
// "ran 0", which is what a build with nothing to do says, and what says
// the build before missed nothing: Buck2's reader says so as Bazel's,
// the control, does.
func TestWhatCannotBeReadIsNotReadAsNothingRan(t *testing.T) {
	for name, make := range map[string]func(*testing.T, testworkload.Shape) tool{"control: Bazel": bazel, "Buck2": buck2} {
		t.Run(name, func(t *testing.T) {
			program, _ := fake(t, strings.ToLower(strings.TrimPrefix(name, "control: ")))
			t.Setenv("COMPARE_BAZEL", program)
			t.Setenv("COMPARE_BUCK2", program)
			tl := make(t, testworkload.Shape{Modules: 1, Group: 1})
			if got := tl.ran(t, "something went differently, and no count is printed\n"); got == "0" {
				t.Errorf("what holds no count is read as %q actions run", got)
			}
		})
	}
}

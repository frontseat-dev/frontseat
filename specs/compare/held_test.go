package compare_test

// Tests written by an independent review of fe67226..f74db45. None of
// them runs the comparison or touches the grid.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// "Missed the change" is found only where the build after
// finds something to run. A tool that never sees the change (it kept
// what the file held and did not look again) runs nothing in either
// build, is the quickest in the table, and nothing says it built
// nothing: no case is held to the actions it must run (3, or 30).
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

// When the comparison is over, `bazel clean --expunge` is run
// in the directory the test runs in (specs/compare/, in this repository), not
// in the workspace it wrote: Bazel refuses there, the error is dropped,
// and the output base of every comparison stays on the machine. Were
// this checkout beneath a Bazel workspace, it is that workspace's
// output that would be expunged. `shutdown`, two lines below, is given
// the directory.
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

// What Buck2 printed that no count can be read from is read as
// "ran 0": the same as a build with nothing to do, and the value that
// says the build before missed nothing. Bazel's and frontseat's readers
// say "?" there.
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

// The Bazel that is timed is whatever `bazel` is on the path.
// Where that is Bazelisk (Homebrew's is), the workspace the generator
// writes pins no Bazel, so every command first asks the network which
// Bazel is the latest: about a second of each "build" is that. The
// control tells Bazelisk the version, as a .bazelversion in the
// workspace would.
func TestTheBazelTimedIsBazelAndNotItsLauncherAskingTheNetwork(t *testing.T) {
	path, runs := program("COMPARE_BAZEL", "bazel")
	if !runs {
		t.Skip("no bazel here")
	}
	if resolved, _ := filepath.EvalSymlinks(mustLook(t, path)); !strings.Contains(resolved, "bazelisk") {
		t.Skipf("%s is not Bazelisk", resolved)
	}
	// Which Bazel it is, asked as the comparison asks: once, outside the
	// workspace.
	asked, err := exec.Command(path, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := testworkload.Bazel(dir, testworkload.Shape{Modules: 1, Group: 1}, strings.TrimPrefix(strings.TrimSpace(string(asked)), "bazel "), nil); err != nil {
		t.Fatal(err)
	}
	started := func(env ...string) (time.Duration, string) {
		quickest, version := time.Hour, ""
		for range 3 {
			cmd := exec.Command(path, "--version")
			cmd.Dir, cmd.Env = dir, append(os.Environ(), env...)
			start := time.Now()
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			quickest, version = min(quickest, time.Since(start)), strings.TrimSpace(string(out))
		}
		return quickest, version
	}
	asIs, version := started()
	told, _ := started("USE_BAZEL_VERSION=" + strings.TrimPrefix(version, "bazel "))
	t.Logf("%s starts in %s in the generated workspace, and in %s told its version", version, asIs, told)
	if asIs > told+100*time.Millisecond {
		t.Errorf("every bazel command in the generated workspace takes %s before Bazel is reached, %s when the workspace says which Bazel it is: the difference is in every time the comparison reports for Bazel", asIs, told)
	}
}

func mustLook(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

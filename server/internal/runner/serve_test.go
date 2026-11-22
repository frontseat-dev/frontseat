package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	bbrunner "github.com/buildbarn/bb-remote-execution/pkg/proto/runner"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/frontseat-dev/frontseat/server/internal/confine"
)

// aForm is a confiner's form, as its name holds it.
var aForm = strings.Repeat("ab", 32)

// A command is run only where it begins with a confiner of a form, then
// a spec, then the way up to the action's own input root. The control is
// that command, which is run.
func TestOnlyAConfinedCommandIsRun(t *testing.T) {
	const confiners = "/bb/frontseat-action-"
	root, dir := "/slot/build/x/root", "/slot/build/x/root/repo/module"
	control := []string{confiners + aForm, "{}", "../..", "go", "build"}
	if err := confined(confiners, control, dir, root); err != nil {
		t.Fatalf("a confined command was refused: %v", err)
	}
	for name, arguments := range map[string][]string{
		"another program":            {"/bin/sh", "-c", "anything", "x"},
		"a confiner of no form":      {confiners + "x", "{}", "../..", "go"},
		"a confiner elsewhere":       {"/tmp/frontseat-action-" + aForm, "{}", "../..", "go"},
		"a confiner told no spec":    {confiners + aForm, "install", "/bb", "x"},
		"a confiner told a word":     {confiners + aForm, "at", "/", "x"},
		"a way up that is no way up": {confiners + aForm, "{}", "../x", "go"},
		"a way up beyond the root":   {confiners + aForm, "{}", "../../..", "go"},
		"a way up short of the root": {confiners + aForm, "{}", "..", "go"},
		"a confiner with no command": {confiners + aForm, "{}", "../.."},
		"nothing":                    nil,
	} {
		if err := confined(confiners, arguments, dir, root); err == nil {
			t.Errorf("%s was run: %q", name, arguments)
		}
	}
}

// runningOn is a runner over a build directory of the test's, whose
// confiners are a program of the test's that runs what it is told after
// the spec and the way up as its child, as a confiner does once it has
// confined it.
func runningOn(t *testing.T) (*Confined, string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the runner runs on Linux alone")
	}
	if err := subreaper(); err != nil {
		t.Fatal(err)
	}
	build := t.TempDir()
	if err := os.MkdirAll(filepath.Join(build, "x", "root"), 0o755); err != nil {
		t.Fatal(err)
	}
	confiners := filepath.Join(t.TempDir(), "frontseat-action-")
	if err := os.WriteFile(confiners+aForm, []byte("#!/bin/sh\nshift 2\n\"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Confined{Build: build, Confiners: confiners, Temporaries: t.TempDir()}, confiners + aForm
}

// The action runs where it is told, with what it is told, its output
// where the worker reads it and its exit status answered; a command that
// is not confined is refused and not run.
func TestTheRunnerRunsAConfinedActionAndRefusesAnother(t *testing.T) {
	r, confiner := runningOn(t)
	run := func(output string, arguments ...string) (*bbrunner.RunResponse, error) {
		return r.Run(context.Background(), &bbrunner.RunRequest{
			Arguments: arguments, EnvironmentVariables: map[string]string{"SAID": "it"},
			InputRootDirectory: "x/root", StdoutPath: "x/" + output + ".out", StderrPath: "x/" + output + ".err",
		})
	}
	done, err := run("control", confiner, "{}", ".", "/bin/sh", "-c", `echo "$SAID in $(pwd)"; exit 3`)
	if err != nil || done.GetExitCode() != 3 {
		t.Fatalf("the action ended %v %v, want exit 3", done, err)
	}
	if said, _ := os.ReadFile(filepath.Join(r.Build, "x", "control.out")); strings.TrimSpace(string(said)) != "it in "+filepath.Join(r.Build, "x", "root") {
		t.Errorf("the action said %q", said)
	}
	_, err = run("refused", "/bin/sh", "-c", "echo ran > ran", "x")
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("a command with no confiner was answered %v, want refused", err)
	}
	if _, err := os.Stat(filepath.Join(r.Build, "x", "root", "ran")); err == nil {
		t.Error("a command with no confiner ran")
	}
}

// What an action killed before it could empty its temporary directory
// left there is gone before the next action runs, whatever its age: one
// action runs at a time, and the next reads none of it. The control is
// a directory not named as an action's, which stays.
func TestATemporaryDirectoryAnActionLeftIsGoneBeforeTheNextRuns(t *testing.T) {
	r, confiner := runningOn(t)
	for _, name := range []string{confine.TemporaryPrefix + "killed", "somebody-else"} {
		if err := os.MkdirAll(filepath.Join(r.Temporaries, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	check := `test ! -e "$AT/` + confine.TemporaryPrefix + `killed" && test -d "$AT/somebody-else" && echo cleared`
	done, err := r.Run(context.Background(), &bbrunner.RunRequest{
		Arguments: []string{confiner, "{}", ".", "/bin/sh", "-c", check}, EnvironmentVariables: map[string]string{"AT": r.Temporaries},
		InputRootDirectory: "x/root", StdoutPath: "x/out", StderrPath: "x/err",
	})
	if err != nil || done.GetExitCode() != 0 {
		said, _ := os.ReadFile(filepath.Join(r.Build, "x", "out"))
		t.Fatalf("the next action found what the killed one left, or the control gone: %q %v %v", said, done, err)
	}
}

// Nothing an action started runs once the runner has answered: not what
// it left in the background, nor what forked again and again, nor what
// outlived the confiner, killed. The control is the action's own word
// that each was running as it ended.
func TestNothingAnActionStartedOutlivesItsRun(t *testing.T) {
	r, confiner := runningOn(t)
	const left = `sleep 60 >/dev/null 2>&1 & echo $! > left
sh -c 'while :; do sh -c "sleep 60" >/dev/null 2>&1 & sleep 0.01; done' >/dev/null 2>&1 & echo $! > hopping
sleep 0.3; kill -0 $(cat left) && kill -0 $(cat hopping) && echo running
kill -KILL $PPID`
	started := time.Now()
	done, err := r.Run(context.Background(), &bbrunner.RunRequest{
		Arguments: []string{confiner, "{}", ".", "/bin/sh", "-c", left}, InputRootDirectory: "x/root", StdoutPath: "x/out", StderrPath: "x/err",
	})
	if err != nil {
		t.Fatal(err)
	}
	if said, _ := os.ReadFile(filepath.Join(r.Build, "x", "out")); !strings.Contains(string(said), "running") {
		t.Fatalf("the action did not leave its processes running, so this test proves nothing: %q (exit %d)", said, done.GetExitCode())
	}
	if took := time.Since(started); took > 30*time.Second {
		t.Fatalf("the run took %s", took)
	}
	if beneath := descendants("/proc", os.Getpid()); len(beneath) > 0 {
		t.Errorf("what the action started runs on after it: %v", beneath)
	}
	for _, name := range []string{"left", "hopping"} {
		pid, _ := os.ReadFile(filepath.Join(r.Build, "x", "root", name))
		if n, err := strconv.Atoi(strings.TrimSpace(string(pid))); err == nil {
			if _, err := os.Stat("/proc/" + strconv.Itoa(n)); err == nil {
				t.Errorf("the action's %s process, %d, runs on", name, n)
			}
		}
	}
}

// Where the action runs is reached from its root as it is written: one
// reached through a link in the input tree, which would take the
// confiner's root elsewhere, is refused, and nothing is run. The control
// is a directory of its own, whose action is run. The action is told
// where it runs as the runner put it there, whatever the request says.
func TestWhereAnActionRunsIsWhatTheRunnerChecked(t *testing.T) {
	r, confiner := runningOn(t)
	root := filepath.Join(r.Build, "x", "root")
	if err := os.Mkdir(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../..", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	run := func(output, dir string) (*bbrunner.RunResponse, error) {
		return r.Run(context.Background(), &bbrunner.RunRequest{
			Arguments: []string{confiner, "{}", "..", "/bin/sh", "-c", `echo "$PWD" > ` + filepath.Join(r.Build, "x", output+".said")}, WorkingDirectory: dir,
			EnvironmentVariables: map[string]string{"PWD": "/proc/self/cwd"},
			InputRootDirectory:   "x/root", StdoutPath: "x/" + output + ".out", StderrPath: "x/" + output + ".err",
		})
	}
	if _, err := run("control", "real"); err != nil {
		t.Fatalf("a directory of the root's own was refused: %v", err)
	}
	if said, _ := os.ReadFile(filepath.Join(r.Build, "x", "control.said")); strings.TrimSpace(string(said)) != filepath.Join(root, "real") {
		t.Errorf("the action was told it runs in %q, want where the runner put it", said)
	}
	if _, err := run("linked", "link"); status.Code(err) != codes.PermissionDenied {
		t.Errorf("a directory reached through a link was answered %v, want refused", err)
	}
	if _, err := os.Stat(filepath.Join(r.Build, "x", "linked.said")); err == nil {
		t.Error("an action whose directory is reached through a link ran")
	}
}

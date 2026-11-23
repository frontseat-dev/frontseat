package confine

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The test binary is frontseat-action where a test runs it so: what it
// does is what the program does, without building it.
func TestMain(m *testing.M) {
	// Run as an action's command, it connects to the socket named, and
	// says whether it could.
	if at := os.Getenv("FRONTSEAT_ACTION_CONNECT"); at != "" && os.Getenv("FRONTSEAT_ACTION_TEST") == "" {
		c, err := net.Dial("unix", at)
		if err != nil {
			fmt.Println("refused")
			os.Exit(0)
		}
		c.Close()
		fmt.Println("connected")
		os.Exit(0)
	}
	if os.Getenv("FRONTSEAT_ACTION_TEST") == "1" {
		os.Unsetenv("FRONTSEAT_ACTION_TEST")
		temporaries = os.Getenv("FRONTSEAT_ACTION_TEST_TEMPORARIES")
		os.Unsetenv("FRONTSEAT_ACTION_TEST_TEMPORARIES")
		Run()
	}
	// Asked its form, as Install asks what it finds, it answers as the
	// program does: a copy of it installed is a confiner of this form.
	if len(os.Args) == 2 && os.Args[1] == "form" {
		Run()
	}
	// The actions these tests run make their temporary directories in a
	// directory of this test's own: run as an action itself, as on the
	// grid, a test may write nothing else of Temporaries.
	own, err := os.MkdirTemp("", "temporaries")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	temporaries = own
	code := m.Run()
	os.RemoveAll(own)
	os.Exit(code)
}

func encoded(t *testing.T, spec Spec) string {
	t.Helper()
	out, err := Encode(spec)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func valid() Spec {
	return Spec{Home: ".frontseat/home", Temporary: ".frontseat/tmp"}
}

// What it is told is read before anything confines it, from a command
// any client of the grid may write: a spec of another form, one that
// names what it does not read, or one that would make a directory
// anywhere but beneath the root, is refused. The control is the spec
// the daemon writes.
func TestASpecThatIsNotWhatTheDaemonWritesIsRefused(t *testing.T) {
	if _, err := read(encoded(t, valid())); err != nil {
		t.Fatalf("the daemon's spec is refused: %v", err)
	}
	other := strings.Replace(encoded(t, valid()), Form, strings.Repeat("0", len(Form)), 1)
	for way, given := range map[string]string{
		"for another confiner":     other,
		"naming what it reads not": strings.Replace(encoded(t, valid()), `{`, `{"shell":"rm -rf /",`, 1),
		"not a spec":               "echo hello",
		"empty":                    "",
	} {
		if _, err := read(given); err == nil {
			t.Errorf("a spec %s is read: %q", way, given)
		}
	}
	for _, dir := range []string{"", "..", "../x", "/tmp", "a/../../b"} {
		home := valid()
		home.Home = dir
		temporary := valid()
		temporary.Temporary = dir
		for _, spec := range []Spec{home, temporary} {
			if _, err := read(encoded(t, spec)); err == nil {
				t.Errorf("a spec that makes %q is read", dir)
			}
		}
	}
}

// The way up to the root is up and nothing else: one that goes down,
// or anywhere at all, is refused before anything is made. The control is
// a way up, which makes the action's home in the root.
func TestTheWayUpToTheRootGoesUpAlone(t *testing.T) {
	spec := encoded(t, valid())
	for _, up := range []string{"", "x", "../x", "..//..", "/", "./x", "../../etc"} {
		dir := t.TempDir()
		t.Chdir(dir)
		err := Main([]string{spec, up, "/bin/true"})
		if err == nil || !strings.Contains(err.Error(), "the way up") {
			t.Errorf("the way up %q was taken: %v", up, err)
		}
		if made, _ := os.ReadDir(dir); len(made) > 0 {
			t.Errorf("the way up %q made something before it was refused", up)
		}
	}
	for _, up := range []string{".", "..", "../.."} {
		root := filepath.Join(t.TempDir(), "a", "b")
		here := filepath.Join(root, "c", "d")
		if err := os.MkdirAll(here, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(here)
		a, err := prepare([]string{spec, up, "/usr/bin/true"})
		if err != nil {
			t.Errorf("the way up %q was refused: %v", up, err)
		}
		real, err := filepath.EvalSymlinks(here)
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(real, up, ".frontseat", "home"); !slices.Contains(a.made, want) {
			t.Errorf("the way up %q makes %q, not a home in the root", up, a.made)
		}
	}
}

// It runs the command with the environment it is told, in the
// order it is told it: its home and temporary directory beneath the
// root, each value joined by the root where it was cut, a name unset
// where it is told so, a later one replacing an earlier, and the rest of
// what it was started with kept. What a value holds is never read by a
// shell.
func TestTheCommandRunsWithTheEnvironmentItIsTold(t *testing.T) {
	// The root as the program finds it, from where it runs: a temporary
	// directory may be reached through a link.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	spec := valid()
	spec.Environment = []Variable{
		{Name: "ROOTED", Parts: []string{"", "/a:", "/b"}},
		{Name: "QUOTED", Parts: []string{`it's "$HOME" $(echo ran)`}},
		{Name: "LINES", Parts: []string{"one\ntwo"}},
		{Name: "EMPTY", Parts: []string{""}},
		{Name: "GONE"},
		{Name: "LATER", Parts: []string{"first"}},
		{Name: "LATER", Parts: []string{"second"}},
	}
	t.Setenv("GONE", "still here")
	t.Setenv("KEPT", "the worker's")
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Chdir(root)
	a, err := prepare([]string{encoded(t, spec), ".", "/usr/bin/env"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, each := range a.environment {
		name, value, _ := strings.Cut(each, "=")
		got[name] = value
	}
	want := map[string]string{
		"HOME":   filepath.Join(root, ".frontseat/home"),
		"TMPDIR": filepath.Join(root, ".frontseat/tmp"),
		"ROOTED": root + "/a:" + root + "/b",
		"QUOTED": `it's "$HOME" $(echo ran)`,
		"LINES":  "one\ntwo",
		"EMPTY":  "",
		"LATER":  "second",
		"KEPT":   "the worker's",
		"PATH":   "/usr/bin:/bin",
	}
	for name, value := range want {
		if have, set := got[name]; !set || have != value {
			t.Errorf("%s is %q (set %v), want %q", name, have, set, value)
		}
	}
	if _, set := got["GONE"]; set {
		t.Errorf("GONE was told to be unset and is %q", got["GONE"])
	}
}

// A command's first word is found as a shell finds it, on the path it
// is given, not the one the program was started with; a word with a
// slash is a path, and a file that is not a program is passed by.
func TestTheCommandIsFoundOnThePathItIsGiven(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "tool")
	if err := os.WriteFile(program, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(plain, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if found, err := find("tool", filepath.Dir(plain)+":"+dir); err != nil || found != program {
		t.Errorf("tool was found at %q (%v), want %q", found, err, program)
	}
	if found, err := find("./x/tool", ""); err != nil || found != "./x/tool" {
		t.Errorf("a path was looked for: %q %v", found, err)
	}
	for _, word := range []string{"", "nothing-of-the-name"} {
		if found, err := find(word, dir); err == nil {
			t.Errorf("%q was found at %q", word, found)
		}
	}
}

// The same spec is the same bytes, so an action's key is the same from
// one run to the next; and the command the grid runs is the program,
// the spec, the way up and the command, in that order.
func TestTheSameSpecIsTheSameCommand(t *testing.T) {
	spec := valid()
	spec.Environment = []Variable{{Name: "A", Parts: []string{"1"}}}
	first, second := encoded(t, spec), encoded(t, spec)
	if first != second {
		t.Errorf("one spec is two commands: %s, %s", first, second)
	}
	var back Spec
	if err := json.Unmarshal([]byte(first), &back); err != nil || back.Form != Form || len(Form) != 64 {
		t.Errorf("the spec is not of this program's form: %v %q", err, back.Form)
	}
	if got := Arguments(first, "..", []string{"go", "test"}); !slices.Equal(got, []string{Confiner, first, "..", "go", "test"}) {
		t.Errorf("the command is %q", got)
	}
}

// The command is told where it runs, as the shell that ran it told it,
// whatever its environment said.
func TestTheCommandIsToldWhereItRuns(t *testing.T) {
	root := t.TempDir()
	here := filepath.Join(root, "repo")
	if err := os.MkdirAll(here, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := valid()
	spec.Environment = []Variable{{Name: "PWD", Parts: []string{"/elsewhere"}}}
	t.Setenv("PWD", "/somewhere")
	t.Chdir(here)
	a, err := prepare([]string{encoded(t, spec), "..", "/usr/bin/printenv", "PWD"})
	if err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(here)
	if err != nil {
		t.Fatal(err)
	}
	if got := envOf(a, "PWD"); got != here && got != real {
		t.Errorf("the command is told it runs in %q, and runs in %q", got, here)
	}
}

// envOf is the value a prepared action is given of a name.
func envOf(a prepared, name string) string {
	for _, each := range a.environment {
		if n, value, _ := strings.Cut(each, "="); n == name {
			return value
		}
	}
	return ""
}

// running runs this test binary as frontseat-action in a directory, where
// only Linux runs an action: elsewhere the test is skipped.
func running(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	return runningWith(t, dir, nil, args...)
}

// confiner is this program run as frontseat-action in a directory, with
// what the request's environment adds: by its own path, since it runs
// elsewhere than here, making its actions' temporary directories in this
// test's own.
func confiner(t *testing.T, dir string, env []string, args ...string) *exec.Cmd {
	t.Helper()
	run := exec.Command(self(t), args...)
	run.Dir = dir
	run.Env = append([]string{"FRONTSEAT_ACTION_TEST=1", "FRONTSEAT_ACTION_TEST_TEMPORARIES=" + temporaries, "PATH=/usr/bin:/bin"}, env...)
	return run
}

// self is this program's own path.
func self(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// runningWith is running, with what the request's environment adds.
func runningWith(t *testing.T, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("an action is confined by Landlock and run on Linux alone")
	}
	out, err := confiner(t, dir, env, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// A file of commands with no #! line is run by the shell, as a shell runs
// one, even where its path reads as an option to the shell.
func TestAScriptWithNoInterpreterLineIsRunByTheShell(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "-c"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, at := range []string{"said", "-c/said"} {
		if err := os.WriteFile(filepath.Join(root, at), []byte("echo \"said $1\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if got, err := running(t, root, encoded(t, valid()), ".", "./"+at, "it"); err != nil || got != "said it" {
			t.Errorf("a script at %s with no #! said %q: %v", at, got, err)
		}
	}
}

// The action writes its own root and nothing else of what it runs in: not
// the directory above its root, where the next action's and the runner's
// socket are. The control is a write in its root, which is made.
func TestAnActionWritesItsOwnRootAndNothingAboveIt(t *testing.T) {
	slot := t.TempDir()
	root := filepath.Join(slot, "build", "action")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := running(t, root, encoded(t, valid()), ".", "/bin/sh", "-c", "echo x > mine && cat mine"); err != nil || got != "x" {
		t.Fatalf("a write in the root was refused: %q %v", got, err)
	}
	if _, err := running(t, root, encoded(t, valid()), ".", "/bin/sh", "-c", "echo x > ../../planted"); err == nil {
		t.Error("a write above the root was made")
	}
	if _, err := os.Stat(filepath.Join(slot, "planted")); err == nil {
		t.Error("what the action wrote above its root is there")
	}

}

// An action writes its own temporary directory and nothing else of the
// temporary directories': not their directory, nor another action's.
// The control is a write in its own, which is made.
func TestAnActionWritesItsOwnTemporaryDirectoryAndNoOthers(t *testing.T) {
	root := actionRoot(t)
	other, err := os.MkdirTemp(temporaries, TemporaryPrefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(other) })
	if err := os.Chmod(other, 0o777); err != nil {
		t.Fatal(err)
	}
	escaped := filepath.Join(temporaries, "escaped-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	t.Cleanup(func() { os.Remove(escaped) })
	for _, at := range []string{escaped, filepath.Join(other, "planted")} {
		if _, err := running(t, root, encoded(t, valid()), ".", "/bin/sh", "-c", "echo x > "+at); err == nil {
			t.Errorf("the action wrote %s", at)
		}
		if _, err := os.Stat(at); err == nil {
			t.Errorf("what the action wrote at %s is there", at)
		}
	}
	if got, err := running(t, root, encoded(t, valid()), ".", "/bin/sh", "-c", `echo x > "$TMPDIR/mine" && cat "$TMPDIR/mine"`); err != nil || got != "x" {
		t.Errorf("the control, a write in its own temporary directory, was refused: %q %v", got, err)
	}
}

// An action's temporary directory is its own, beside its root and not
// beneath it, where the worker's mount keeps no file's mode: it writes
// there, what it wrote is gone once it has ended, and the directory is
// swept by an action after. The control is the directory beneath the root
// its spec names, which is made, and is not its TMPDIR.
func TestAnActionsTemporaryDirectoryIsItsOwnAndEmptiedWhenItEnds(t *testing.T) {
	root := actionRoot(t)
	got, err := running(t, root, encoded(t, valid()), ".", "/bin/sh", "-c", `echo x > "$TMPDIR/mine" && cat "$TMPDIR/mine" >/dev/null && echo "$TMPDIR"`)
	if err != nil {
		t.Fatalf("the action could not write its temporary directory: %q %v", got, err)
	}
	if !filepath.IsAbs(got) || strings.HasPrefix(got, root+"/") {
		t.Errorf("the action's TMPDIR is %q, beneath its root %s", got, root)
	}
	if left, err := os.ReadDir(got); err != nil || len(left) != 0 {
		t.Errorf("the action's temporary directory %s holds %v after it ended: %v", got, left, err)
	}
	// What an action after it sweeps, once it is old enough.
	sweep(filepath.Dir(got), time.Now().Add(2*fresh))
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Errorf("the action's temporary directory %s was not swept: %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, valid().Temporary)); err != nil {
		t.Errorf("the control: the directory its spec names beneath the root was not made: %v", err)
	}
}

// Where an action's temporary directory is made is fixed, whatever the
// request's environment says: one that names another directory has
// nothing made there and gains no write to it. The control is a request
// that names none.
func TestAnActionsTemporaryDirectoryIsWhereTheConfinerSaysAndNoWhereTheRequestSays(t *testing.T) {
	root := actionRoot(t)
	elsewhere := t.TempDir()
	for _, env := range [][]string{nil, {"TMPDIR=" + elsewhere}} {
		got, err := runningWith(t, root, env, encoded(t, valid()), ".", "/bin/sh", "-c", `echo "$TMPDIR"`)
		if err != nil {
			t.Fatalf("told %v: %q %v", env, got, err)
		}
		if filepath.Dir(got) != temporaries || !strings.HasPrefix(filepath.Base(got), TemporaryPrefix) {
			t.Errorf("told %v, the action's TMPDIR is %q, not its own in %s", env, got, temporaries)
		}
	}
	if made, _ := os.ReadDir(elsewhere); len(made) != 0 {
		t.Errorf("the directory the request named holds %v", made)
	}
	if _, err := runningWith(t, root, []string{"TMPDIR=" + elsewhere}, encoded(t, valid()), ".", "/bin/sh", "-c", "echo x > "+filepath.Join(elsewhere, "written")); err == nil {
		t.Error("the action wrote the directory its request named")
	}
}

// The action reaches no socket of the directory above its root by its
// path: not the runner's, which would run what it is sent unconfined. The
// control is a socket in its root, which it reaches. Where the kernel
// cannot refuse it (Landlock's ABI 9), the test says so and is skipped.
func TestAnActionReachesNoSocketAboveItsRoot(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("an action is confined by Landlock and run on Linux alone")
	}
	if abi, _, e := syscall.Syscall(landlockCreateRuleset, 0, 0, landlockVersion); e != 0 || abi < 9 {
		t.Skip("the kernel's Landlock cannot refuse a connection to a socket by its path (ABI 9)")
	}
	slot := t.TempDir()
	root := filepath.Join(slot, "build", "action")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for at, want := range map[string]string{filepath.Join(slot, "runner"): "refused", filepath.Join(root, "own"): "connected"} {
		listener, err := net.Listen("unix", at)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			for {
				c, err := listener.Accept()
				if err != nil {
					return
				}
				c.Close()
			}
		}()
		run := confiner(t, root, []string{"FRONTSEAT_ACTION_CONNECT=" + at}, encoded(t, valid()), ".", self(t))
		out, err := run.CombinedOutput()
		listener.Close()
		if got := strings.TrimSpace(string(out)); err != nil || got != want {
			t.Errorf("the action, connecting to %s, was %q (%v); want %s", at, got, err, want)
		}
	}
}

// actionRoot is a root for an action, beneath a slot's build directory as
// a worker's is.
func actionRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "build", "action")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// Nothing the command started runs on once it has ended: not a process
// it left in the background, nor one that left its parent to run on its
// own. The runner sandbox runs the next action next. The control is the
// command's own output, which says each was running as it ended.
func TestNothingAnActionStartedOutlivesIt(t *testing.T) {
	root := actionRoot(t)
	// What it leaves holds none of its output open: the test would wait
	// for it to end, and see nothing left.
	const left = `sleep 60 >/dev/null 2>&1 & echo $! > left; sh -c 'sleep 60 >/dev/null 2>&1 & echo $! > orphaned' & wait $!; sleep 0.2; kill -0 $(cat left) && kill -0 $(cat orphaned) && echo running`
	started := time.Now()
	if got, err := running(t, root, encoded(t, valid()), ".", "/bin/sh", "-c", left); err != nil || got != "running" {
		t.Fatalf("the command did not leave its processes running, so this test proves nothing: %q %v", got, err)
	}
	if time.Since(started) > 30*time.Second {
		t.Fatalf("the action took %s: it waited for what it left", time.Since(started))
	}
	for _, name := range []string{"left", "orphaned"} {
		pid, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		stat, err := os.ReadFile("/proc/" + strings.TrimSpace(string(pid)) + "/stat")
		if err == nil && !strings.Contains(string(stat), ") Z ") {
			t.Errorf("what the action %s runs on after it: %s", name, stat)
		}
	}
}

// The action ends as its command does: its exit status, or, as a shell
// says it, 128 and the number of the signal that ended it, whichever.
func TestTheActionEndsAsItsCommandDoes(t *testing.T) {
	root := actionRoot(t)
	for script, want := range map[string]int{"exit 3": 3, "kill -TERM $$": 128 + 15, "kill -SEGV $$": 128 + 11, "kill -USR1 $$": 128 + 10} {
		_, err := running(t, root, encoded(t, valid()), ".", "/bin/sh", "-c", script)
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != want {
			t.Errorf("%q ended %v, want exit code %d", script, err, want)
		}
	}
}

// The command writes its output through /dev/stdout and /dev/stderr,
// which lead to the files the runner keeps beside the root.
func TestAnActionWritesItsOutputByItsDevices(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "build", "action", "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "linux" {
		t.Skip("an action is confined by Landlock and run on Linux alone")
	}
	stdout, err := os.Create(filepath.Join(filepath.Dir(root), "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	run := confiner(t, root, nil, encoded(t, valid()), ".", "/bin/sh", "-c", "echo said > /dev/stdout")
	run.Stdout = stdout
	var said strings.Builder
	run.Stderr = &said
	if err := run.Run(); err != nil {
		t.Fatalf("%v: %s", err, said.String())
	}
	if wrote, _ := os.ReadFile(stdout.Name()); strings.TrimSpace(string(wrote)) != "said" {
		t.Errorf("the action wrote %q to its output", wrote)
	}
}

// A root at a directory of the system's, or the system's own, is refused:
// what confines the action is the directory it is in.
func TestAnActionsRootIsBelowADirectoryOfTheSystems(t *testing.T) {
	for _, root := range []string{"/", "/slot"} {
		if err := confineTo(root, os.TempDir()); err == nil || !strings.Contains(err.Error(), "below a directory") {
			t.Errorf("a root at %s was taken: %v", root, err)
		}
	}
}

// Where Landlock is not, as on this system where it is not Linux, no
// action is run: frontseat-action refuses rather than run one unconfined.
func TestAnActionIsNotRunUnconfined(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Linux confines an action: the refusal is elsewhere's")
	}
	run := confiner(t, t.TempDir(), nil, encoded(t, valid()), ".", "/usr/bin/true")
	out, err := run.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "Landlock") {
		t.Errorf("an action ran where nothing confines it: %v: %s", err, out)
	}
}

// The program says its form, by which it is installed, and the path a
// daemon names it by holds that form: two forms on one grid are two
// files, and what an action runs by is in its key.
func TestTheConfinerIsNamedByItsForm(t *testing.T) {
	said := exec.Command(os.Args[0], "form")
	said.Env = []string{"FRONTSEAT_ACTION_TEST=1"}
	got, err := said.Output()
	if err != nil || strings.TrimSpace(string(got)) != Form {
		t.Errorf("told form, it said %q (%v), want %q", got, err, Form)
	}
	if Confiner != "/bb/frontseat-action-"+Form || len(Form) != 64 {
		t.Errorf("the confiner is at %q, of form %q", Confiner, Form)
	}
}

// thisProgram returns the bytes of the program the test runs in, which is
// what the confiner installs.
func thisProgram(t *testing.T) []byte {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	return want
}

// It installs itself under the name of its form: installs at once into a
// directory with none leave one whole file and nothing beside it, and
// once it is there it is never replaced, so that an action starting with
// it keeps the file it was told.
func TestTheConfinerInstallsItselfOnceUnderItsForm(t *testing.T) {
	want := thisProgram(t)
	dir := t.TempDir()
	var together sync.WaitGroup
	for range 16 {
		together.Go(func() {
			if _, err := Install(dir); err != nil {
				t.Error(err)
			}
		})
	}
	together.Wait()
	at := filepath.Join(dir, "frontseat-action-"+Form)
	if entries, _ := os.ReadDir(dir); len(entries) != 1 || entries[0].Name() != filepath.Base(at) {
		t.Fatalf("installing at once left %v, want the confiner alone", entries)
	}
	if got, err := os.ReadFile(at); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("the installed confiner is not this program whole: %v", err)
	}
	first, err := os.Stat(at)
	if err != nil || first.Mode()&0o111 == 0 {
		t.Fatalf("the installed confiner is %v, %v", first, err)
	}
	if _, err := Install(dir); err != nil {
		t.Fatal(err)
	}
	if again, err := os.Stat(at); err != nil || !os.SameFile(first, again) {
		t.Errorf("installing again replaced the confiner: %v", err)
	}
}

// What is under its name and is no confiner of its form, a link to
// nowhere, a file cut short, one that may not be run, or a program of
// another form, is replaced with this program, whole. A confiner of this
// form of another build, whose bytes are not these, is left as it is.
func TestWhatIsUnderTheConfinersNameAndIsNotOfItsFormIsReplaced(t *testing.T) {
	want := thisProgram(t)
	for way, plant := range map[string]func(at string) error{
		"a link to nowhere":    func(at string) error { return os.Symlink("/nowhere", at) },
		"a file cut short":     func(at string) error { return os.WriteFile(at, want[:7], 0o755) },
		"a file not a program": func(at string) error { return os.WriteFile(at, want, 0o644) },
		"a program of another form": func(at string) error {
			return os.WriteFile(at, []byte("#!/bin/sh\necho "+strings.Repeat("0", len(Form))+"\n"), 0o755)
		},
	} {
		dir := t.TempDir()
		at := filepath.Join(dir, "frontseat-action-"+Form)
		if err := plant(at); err != nil {
			t.Fatal(err)
		}
		if _, err := Install(dir); err != nil {
			t.Errorf("%s: %v", way, err)
			continue
		}
		info, err := os.Lstat(at)
		got, readErr := os.ReadFile(at)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 || readErr != nil || !bytes.Equal(got, want) {
			t.Errorf("%s was left in the confiner's place", way)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 1 {
			t.Errorf("%s: installing left %d files", way, len(entries))
		}
	}
}

// A confiner of this form that another build left is not replaced: a
// running action may be starting with it, and it is what this one is.
func TestAConfinerOfThisFormOfAnotherBuildIsLeftAsItIs(t *testing.T) {
	dir := t.TempDir()
	at := filepath.Join(dir, "frontseat-action-"+Form)
	if err := os.WriteFile(at, []byte("#!/bin/sh\necho "+Form+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(at)
	if err != nil || !os.SameFile(before, after) {
		t.Errorf("a confiner of this form of another build was replaced: %v", err)
	}
}

// What is under the confiner's name and does not answer, or answers and
// leaves a process behind holding its output, keeps an install waiting
// no longer than it is given to say its form, and is replaced, and what
// it started is ended with it. One that only its owner may run is no
// confiner an action, which runs as a user of no account, can start, and
// is replaced too.
func TestWhatDoesNotAnswerInTimeOrCannotBeRunByAnyoneIsReplaced(t *testing.T) {
	was := asking
	asking = 500 * time.Millisecond
	defer func() { asking = was }()
	// Each process a planted file starts writes its number down, so that
	// what is still running is asked of those processes and no other; and
	// the file itself writes its own first, which is the number of the
	// group they are all in, so that what is cleaned up is of that group.
	started := func(pids string) string {
		return "sh -c 'echo $$ >> " + pids + "; exec sleep 61' &\n"
	}
	leads := func(pids string) string {
		return "#!/bin/sh\necho $$ > " + pids + ".group\n"
	}
	for way, plant := range map[string]struct {
		text func(pids string) string
		mode os.FileMode
	}{
		"one that never answers": {func(pids string) string {
			return leads(pids) + "echo $$ >> " + pids + "\nexec sleep 61\n"
		}, 0o755},
		"one that leaves a process holding its output": {func(pids string) string {
			return leads(pids) + started(pids) + "echo $$ >> " + pids + "\nexec sleep 61\n"
		}, 0o755},
		"one that answers and leaves a process behind": {func(pids string) string {
			return leads(pids) + started(pids) + "echo " + Form + "\n"
		}, 0o755},
		"one only its owner may run": {func(string) string {
			return "#!/bin/sh\necho " + Form + "\n"
		}, 0o700},
	} {
		dir, other := t.TempDir(), t.TempDir()
		pids := filepath.Join(other, "pids")
		at := filepath.Join(dir, "frontseat-action-"+Form)
		text := plant.text(pids)
		if err := os.WriteFile(at, []byte(text), plant.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(at, plant.mode); err != nil {
			t.Fatal(err)
		}
		began := time.Now()
		if _, err := Install(dir); err != nil {
			t.Errorf("%s: %v", way, err)
			continue
		}
		if took := time.Since(began); took > 5*time.Second {
			t.Errorf("%s kept the install waiting %s", way, took)
		}
		// None answers in time and leaves nothing behind, so each is
		// replaced.
		if got, _ := os.ReadFile(at); string(got) == text {
			t.Errorf("%s was left in the confiner's place", way)
		}
		written, _ := os.ReadFile(pids)
		leader, _ := os.ReadFile(pids + ".group")
		group, _ := strconv.Atoi(strings.TrimSpace(string(leader)))
		for _, pid := range strings.Fields(string(written)) {
			number, err := strconv.Atoi(pid)
			if err != nil {
				t.Fatal(err)
			}
			if !gone(number) {
				t.Errorf("%s left process %d running", way, number)
				// Ended only where it is still of the planted file's group:
				// a number given to another process since is not this one.
				if in, err := syscall.Getpgid(number); err == nil && group > 0 && in == group {
					_ = syscall.Kill(number, syscall.SIGKILL)
				}
			}
		}
	}
}

// gone says whether a process has ended, waiting a moment for what
// reaps it: one that is no more, or one that has ended and is only not
// reaped yet (a zombie), as it stays where nothing reaps orphans, as
// under a runner whose first process reaps none. Where the system says
// a process's state (/proc), that is asked; elsewhere, whether it is.
func gone(pid int) bool {
	for range 50 {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		if stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat")); err == nil {
			if state, _, _, _, ok := statOf(stat); ok && state == 'Z' {
				return true
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// A temporary directory no running action holds is removed when the next
// action starts; one a running action holds stays however old it is, as
// does one just made, which its confiner may not hold yet, and anything
// not named as the confiner names its own.
func TestATemporaryDirectoryAKilledActionLeftIsSweptAndARunningOnesIsNot(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{TemporaryPrefix + "killed", TemporaryPrefix + "running", TemporaryPrefix + "just-made", "somebody-else"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	long := time.Now().Add(-48 * time.Hour)
	for _, name := range []string{TemporaryPrefix + "killed", TemporaryPrefix + "running", "somebody-else"} {
		if err := os.Chtimes(filepath.Join(dir, name), long, long); err != nil {
			t.Fatal(err)
		}
	}
	running, err := hold(filepath.Join(dir, TemporaryPrefix+"running"))
	if err != nil {
		t.Fatal(err)
	}
	defer running.Close()
	sweep(dir, time.Now())
	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range left {
		names = append(names, entry.Name())
	}
	if want := []string{TemporaryPrefix + "just-made", TemporaryPrefix + "running", "somebody-else"}; !slices.Equal(names, want) {
		t.Fatalf("left %v, want %v", names, want)
	}
}

// A socket is connected to where it is one itself; a link to one, as a
// sandbox could leave where its socket belongs, is refused, and so is a
// file that is no socket. The control is the socket, connected to.
func TestOnlyASocketItselfIsConnectedToAndNoLinkToOne(t *testing.T) {
	dir, err := os.MkdirTemp("", "sock")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	// A connection is taken into the listener's backlog: nothing need
	// accept it.
	defer listener.Close()
	link := filepath.Join(dir, "link")
	if err := os.Symlink(socket, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, refused := range []string{link, file} {
		if conn, err := DialSocket(t.Context(), refused); err == nil {
			conn.Close()
			t.Errorf("%s was connected to", filepath.Base(refused))
		}
	}
	conn, err := DialSocket(t.Context(), socket)
	if err != nil {
		t.Fatalf("the control, the socket itself: %v", err)
	}
	conn.Close()
}

// A process's state, parent, group and start are read past its name,
// whatever the name holds; what cannot be read so is not taken for them.
func TestAProcesssGroupIsReadPastItsName(t *testing.T) {
	rest := " 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 4242 0"
	for stat, want := range map[string][4]int{
		"166 (hello) S 67 166 1" + rest:    {'S', 67, 166, 4242},
		"9 (a b) c) 1 2) R 1 77 77" + rest: {'R', 1, 77, 4242},
		"12 (sleep) Z 166 166 1" + rest:    {'Z', 166, 166, 4242},
	} {
		state, parent, group, started, ok := statOf([]byte(stat))
		if !ok || [4]int{int(state), parent, group, started} != want {
			t.Errorf("%q: %c %d %d %d %v, want %v", stat, state, parent, group, started, ok, want)
		}
	}
	for _, stat := range []string{"", "12 sleep S 1 2" + rest, "12 (sleep) S 1", "12 (sleep) S 1 x" + rest, "12 (sleep) SS 1 2" + rest, "12 (sleep) S 1 2 0 0"} {
		if _, _, _, _, ok := statOf([]byte(stat)); ok {
			t.Errorf("%q was read as a process's stat", stat)
		}
	}
}

// A process group is killed whole where the system refuses a signal to a
// group, as OpenShell's sandboxes do: each of its processes is killed by
// itself, one started in the background as well as its leader. The
// control is the same group where the signal to it is taken; inside an
// OpenShell sandbox, which refuses it too, both take the same way.
func TestAGroupIsKilledWholeWhereASignalToAGroupIsRefused(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("a group's processes are listed in /proc")
	}
	for _, refused := range []bool{false, true} {
		if refused {
			was := killGroup
			killGroup = func(int, syscall.Signal) error { return syscall.EPERM }
			t.Cleanup(func() { killGroup = was })
		}
		run := exec.Command("/bin/sh", "-c", "sleep 61 & echo $!; exec sleep 61")
		run.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		out, err := run.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := run.Start(); err != nil {
			t.Fatal(err)
		}
		line, err := bufio.NewReader(out).ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		background, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			t.Fatal(err)
		}
		if err := SignalGroup(run.Process.Pid, syscall.SIGKILL); err != nil {
			t.Errorf("refused %v: %v", refused, err)
		}
		_ = run.Wait()
		if !gone(background) {
			_ = syscall.Kill(background, syscall.SIGKILL)
			t.Errorf("refused %v: the process the group's leader started in the background was left", refused)
		}
	}
}

// A refusal says what was refused and why, never the value: the spec
// and the way up are written by any client of the grid, and a value may
// hold a token. The controls are the daemon's spec and a way up, which
// TestASpecThatIsNotWhatTheDaemonWritesIsRefused and
// TestTheWayUpToTheRootGoesUpAlone see taken.
func TestARefusalRepeatsNothingTheConfinerWasTold(t *testing.T) {
	const marked = "tok3n"
	if _, err := read(encoded(t, valid())); err != nil {
		t.Fatalf("the daemon's spec is refused: %v", err)
	}
	spec := valid()
	spec.Home = "../" + marked
	if _, err := read(encoded(t, spec)); err == nil || strings.Contains(err.Error(), marked) {
		t.Errorf("a spec that names a directory out of the root: %v", err)
	}
	t.Chdir(t.TempDir())
	if err := Main([]string{encoded(t, valid()), "../" + marked, "/bin/true"}); err == nil || strings.Contains(err.Error(), marked) {
		t.Errorf("a way up that is none: %v", err)
	}
}

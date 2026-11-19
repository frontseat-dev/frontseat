package program_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	execport "github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/program"
	"github.com/frontseat-dev/frontseat/server/internal/exec/tools"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// grid is a grid that holds directories by name: each is written as the
// files it was told, a file whose content begins "->" being a link.
type grid map[string]map[string]string

func (g grid) Materialize(_ context.Context, directory execport.Held, into string) error {
	for name, content := range g[directory.Digest.Hash] {
		at := filepath.Join(into, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
			return err
		}
		if target, link := strings.CutPrefix(content, "->"); link {
			if err := os.Symlink(target, at); err != nil {
				return err
			}
			continue
		}
		mode := os.FileMode(0o755)
		if strings.HasPrefix(content, "data:") {
			mode = 0o644
		}
		if err := os.WriteFile(at, []byte(content), mode); err != nil {
			return err
		}
	}
	return os.MkdirAll(into, 0o755)
}

// here is this machine, said as a grid says its workers' platform.
func here() map[string]string {
	return map[string]string{"OSFamily": runtime.GOOS, "ISA": map[string]string{"arm64": "aarch64", "amd64": "x86-64"}[runtime.GOARCH]}
}

func held(name string) execport.Held {
	return execport.Held{Digest: execport.Digest{Hash: name}, Directory: true}
}

// greeter is a program in its action's home: a script that says what it
// was given, where it runs and what it was told, and ends as asked.
const greeter = `#!/bin/sh
echo "given $*"
echo "in $(pwd)"
echo "home $HOME told $GREETING daemon ${DAEMON_SECRET:-nothing}"
echo "to standard error" >&2
printf 'no newline'
[ "$2" = fail ] && exit 3
exit 0
`

func action(arguments ...string) plan.Action {
	return plan.Action{
		Label:       model.Label{Path: "code/hello", Plugin: "go", Name: "run/hello"},
		Repository:  "code",
		ProjectRoot: "hello",
		Declared: &pluginv1.Action{
			Name:    "run/hello",
			Command: &pluginv1.Command{Arguments: arguments, Environment: map[string]string{"GREETING": "hello"}},
			Inputs:  []*pluginv1.Input{{Kind: &pluginv1.Input_Output{Output: "compile"}}},
			Program: true,
		},
	}
}

// printed gathers what a program prints, by stream.
type printed struct {
	mu          sync.Mutex
	out, errors []string
}

func (p *printed) say(stderr bool, line []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if stderr {
		p.errors = append(p.errors, string(line))
		return
	}
	p.out = append(p.out, string(line))
}

func bringer(t *testing.T, g grid) program.Bringer {
	return program.Bringer{Grid: g, Kept: filepath.Join(t.TempDir(), "work"), Platform: here()}
}

// hello brings a program, hello, whose script is script, from where its
// action left it on the grid.
func hello(t *testing.T, script string) (program.Bringer, *program.Brought) {
	t.Helper()
	b := bringer(t, grid{"bin": {"hello": script}})
	brought, err := b.Bring(context.Background(), "code", action("~/go/bin/hello"), map[string]execport.Held{tools.Home + "/go/bin": held("bin")})
	if err != nil {
		t.Fatal(err)
	}
	return b, brought
}

// saying runs what was brought, given arguments, to its end, and returns
// what it said.
func saying(t *testing.T, brought *program.Brought, arguments ...string) *printed {
	t.Helper()
	said := &printed{}
	running, err := brought.Start(arguments, said.say)
	if err != nil {
		t.Fatal(err)
	}
	running.ExitCode()
	return said
}

// catting is what a program that prints the file it is given says, given
// one that holds content.
func catting(t *testing.T, content string) *printed {
	t.Helper()
	file := filepath.Join(t.TempDir(), "printed")
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, brought := hello(t, "#!/bin/sh\ncat \"$1\"\n")
	return saying(t, brought, file)
}

func kept(t *testing.T, b program.Bringer) []string {
	t.Helper()
	entries, err := os.ReadDir(b.Kept)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// A program is brought from the grid and started where the daemon is:
// with its plugin's arguments and then the caller's, in its project's
// directory, with its plugin's environment and nothing of the daemon's.
func TestAProgramRunsWithWhatItIsGivenAndNothingOfTheDaemons(t *testing.T) {
	t.Setenv("DAEMON_SECRET", "leaked")
	b := bringer(t, grid{"bin": {"hello": greeter}})
	left := map[string]execport.Held{tools.Home + "/go/bin": held("bin")}
	for _, c := range []struct {
		arguments []string
		code      int
	}{{[]string{"two words"}, 0}, {[]string{"fail"}, 3}} {
		brought, err := b.Bring(context.Background(), "code", action("~/go/bin/hello", "--first"), left)
		if err != nil {
			t.Fatal(err)
		}
		var said printed
		running, err := brought.Start(c.arguments, said.say)
		if err != nil {
			t.Fatal(err)
		}
		if code := running.ExitCode(); code != c.code {
			t.Errorf("given %v it exited %d, want %d", c.arguments, code, c.code)
		}
		if len(said.out) != 4 || said.out[0] != "given --first "+c.arguments[0] || !strings.HasSuffix(said.out[1], "/root/code/hello") || said.out[3] != "no newline" {
			t.Errorf("it printed %q", said.out)
		}
		if !strings.Contains(said.out[2], "told hello daemon nothing") || !strings.Contains(said.out[2], "/root/"+tools.Home+" ") {
			t.Errorf("its environment: %q", said.out[2])
		}
		if !slices.Equal(said.errors, []string{"to standard error"}) {
			t.Errorf("it printed to standard error %q", said.errors)
		}
		// Stopping what has ended does nothing, and nothing is left of it.
		if err := running.Stop(); err != nil {
			t.Error(err)
		}
		if left := kept(t, b); len(left) != 0 {
			t.Errorf("what was brought outlived the program: %v", left)
		}
	}
}

// A program that is stopped ends, with what it started, and is waited
// for; one that takes no telling is ended.
func TestAProgramThatIsStoppedEndsWithWhatItStarted(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child")
	b, brought := hello(t, "#!/bin/sh\n(sleep 300; echo late > "+marker+") &\necho up\nwait\n")
	up := make(chan struct{})
	var once sync.Once
	running, err := brought.Start(nil, func(_ bool, line []byte) {
		if string(line) == "up" {
			once.Do(func() { close(up) })
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-up:
	case <-time.After(30 * time.Second):
		t.Fatal("it never said it was up")
	}
	select {
	case <-running.Ended():
		t.Fatal("it ended by itself, so this proves nothing")
	default:
	}
	began := time.Now()
	if err := running.Stop(); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(began); took > 5*time.Second {
		t.Errorf("stopping took %s: what it started held it open", took)
	}
	if code := running.ExitCode(); code != 128+15 {
		t.Errorf("it exited %d, want 143, ended by the signal", code)
	}
	if left := kept(t, b); len(left) != 0 {
		t.Errorf("what was brought outlived the program: %v", left)
	}
}

// What is brought is named by the grid and by a plugin: nothing is
// written outside the program's own directory, and nothing is run that
// was not brought.
func TestWhatIsNotAProgramThatWasBroughtIsNotRun(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("#!/bin/sh\necho escaped\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	g := grid{"bin": {"hello": greeter, "data": "data: not to be run", "out": "->" + outside, "sub/in": "->../hello"}}
	home := tools.Home + "/go/bin"
	// The control: the program itself, and a link that stays within.
	for _, named := range []string{"~/go/bin/hello", "~/go/bin/sub/in"} {
		b := bringer(t, g)
		brought, err := b.Bring(context.Background(), "code", action(named), map[string]execport.Held{home: held("bin")})
		if err != nil {
			t.Fatalf("%s: %v", named, err)
		}
		brought.Remove()
		if left := kept(t, b); len(left) != 0 {
			t.Errorf("removing left %v", left)
		}
	}
	for says, c := range map[string]struct {
		action plan.Action
		left   map[string]execport.Held
		want   string
	}{
		"a program nothing left":         {action("~/go/bin/other"), map[string]execport.Held{home: held("bin")}, "which nothing it reads left: there is no such file"},
		"a link out of what was brought": {action("~/go/bin/out"), map[string]execport.Held{home: held("bin")}, "it leads outside what was brought"},
		"a file that may not be run":     {action("~/go/bin/data"), map[string]execport.Held{home: held("bin")}, "it may not be run"},
		"a directory":                    {action("~/go/bin/sub"), map[string]execport.Held{home: held("bin")}, "it is not a file"},
		"a place outside the root":       {action("~/go/bin/hello"), map[string]execport.Held{"../../escaped": held("bin")}, "not beneath its root"},
		"a place by a full path":         {action("~/go/bin/hello"), map[string]execport.Held{"/tmp/escaped": held("bin")}, "not beneath its root"},
		"one file alone":                 {action("~/go/bin/hello"), map[string]execport.Held{home: {Digest: execport.Digest{Hash: "bin"}}}, "one file alone is not served yet"},
		"an action that is no program": {func() plan.Action {
			a := action("~/go/bin/hello")
			a.Declared.Program = false
			return a
		}(), map[string]execport.Held{home: held("bin")}, "is no program"},
	} {
		b := bringer(t, g)
		_, err := b.Bring(context.Background(), "code", c.action, c.left)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want it refused saying %q", says, err, c.want)
		}
		if left := kept(t, b); len(left) != 0 {
			t.Errorf("%s: a refusal left %v", says, left)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(outside), "escaped")); err == nil {
		t.Error("something was written outside the program's directory")
	}
}

// What the workers built is run only where the daemon is on their
// platform.
func TestAProgramBuiltForAnotherPlatformIsNotRun(t *testing.T) {
	b := bringer(t, grid{"bin": {"hello": greeter}})
	b.Platform = map[string]string{"OSFamily": "plan9", "ISA": "aarch64"}
	_, err := b.Bring(context.Background(), "code", action("~/go/bin/hello"), map[string]execport.Held{tools.Home + "/go/bin": held("bin")})
	var other *program.OtherPlatform
	if !errors.As(err, &other) || other.Workers != "plan9-arm64" {
		t.Fatalf("a program of another platform: %v", err)
	}
	if left := kept(t, b); len(left) != 0 {
		t.Errorf("a refusal left %v", left)
	}
}

// Something a program started that left its group, as a daemon of its
// own does, holds the program's output open and is not ended with it:
// the run ends a moment after the program does, all the same.
func TestWhatLeftTheProgramsGroupDoesNotHoldTheRunOpen(t *testing.T) {
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("no perl to leave a group with")
	}
	defer program.Hurried(time.Second, 300*time.Millisecond)()
	script := "#!/usr/bin/env perl\nuse POSIX;\n$| = 1;\nif (fork() == 0) { POSIX::setsid(); sleep 25; exit 0 }\nprint \"up\\n\";\nexit 0;\n"
	b, brought := hello(t, script)
	// The run is timed from when the program has said it is up: a
	// busy machine may take its time to start a program it has not run
	// before, and that is not the run being held open.
	var said printed
	up := make(chan struct{})
	var once sync.Once
	running, err := brought.Start(nil, func(stderr bool, line []byte) {
		said.say(stderr, line)
		once.Do(func() { close(up) })
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-up:
	case <-running.Ended():
	case <-time.After(2 * time.Minute):
		t.Fatal("the program said nothing")
	}
	select {
	case <-running.Ended():
	case <-time.After(10 * time.Second):
		t.Fatal("the program exited, and what it started held the run open")
	}
	said.mu.Lock()
	defer said.mu.Unlock()
	if running.ExitCode() != 0 || !slices.Equal(said.out, []string{"up"}) {
		t.Errorf("it exited %d having said %q", running.ExitCode(), said.out)
	}
	if left := kept(t, b); len(left) != 0 {
		t.Errorf("what was brought outlived the program: %v", left)
	}
}

// A program that takes no telling is ended once it has been given its
// time.
func TestAProgramThatTakesNoTellingIsEnded(t *testing.T) {
	defer program.Hurried(500*time.Millisecond, time.Second)()
	_, brought := hello(t, "#!/bin/sh\ntrap '' TERM\necho up\nwhile :; do sleep 1; done\n")
	up := make(chan struct{})
	var once sync.Once
	running, err := brought.Start(nil, func(_ bool, line []byte) { once.Do(func() { close(up) }) })
	if err != nil {
		t.Fatal(err)
	}
	<-up
	began := time.Now()
	if err := running.Stop(); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(began); took < 400*time.Millisecond || took > 8*time.Second {
		t.Errorf("stopping took %s: it was not given its time and then ended", took)
	}
	if code := running.ExitCode(); code != 128+9 {
		t.Errorf("it exited %d, want 137, ended and not told", code)
	}
}

// A line is said without what ends it and nothing else of it is lost,
// however long it is: one cut for its length is said in parts.
func TestEveryByteOfALongLineIsSaid(t *testing.T) {
	long := strings.Repeat("a", 64<<10-1) + "\r" + strings.Repeat("b", 10)
	said := catting(t, long+"\nshort\r\nlast")
	if n := len(said.out); n < 4 || said.out[n-2] != "short" || said.out[n-1] != "last" {
		t.Fatalf("it said %d lines, ending %q", len(said.out), said.out[max(0, len(said.out)-2):])
	}
	if got := strings.Join(said.out[:len(said.out)-2], ""); got != long {
		t.Errorf("of a line of %d bytes, %d were said", len(long), len(got))
	}
}

// Two things handed where one is inside the other are refused: the
// second would be written over, or lost.
func TestAPlaceInsideAnotherIsRefused(t *testing.T) {
	b := bringer(t, grid{"bin": {"hello": greeter}})
	_, err := b.Bring(context.Background(), "code", action("~/go/bin/hello"), map[string]execport.Held{
		tools.Home + "/go": held("bin"), tools.Home + "/go/bin": held("bin"), tools.Home + "/go-other": held("bin"),
	})
	if err == nil || !strings.Contains(err.Error(), "inside it") {
		t.Errorf("a place inside another: %v", err)
	}
	if left := kept(t, b); len(left) != 0 {
		t.Errorf("a refusal left %v", left)
	}
}

// What a program left that may not be written is removed with it.
func TestWhatAProgramLeftThatMayNotBeWrittenIsRemoved(t *testing.T) {
	b, brought := hello(t, "#!/bin/sh\nmkdir -p \"$HOME/kept/deep\" && echo x > \"$HOME/kept/deep/file\" && chmod -R a-w \"$HOME/kept\"\n")
	running, err := brought.Start(nil, func(bool, []byte) {})
	if err != nil {
		t.Fatal(err)
	}
	if code := running.ExitCode(); code != 0 {
		t.Fatalf("it exited %d", code)
	}
	if left := kept(t, b); len(left) != 0 {
		t.Errorf("what it left that may not be written outlived it: %v", left)
	}
}

// A line that ends just where one is cut is one line, and what ends it
// is no line of its own, nor any part of it.
func TestALineCutAtItsEndIsOneLine(t *testing.T) {
	for says, printed_ := range map[string]string{
		"a newline at the cut":         strings.Repeat("a", 64<<10) + "\nnext\n",
		"a carriage return at the cut": strings.Repeat("a", 64<<10-1) + "\r\nnext\n",
		"both after the cut":           strings.Repeat("a", 64<<10) + "\r\nnext\n",
	} {
		said := catting(t, printed_)
		long := strings.Join(said.out[:max(0, len(said.out)-1)], "")
		if n := len(said.out); n < 2 || said.out[n-1] != "next" || strings.Trim(long, "a") != "" || len(long) < 64<<10-1 || slices.Contains(said.out, "") {
			t.Errorf("%s: %d lines, the last %q, of %d bytes before it", says, len(said.out), said.out[len(said.out)-1], len(long))
		}
	}
	// A line that is empty is still a line.
	_, brought := hello(t, "#!/bin/sh\nprintf 'a\\n\\nb\\n'\n")
	said := saying(t, brought)
	if !slices.Equal(said.out, []string{"a", "", "b"}) {
		t.Errorf("an empty line: %q", said.out)
	}
}

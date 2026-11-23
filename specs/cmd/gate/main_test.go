package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/local/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/local/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
	"github.com/frontseat-dev/frontseat/testsupport/testmodule"
	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
)

// The gates run every module go.work uses, each whole, and nothing is
// written out but go.work: a module added there is in every gate.
func TestEveryModuleTheWorkspaceUsesIsGated(t *testing.T) {
	root, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := testmodule.Workspace(root)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := selection(root, dirs, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := patterns(dirs); !slices.Equal(selected, want) {
		t.Errorf("selected %q, want %q", selected, want)
	}
	for _, dir := range dirs {
		if !slices.Contains(selected, "./"+dir+"/...") {
			t.Errorf("the module %s is not gated", dir)
		}
	}
}

// A directory left to another command takes its packages out and no
// other: the rest of its module is named package by package, and every
// other module whole. One that holds no package of a module is refused,
// since what it would leave to another command is nothing.
func TestADirectoryLeftToAnotherCommandTakesOutItsPackagesAlone(t *testing.T) {
	root, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := selection(root, []string{"specs", "local"}, []string{"specs/build"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(selected, "./specs/build") || slices.Contains(selected, "./specs/...") {
		t.Errorf("specs/build is still run: %q", selected)
	}
	if !slices.Contains(selected, "./specs/sandbox") || !slices.Contains(selected, "./specs/cli") || !slices.Contains(selected, "./local/...") {
		t.Errorf("more than specs/build was left out: %q", selected)
	}
	for _, nothing := range []string{"specs/absent", "nowhere", "spec"} {
		if got, err := selection(root, []string{"specs", "local"}, []string{nothing}, nil); err == nil {
			t.Errorf("-except %s was taken, selecting %q", nothing, got)
		}
	}
}

// What one command leaves out another runs whole, by the same reading:
// every package beneath the directory, one added there included, and no
// other. One that holds no package is refused, as for -except.
func TestADirectoryRunAloneIsEveryPackageBeneathItAndNoOther(t *testing.T) {
	root := t.TempDir()
	testfiles.Tree(t, root, map[string]string{
		"go.work":            "go 1.27\n\nuse ./m\n",
		"m/go.mod":           "module example.com/m\n\ngo 1.27\n",
		"m/a/a.go":           "package a\n",
		"m/heavy/h.go":       "package heavy\n",
		"m/heavy/new/n.go":   "package n\n",
		"m/heavy2/h2.go":     "package heavy2\n",
		"m/heavy/testdata/x": "",
	})
	t.Setenv("GOWORK", root+"/go.work")
	only, err := selection(root, []string{"m"}, nil, []string{"m/heavy"})
	if err != nil {
		t.Fatal(err)
	}
	except, err := selection(root, []string{"m"}, []string{"m/heavy"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(only, " "), "./m/heavy ./m/heavy/new"; got != want {
		t.Errorf("-only m/heavy runs %q, want %q", got, want)
	}
	if got, want := strings.Join(except, " "), "./m/a ./m/heavy2"; got != want {
		t.Errorf("-except m/heavy runs %q, want %q", got, want)
	}
	if got, err := selection(root, []string{"m"}, nil, []string{"m/absent"}); err == nil {
		t.Errorf("-only m/absent was taken, selecting %q", got)
	}
}

// The gate says the stack word itself: none where it runs with no stack,
// required where it runs the tests against it, and nothing otherwise,
// whatever it was itself given; it names no grid in the stack's place,
// whoever exported one; and it passes go no GOFLAGS, which would add to
// what its task says. A variable that only begins alike is passed, as the
// control.
func TestTheGateSaysItsOwnWordOnTheStack(t *testing.T) {
	given := []string{"HOME=/h", testrequired.Variable + "=" + testrequired.NoStack, "X=1", "GOFLAGS=-run=TestA -short", "GOFLAGSX=1", testbox.Variable + "=frontseat-box:another", testgrid.ConfinerVariable + "=/bb/another", testgrid.Variable + "=elsewhere:1", testgrid.Variable + "X=1"}
	given = append(given, "DOCKER_HOST=unix:///engine.sock", "DOCKER_CONTEXT=orbstack")
	const kept = "HOME=/h X=1 GOFLAGSX=1 " + testgrid.Variable + "X=1"
	if got := strings.Join(environment(given, unsaid, "A=b"), " "); got != kept+" DOCKER_HOST=unix:///engine.sock DOCKER_CONTEXT=orbstack "+strings.Join(isolated, " ")+" A=b" {
		t.Errorf("a gate that says nothing of the stack runs with %q", got)
	}
	if got := strings.Join(environment(given, required), " "); got != kept+" DOCKER_HOST=unix:///engine.sock DOCKER_CONTEXT=orbstack "+testrequired.Variable+"="+testrequired.Required+" "+strings.Join(isolated, " ") {
		t.Errorf("a gate against the stack runs with %q", got)
	}
	if got := strings.Join(environment(given, none), " "); got != kept+" "+testrequired.Variable+"="+testrequired.NoStack+" DOCKER_HOST="+NoEngine+" "+strings.Join(isolated, " ") {
		t.Errorf("a gate with no stack runs with %q", got)
	}
}

// What a gate runs reads none of this machine's git settings, whatever
// it was given: git run plainly by a test finds no credential helper of
// a person's or the system's. The control is git run with what the gate
// was given, a person's settings naming a helper, which finds it.
func TestWhatAGateRunsReadsNoneOfThisMachinesGitSettings(t *testing.T) {
	given := []string{"HOME=/h", "GIT_CONFIG_GLOBAL=/h/.gitconfig", "GIT_CONFIG_SYSTEM=/etc/gitconfig", "GIT_CONFIG_NOSYSTEM=0", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=given"}
	if got := strings.Join(environment(given, unsaid), " "); got != "HOME=/h "+strings.Join(isolated, " ") {
		t.Errorf("a gate runs with %q", got)
	}
	own := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(own, []byte("[credential]\n\thelper = planted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A person's environment: this one's but for what a gate told git,
	// since this test runs under one, and their own settings.
	person := []string{"GIT_CONFIG_GLOBAL=" + own}
	for _, each := range os.Environ() {
		if !strings.HasPrefix(each, "GIT_CONFIG") {
			person = append(person, each)
		}
	}
	helper := func(env []string) string {
		t.Helper()
		cmd := exec.Command("git", "config", "--get-all", "credential.helper")
		cmd.Dir, cmd.Env = t.TempDir(), env
		out, _ := cmd.Output()
		return strings.TrimSpace(string(out))
	}
	if got := helper(environment(person, unsaid)); strings.Contains(got, "planted") || got != "" {
		t.Errorf("git run as a gate runs it finds the credential helper %q", got)
	}
	if got := helper(person); !strings.Contains(got, "planted") {
		t.Errorf("the control: git run with a person's settings finds %q, not theirs", got)
	}
	// Nor the ignore file a person keeps where no setting names it.
	xdg := t.TempDir()
	if err := os.MkdirAll(filepath.Join(xdg, "git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "git", "ignore"), []byte("hidden\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ignored := func(env []string) bool {
		t.Helper()
		dir := t.TempDir()
		if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
		cmd := exec.Command("git", "check-ignore", "-q", "hidden")
		cmd.Dir, cmd.Env = dir, append(env, "XDG_CONFIG_HOME="+xdg)
		return cmd.Run() == nil
	}
	if ignored(environment(os.Environ(), unsaid)) {
		t.Error("git run as a gate runs it reads a person's own ignore file")
	}
	if !ignored(person) {
		t.Error("the control: git run with a person's settings does not read their ignore file")
	}
}

// A person who exported the word that there is no stack does not make a
// gate that needs one skip it: go-test, as every verb but test -no-stack,
// runs a test that needs the stack, which fails here if it is told there
// is none. The control is the same test told so by the gate's own
// environment for -no-stack, which fails.
func TestAnExportedWordOnTheStackIsNotTheGates(t *testing.T) {
	runsTheGate(t)
	t.Setenv(testrequired.Variable, testrequired.NoStack)
	t.Setenv(probe, "1")
	if code, printed := quietly(t, func() int {
		return gate([]string{"go-test", "-count=1", "-run", "^TestTheStackWordAsTheGateGaveIt$", "./specs/cmd/gate/"})
	}); code != 0 {
		t.Fatalf("with %s=%s exported, a gate that needs the stack told its test there was none (exit %d):\n%s", testrequired.Variable, testrequired.NoStack, code, printed)
	}
	if code, printed := ofThisPackage(t, none, "TestTheStackWordAsTheGateGaveIt"); code == 0 {
		t.Fatalf("the control, a test told there is no stack, passed:\n%s", printed)
	}
}

// ofThisPackage runs one test of this package as the gate runs a test
// program, with no stack or with it, quietly, outside any gate.
func ofThisPackage(t *testing.T, word stack, test string) (int, string) {
	t.Helper()
	root, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	return quietly(t, func() int {
		return run(root, environment(os.Environ(), word, source(root)), "go", "test", "-count=1", "-run", "^"+test+"$", "./specs/cmd/gate/")
	})
}

// quietly runs a gate a test runs inside itself, keeping what it prints
// for the test to say where the outcome was not the one wanted: a run
// expected to fail prints no "--- FAIL" into the log of the gate that runs
// this test, where it would read as a failure of its own.
func quietly(t *testing.T, do func() int) (int, string) {
	t.Helper()
	var printed bytes.Buffer
	wasSaid, wasComplained := said, complained
	said, complained = &printed, &printed
	t.Cleanup(func() { said, complained = wasSaid, wasComplained })
	code := do()
	said, complained = wasSaid, wasComplained
	return code, printed.String()
}

// reachTheRuntime marks the run of TestTheRuntimeAsATestMightReachIt
// that the test below makes.
const reachTheRuntime = "FRONTSEAT_GATE_REACH_THE_RUNTIME"

// A gate that runs with no stack leaves the container runtime out of
// every test's reach, whatever engine or context it was itself told of:
// here an engine this test serves, which a test of the gate tries to
// reach as a runtime's client does, by the address DOCKER_HOST names.
// The control is the same test run with the environment the gate gives
// where it runs with the stack, which reaches it.
func TestAGateWithNoStackReachesNoContainerRuntime(t *testing.T) {
	runsTheGate(t)
	// A short path: a socket's is held to about a hundred bytes. The
	// temporary directory, where it is short (a test action's own); /tmp
	// where it is not (macOS gives a long one).
	base := os.TempDir()
	if len(base) > 48 {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "gate-engine-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	engine, err := net.Listen("unix", filepath.Join(dir, "engine.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close() })
	go func() {
		for {
			conn, err := engine.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	t.Setenv("DOCKER_HOST", "unix://"+engine.Addr().String())
	t.Setenv("DOCKER_CONTEXT", "elsewhere")
	t.Setenv(reachTheRuntime, "1")
	only := []string{"test", "-no-stack", "-only", "specs/cmd/gate", "--", "-count=1", "-run", "^TestTheRuntimeAsATestMightReachIt$"}
	if code, printed := quietly(t, func() int { return gate(only) }); code != 0 {
		t.Fatalf("a test reached the container runtime in a gate with no stack (exit %d):\n%s", code, printed)
	}
	// The control is told of the engine alone, and of no context.
	t.Setenv("DOCKER_CONTEXT", "")
	if code, printed := ofThisPackage(t, unsaid, "TestTheRuntimeAsATestMightReachIt"); code == 0 || !strings.Contains(printed, "reached the engine") {
		t.Fatalf("the control, a test given the engine this test serves, did not reach it:\n%s", printed)
	}
}

// A gate that runs the tests with the stack fails, naming it, where no
// stack answers, before it runs a test: a test that needs the stack is
// never left to skip, and the gate to end green with no spec of the
// stack run. Here the container runtime is named as an engine nothing
// listens at, so no stack answers whatever this machine runs. The
// control is the same gate with -no-stack, which gets past the asking
// and runs go test over the package (the test it names does nothing
// unless told to).
func TestAGateThatNeedsTheStackFailsWhereNoneAnswers(t *testing.T) {
	runsTheGate(t)
	t.Setenv("DOCKER_HOST", NoEngine)
	t.Setenv("DOCKER_CONTEXT", "")
	only := []string{"-only", "specs/cmd/gate", "--", "-count=1", "-run", "^TestTheStackWordAsTheGateGaveIt$"}
	code, printed := quietly(t, func() int { return gate(append([]string{"test"}, only...)) })
	if code == 0 || !strings.Contains(printed, "no local stack answers") {
		t.Fatalf("a gate that needs the stack ran with none (exit %d):\n%s", code, printed)
	}
	if strings.Contains(printed, "ok ") || strings.Contains(printed, "PASS") {
		t.Errorf("a gate that needs the stack ran a test before it found none:\n%s", printed)
	}
	if code, printed := quietly(t, func() int { return gate(append([]string{"test", "-no-stack"}, only...)) }); code != 0 || !strings.Contains(printed, "ok ") {
		t.Fatalf("the control, a gate with no stack, failed (exit %d):\n%s", code, printed)
	}
}

// Where the container runtime answers and a service of the stack does
// not listen, no stack answers, naming the service; where each listens,
// as at a listener of this test's own, it does. A runtime that does not
// answer is said before any service is asked.
func TestAStackWhoseServiceDoesNotListenDoesNotAnswer(t *testing.T) {
	listening, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listening.Close()
	gone, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := gone.Addr().String()
	gone.Close()
	answers := func(context.Context) error { return nil }
	ctx := context.Background()
	err = stackAnswers(ctx, answers, []service{{"the control plane", listening.Addr().String()}, {"the grid", closed}})
	if err == nil || !strings.Contains(err.Error(), "the grid does not answer") {
		t.Fatalf("a stack whose grid does not listen: %v; want it said that the grid does not answer", err)
	}
	if err := stackAnswers(ctx, answers, []service{{"the control plane", listening.Addr().String()}, {"the grid", listening.Addr().String()}}); err != nil {
		t.Fatalf("the control, a stack whose every service listens: %v", err)
	}
	silent := func(context.Context) error { return errors.New("no engine") }
	if err := stackAnswers(ctx, silent, []service{{"the grid", closed}}); err == nil || !strings.Contains(err.Error(), "no engine") || strings.Contains(err.Error(), "the grid") {
		t.Fatalf("a runtime that does not answer: %v; want it said before any service", err)
	}
}

// A gate a person runs by hand over tests of their choosing (go-test,
// `mise run go:test`) does not end green with a test of the stack
// skipped: where the container runtime does not answer, here an engine
// nothing listens at, a test that needs it fails, naming it. The control
// is the same test run with nothing said of the stack, as go test alone
// runs it, which is skipped and passes.
func TestATestOfTheStackFailsInAGateRunByHandWhereNoStackAnswers(t *testing.T) {
	runsTheGate(t)
	t.Setenv("DOCKER_HOST", NoEngine)
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv(needTheRuntime, "1")
	code, printed := quietly(t, func() int {
		return gate([]string{"go-test", "-count=1", "-v", "-run", "^TestTheRuntimeAsATestNeedsIt$", "./specs/cmd/gate/"})
	})
	if code == 0 || !strings.Contains(printed, "no container runtime answers") || strings.Contains(printed, "--- SKIP") {
		t.Fatalf("a gate run by hand let a test of the stack skip where no stack answers (exit %d):\n%s", code, printed)
	}
	root, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	if code, printed := quietly(t, func() int {
		return run(root, environment(os.Environ(), unsaid, source(root)), "go", "test", "-count=1", "-v", "-run", "^TestTheRuntimeAsATestNeedsIt$", "./specs/cmd/gate/")
	}); code != 0 || !strings.Contains(printed, "--- SKIP") {
		t.Fatalf("the control, the same test with nothing said of the stack, was not skipped (exit %d):\n%s", code, printed)
	}
}

// needTheRuntime marks the run of TestTheRuntimeAsATestNeedsIt that the
// test above makes.
const needTheRuntime = "FRONTSEAT_GATE_NEED_THE_RUNTIME"

func TestTheRuntimeAsATestNeedsIt(t *testing.T) {
	if os.Getenv(needTheRuntime) == "" {
		return
	}
	testbox.Runtime(t)
}

func TestTheRuntimeAsATestMightReachIt(t *testing.T) {
	if os.Getenv(reachTheRuntime) == "" {
		return
	}
	if named := os.Getenv("DOCKER_CONTEXT"); named != "" {
		t.Errorf("told of the runtime's context %q", named)
	}
	at, isSocket := strings.CutPrefix(os.Getenv("DOCKER_HOST"), "unix://")
	if !isSocket {
		t.Fatalf("told of the engine at %q, where a runtime's client finds none here", os.Getenv("DOCKER_HOST"))
	}
	if conn, err := net.Dial("unix", at); err == nil {
		conn.Close()
		t.Fatalf("reached the engine at %s", at)
	}
}

// probe marks the run of TestTheStackWordAsTheGateGaveIt that the test
// above makes.
const probe = "FRONTSEAT_GATE_PROBE"

func TestTheStackWordAsTheGateGaveIt(t *testing.T) {
	if os.Getenv(probe) == "" {
		return
	}
	if testrequired.Stackless() {
		t.Fatal("told there is no stack")
	}
}

// A package holds a budget where a test file of it declares a function
// named TestBudget…, in any module and whatever constraint it builds
// under; a method of that name, a function in a file that is no test,
// testdata and a nested module are none.
func TestThePackagesThatHoldABudgetAreFoundByTheirTests(t *testing.T) {
	root := t.TempDir()
	testfiles.Tree(t, root, map[string]string{
		"m/a/a_test.go":             "package a\n\nimport \"testing\"\n\nfunc TestBudgetOfA(t *testing.T) {}\n",
		"m/linux/l_test.go":         "//go:build linux\n\npackage linux\n\nimport \"testing\"\n\nfunc TestBudget(t *testing.T) {}\n",
		"m/method/m_test.go":        "package method\n\ntype s struct{}\n\nfunc (s) TestBudgetOfAMethod() {}\n",
		"m/plain/p.go":              "package plain\n\nfunc TestBudgetInSource() {}\n",
		"m/other/o_test.go":         "package other\n\nimport \"testing\"\n\nfunc TestOther(t *testing.T) {}\n",
		"m/a/testdata/t_test.go":    "package t\n\nimport \"testing\"\n\nfunc TestBudget(t *testing.T) {}\n",
		"m/nested/go.mod":           "module nested\n",
		"m/nested/n_test.go":        "package nested\n\nimport \"testing\"\n\nfunc TestBudget(t *testing.T) {}\n",
		"n/deep/below/d_test.go":    "package below\n\nimport \"testing\"\n\nfunc TestBudgetDeep(t *testing.T) {}\n",
		"n/deep/below/doc.go":       "package below\n",
		"n/deep/below/more_test.go": "package below\n\nimport \"testing\"\n\nfunc TestBudgetAgain(t *testing.T) {}\n",
	})
	held, err := budgeted(root, []string{"m", "n"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(held, " "), "./m/a/ ./m/linux/ ./n/deep/below/"; got != want {
		t.Errorf("found %q, want %q", got, want)
	}
}

// runsTheGate skips a test that runs the gate where the gate cannot run:
// with -short, or where there is no checkout, as in a test action, which
// is given the repository's source and none of what a checkout's gate
// fetches and builds with (cargo's crates, the box). `mise run check`
// runs it. A test it lets run stands at the repository's root, where mise
// runs the gate.
func runsTheGate(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("runs the gate")
	}
	root, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		t.Skip("runs the gate, which runs from a checkout, and this is none")
	}
	// From the repository's root, as mise runs it.
	t.Chdir(root)
}

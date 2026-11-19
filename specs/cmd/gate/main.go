// Command gate runs the Go gates of this repository over every module
// its go.work uses, so that no task writes out the modules, their
// packages or which of them hold a budget: each is read here, from
// go.work and from the source, when the gate runs.
//
//	gate fmt                            gofmt every module, and fail naming what is not formatted
//	gate vet                            go vet every module's packages
//	gate generated                      fail naming what of the Go Buf generates (api/buf.gen.*.yaml) is not what it writes now
//	gate test [-no-stack] [-except DIR]... [-- go test flags]
//	                                    go test every module's packages, but those beneath each DIR
//	gate test [-no-stack] -only DIR... [-- go test flags]
//	                                    go test the packages beneath each DIR, and no other
//	gate budget                         the budgets (TestBudget…), without the race detector
//	gate bench                          the benchmarks of the packages that hold a budget
//	gate go-test [go test arguments]    go test as it is given
//
// What runs tests is told the wasm modules prepared for it from this
// checkout (FRONTSEAT_TEST_ARTIFACTS, which prepare-modules prints).
// test, with the stack, first asks that the stack answers (the container
// runtime, and the stack's control plane and grid where local/ports
// says), and fails naming it where none does, before anything is built
// or run. test with the stack, and go-test, tell the tests that the gate
// runs against the stack (FRONTSEAT_TEST_STACK=required, package
// testrequired): a test that finds what it needs of the stack not
// answering fails, so neither ends green with a test of the stack
// skipped. Where a gate runs with
// the stack and a container runtime answers, it first builds
// the box image of this checkout and installs its frontseat-action for
// the stack's workers (local/box, the one build of the box), and tells
// the tests both (FRONTSEAT_TEST_BOX, the box's tag of its content, and
// FRONTSEAT_TEST_CONFINER): they are built from the source of the server
// and the command line, which no test builds itself. With -no-stack the
// tests are told that the gate runs with no local stack
// (FRONTSEAT_TEST_STACK=none, package testrequired): every test that
// needs it says so first and is skipped, and the container runtime is
// out of every test's reach (DOCKER_HOST names an engine there is none
// at), so the gate starts no container whatever this machine runs.
// Without it the gate takes that word out of what it runs, whoever
// exported it: a gate that needs the stack is never told there is none.
// A test that reaches the runtime without saying so first is named by
// the architecture test
// (TestATestThatReachesTheContainerRuntimeSaysItNeedsTheStack). Its
// command line is read by package gatecommand, as the architecture test
// reads the tasks that run it.
//
// Each command it runs is a program and its arguments, never a shell
// line, and its exit status is the gate's.
package main

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/frontseat-dev/frontseat/local"
	"github.com/frontseat-dev/frontseat/local/box"
	"github.com/frontseat-dev/frontseat/local/ports"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbudget"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/server/testsupport/testmodule"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrequired"
	"github.com/frontseat-dev/frontseat/specs/internal/gatecommand"
)

func main() {
	os.Exit(gate(os.Args[1:]))
}

func gate(arguments []string) int {
	command, err := gatecommand.Parse(arguments)
	if err != nil {
		fmt.Fprintln(complained, "gate:", err)
		return 2
	}
	root, err := local.Checkout()
	if err != nil {
		return failed(err)
	}
	dirs, err := testmodule.Workspace(root)
	if err != nil {
		return failed(err)
	}
	switch command.Verb {
	case "fmt":
		return formatted(root, dirs)
	case "vet":
		return run(root, environment(os.Environ(), unsaid), "go", append([]string{"vet"}, patterns(dirs)...)...)
	case "generated":
		return compareGenerated(root)
	case "test":
		selected, err := selection(root, dirs, command.Except, command.Only)
		if err != nil {
			return failed(err)
		}
		if !command.NoStack {
			if err := stackAnswers(context.Background()); err != nil {
				return failed(err)
			}
		}
		artifacts, err := prepared(root)
		if err != nil {
			return failed(err)
		}
		arguments := append(append([]string{"test"}, command.Go...), selected...)
		if !command.NoStack {
			boxes, err := boxed(root, artifacts)
			if err != nil {
				return failed(err)
			}
			return run(root, environment(os.Environ(), required, append([]string{artifacts, source(root)}, boxes...)...), "go", arguments...)
		}
		return run(root, environment(os.Environ(), none, artifacts, source(root)), "go", arguments...)
	case "budget", "bench":
		held, err := budgeted(root, dirs)
		if err != nil {
			return failed(err)
		}
		if len(held) == 0 {
			return failed(errors.New("no package holds a budget (a test named TestBudget…)"))
		}
		artifacts, err := prepared(root)
		if err != nil {
			return failed(err)
		}
		// A budget is held without the race detector, which changes what
		// code costs; the benchmarks are measured ten times each, in the
		// form benchstat compares.
		arguments := []string{"test", "-count=1", "-run", "TestBudget"}
		if command.Verb == "bench" {
			arguments = []string{"test", "-run", "^$", "-bench", ".", "-benchmem", "-count", "10"}
		}
		// Here alone is a budget's time held: the gate runs nothing else
		// beside it.
		timed := testbudget.Variable + "=" + testbudget.Timed
		return run(root, environment(os.Environ(), unsaid, artifacts, timed, source(root)), "go", append(arguments, held...)...)
	case "go-test":
		artifacts, err := prepared(root)
		if err != nil {
			return failed(err)
		}
		boxes, err := boxed(root, artifacts)
		if err != nil {
			return failed(err)
		}
		return run(root, environment(os.Environ(), required, append([]string{artifacts, source(root)}, boxes...)...), "go", append([]string{"test"}, command.Go...)...)
	}
	return failed(fmt.Errorf("gate does not %s", command.Verb))
}

// stackAnswers returns where no local stack answers the tests a gate
// runs with it: the container runtime does not, or the stack's control
// plane or grid does not on this machine's loopback, where local/ports
// says. A gate that runs the tests with the stack asks it before
// anything is built or run, so that a test that needs the stack is
// never left to skip and the gate to end green with none.
func stackAnswers(ctx context.Context) error {
	refused := func(why error) error {
		return fmt.Errorf("no local stack answers (%w): mise run test runs against it, so start it first (mise start); mise run check runs what needs none", why)
	}
	if err := box.Answers(ctx); err != nil {
		return refused(err)
	}
	for _, each := range []struct {
		name string
		port int
	}{{"the control plane", ports.ControlPlane}, {"the grid", ports.Grid}} {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(each.port)), time.Second)
		if err != nil {
			return refused(fmt.Errorf("%s does not answer at port %d: %w", each.name, each.port, err))
		}
		conn.Close()
	}
	return nil
}

func failed(err error) int {
	fmt.Fprintln(complained, "gate:", err)
	return 1
}

// patterns are every package of every module, as go names them from the
// repository's root.
func patterns(dirs []string) []string {
	out := make([]string, len(dirs))
	for i, dir := range dirs {
		out[i] = "./" + dir + "/..."
	}
	return out
}

// selection is every package of every module but those beneath each
// directory excepted: a module that holds none of them is named whole,
// and one that does by each of its packages as go lists them, but those.
// Given directories to run alone, it is the packages beneath them and no
// other, listed the same way, so that what one command leaves out another
// runs whole, a package added beneath it included. A directory excepted
// or run alone that is beneath no module, or holds no package, is a
// mistake that names it.
func selection(root string, dirs, except, only []string) ([]string, error) {
	var out []string
	held := map[string]bool{}
	named := append(slices.Clone(except), only...)
	for _, dir := range dirs {
		var within []string
		for _, each := range named {
			if each == dir || strings.HasPrefix(each, dir+"/") {
				within = append(within, each)
			}
		}
		if len(within) == 0 {
			if len(only) == 0 {
				out = append(out, "./"+dir+"/...")
			}
			continue
		}
		listed, err := listed(root, dir)
		if err != nil {
			return nil, err
		}
		for _, pkg := range listed {
			beneath := false
			for _, each := range within {
				if pkg == each || strings.HasPrefix(pkg, each+"/") {
					beneath, held[each] = true, true
				}
			}
			if beneath == (len(only) > 0) {
				out = append(out, "./"+pkg)
			}
		}
	}
	for _, each := range named {
		if !held[each] {
			return nil, fmt.Errorf("%s holds no package of a module go.work uses", each)
		}
	}
	return out, nil
}

// listed returns the directories, from the root, of a module's packages
// as go lists them, nested modules left out as go leaves them.
func listed(root, dir string) ([]string, error) {
	list := exec.Command("go", "list", "-f", "{{.Dir}}", "./...")
	list.Dir = filepath.Join(root, dir)
	list.Stderr = complained
	printed, err := list.Output()
	if err != nil {
		return nil, fmt.Errorf("go list in %s: %w", dir, err)
	}
	var out []string
	for _, line := range strings.Fields(string(printed)) {
		rel, err := filepath.Rel(root, line)
		if err != nil {
			return nil, err
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out, nil
}

// budgeted returns each package, from the root, whose tests hold a
// budget: a test file of it declares a function named TestBudget….
// Every module is read, whatever this machine builds of it.
func budgeted(root string, dirs []string) ([]string, error) {
	held := map[string]bool{}
	for _, dir := range dirs {
		top := filepath.Join(root, dir)
		err := filepath.WalkDir(top, func(at string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := entry.Name()
			if entry.IsDir() {
				if at != top && (name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || exists(filepath.Join(at, "go.mod"))) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(name, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), at, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			for _, declared := range file.Decls {
				function, ok := declared.(*ast.FuncDecl)
				if !ok || function.Recv != nil || !strings.HasPrefix(function.Name.Name, "TestBudget") {
					continue
				}
				rel, err := filepath.Rel(root, filepath.Dir(at))
				if err != nil {
					return err
				}
				held["./"+filepath.ToSlash(rel)+"/"] = true
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return slices.Sorted(maps.Keys(held)), nil
}

func exists(at string) bool {
	_, err := os.Stat(at)
	return err == nil
}

// formatted runs gofmt over every module, and fails naming each file it
// would change.
func formatted(root string, dirs []string) int {
	list := exec.Command("gofmt", append([]string{"-l"}, dirs...)...)
	list.Dir = root
	list.Stderr = complained
	printed, err := list.Output()
	if err != nil {
		return failed(fmt.Errorf("gofmt: %w", err))
	}
	files := slices.Compact(slices.Sorted(slices.Values(strings.Fields(string(printed)))))
	if len(files) == 0 {
		return 0
	}
	fmt.Fprintln(complained, strings.Join(files, "\n"))
	fmt.Fprintln(complained, "these files are not formatted: gofmt -w them")
	return 1
}

// prepared builds the wasm modules the tests load, from this checkout,
// and returns the environment that tells a test where they are.
func prepared(root string) (string, error) {
	prepare := exec.Command("go", "run", "./specs/cmd/prepare-modules")
	prepare.Dir = root
	prepare.Stderr = complained
	printed, err := prepare.Output()
	if err != nil {
		return "", fmt.Errorf("preparing the wasm modules: %w", err)
	}
	return testmodule.Artifacts + "=" + strings.TrimSpace(string(printed)), nil
}

// boxed builds the box image of this checkout, with the plugins
// prepared for the tests, hands it to the local stack's Podman, which
// runs the sandboxes the tests make of it, and installs its confiner,
// frontseat-action, for the stack's workers, and returns the environment
// that tells a test both. Where no container runtime answers it does
// neither, and a test that needs either fails as one that needs the
// runtime does, told that the gate runs against the stack: only go-test,
// run by hand, gets so far, since test, with the stack, asks first that
// the stack answers (stackAnswers).
func boxed(root, artifacts string) ([]string, error) {
	ctx := context.Background()
	if err := box.Answers(ctx); err != nil {
		return nil, nil
	}
	_, dir, _ := strings.Cut(artifacts, "=")
	plugins, err := testmodule.PluginsIn(dir)
	if err != nil {
		return nil, err
	}
	tag, err := box.Build{Root: root, Plugins: plugins, Log: complained}.Run(ctx)
	if err != nil {
		return nil, fmt.Errorf("building the box image: %w", err)
	}
	installing, stop := context.WithTimeout(ctx, 5*time.Minute)
	defer stop()
	confiner, err := box.InstallConfiner(installing, root)
	if err != nil {
		return nil, fmt.Errorf("installing the confiner, frontseat-action: %w", err)
	}
	if err := box.Give(installing, tag); err != nil {
		return nil, fmt.Errorf("handing the box to the stack: %w", err)
	}
	told := []string{testbox.Variable + "=" + tag, testgrid.ConfinerVariable + "=" + confiner}
	// The stack as one container, on the box just built. Building it
	// reaches the network: where it fails, the gate says why and runs
	// on, and the tests that need it fail saying it was not built
	// (testbox.Stack), and no other.
	stack, err := box.Stack{Root: root, Box: tag, Log: complained}.Run(ctx)
	if err != nil {
		fmt.Fprintln(complained, "gate: the stack's image was not built, and tests of it will fail:", err)
		return told, nil
	}
	return append(told, testbox.StackVariable+"="+stack), nil
}

// stack is the gate's word on the local stack to what it runs: nothing
// (unsaid), that the tests run against it (required), or that there is
// none (none).
type stack int

const (
	unsaid stack = iota
	required
	none
)

// environment is the environment a gate's program runs with: this one's,
// but the gate's word on the stack, which the gate says itself (none,
// where it runs with no stack, required where it runs the tests against
// it, and nothing otherwise), but a grid a test's own process names in
// the stack's place, but the box and the confiner, which are the gate's
// to build and to name (boxed), and but GOFLAGS, through which what runs the gate could add to go what its
// task does not say (-run, -short, -count, -race=false): what go is told
// is the task's alone. Where it runs with no stack, the container
// runtime is named as an engine at an address nothing can listen at, and
// no context of the runtime's is named beside it, so that whatever a
// test runs reaches no runtime. Git reads none of this machine's
// settings, its system's or a person's own: a test runs git as anyone
// would, and a credential helper of this machine's (macOS's keychain)
// would answer it with what an earlier test, at another port of the same
// address, left there, and keep what this one presents. Then what is
// given.
func environment(base []string, word stack, given ...string) []string {
	var out []string
	for _, each := range base {
		name, _, _ := strings.Cut(each, "=")
		if strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_") {
			continue
		}
		switch name {
		case testrequired.Variable, "GOFLAGS", testbox.Variable, testbox.StackVariable, testgrid.Variable, testgrid.ConfinerVariable, testbudget.Variable, testmodule.SourceVariable,
			"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_NOSYSTEM", "GIT_CONFIG_COUNT":
			continue
		case "DOCKER_HOST", "DOCKER_CONTEXT":
			if word == none {
				continue
			}
		}
		out = append(out, each)
	}
	switch word {
	case none:
		out = append(out, testrequired.Variable+"="+testrequired.NoStack, "DOCKER_HOST="+NoEngine)
	case required:
		out = append(out, testrequired.Variable+"="+testrequired.Required)
	}
	out = append(out, isolated...)
	return append(out, given...)
}

// isolated is what git is told of settings: none of this machine's, nor
// the files of a person's own it reads where no setting names them (its
// ignore and attributes files beneath XDG_CONFIG_HOME).
var isolated = []string{
	"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull,
	"GIT_CONFIG_COUNT=2",
	"GIT_CONFIG_KEY_0=core.excludesFile", "GIT_CONFIG_VALUE_0=" + os.DevNull,
	"GIT_CONFIG_KEY_1=core.attributesFile", "GIT_CONFIG_VALUE_1=" + os.DevNull,
}

// source tells what the gate runs where the repository is: its root,
// whole, since it runs the tests of every package at once.
func source(root string) string { return testmodule.SourceVariable + "=" + root }

// NoEngine is the address of the container runtime's engine a gate with
// no stack names: a socket beneath a file, which nothing can listen at.
const NoEngine = "unix:///dev/null/no-engine.sock"

// said and complained are where the gate, and what it runs, print: the
// terminal, or the record a test keeps of a gate it runs inside itself.
var said, complained io.Writer = os.Stdout, os.Stderr

// run runs a program in the root with the environment given, and
// returns its exit status.
func run(root string, environment []string, program string, arguments ...string) int {
	command := exec.Command(program, arguments...)
	command.Dir = root
	command.Env = environment
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, said, complained
	err := command.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit) && exit.ExitCode() > 0:
		return exit.ExitCode()
	default:
		return failed(fmt.Errorf("%s %s: %w", program, strings.Join(arguments, " "), err))
	}
}

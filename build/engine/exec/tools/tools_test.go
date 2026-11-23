package tools_test

import (
	"bytes"
	"context"
	"fmt"
	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
	"os"
	osexec "os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/reapi/reapitest"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools"
	"github.com/frontseat-dev/frontseat/common/confine"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/testsupport/testpins"
)

// confiner is frontseat-confine as this checkout builds it, for the grid's
// workers' architecture, in a directory to give a container as its /bb,
// as a worker holds it beside the runner.
func confiner(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	build := osexec.Command("go", "build", "-o", filepath.Join(dir, path.Base(confine.Confiner)), "github.com/frontseat-dev/frontseat/cmd/frontseat-confine")
	goarch := map[string]string{"aarch64": "arm64", "x86-64": "amd64"}[testgrid.Platform()["ISA"]]
	if goarch == "" {
		t.Fatalf("the grid's workers are of an architecture this test builds nothing for: %v", testgrid.Platform())
	}
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+goarch, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building frontseat-confine: %v\n%s", err, out)
	}
	return dir
}

func provisioner(t *testing.T) *tools.Provisioner {
	t.Helper()
	return &tools.Provisioner{Executor: reapitest.Dial(t), Platform: testgrid.Platform()}
}

// used is the action as the provisioner gives it the tools named, of the
// repository named, and what it is given as written.
func used(t *testing.T, ctx context.Context, p *tools.Provisioner, action exec.Action, repository string, named []string, fetch bool, within map[string]string) exec.Action {
	t.Helper()
	given, err := p.Use(ctx, action, repository, named, fetch, within)
	if err != nil {
		t.Fatal(err)
	}
	return given
}

// ranWith is what the grid answers for the action, given the tools named
// of the repository named, and the lines it printed; it fails the test
// where the action did not exit 0.
func ranWith(t *testing.T, p *tools.Provisioner, action exec.Action, repository string, named []string) (exec.Result, []string) {
	t.Helper()
	start := time.Now()
	given := used(t, context.Background(), p, action, repository, named, false, nil)
	t.Logf("%v were on the grid after %s", named, time.Since(start).Round(time.Millisecond))
	result := executed(t, context.Background(), p, given)
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Stderr)
	}
	return result, strings.Split(strings.TrimSpace(string(result.Stdout)), "\n")
}

// executed is what the provisioner's grid answers for the action.
// refusedUnconfined requires the grid to refuse an action whose command
// frontseat-confine does not begin, and run nothing of it: the grid's
// runner runs no command but a confined one. What would have been the
// control of a test, the same probe run with nothing confining it, is
// this refusal.
func refusedUnconfined(t *testing.T, ctx context.Context, p *tools.Provisioner, action exec.Action) {
	t.Helper()
	if _, err := p.Executor.Execute(ctx, action); err == nil || !strings.Contains(err.Error(), "begins with Frontseat's confiner") {
		t.Fatalf("a command nothing confines was answered %v; want the grid's runner to refuse it", err)
	}
}

func executed(t *testing.T, ctx context.Context, p *tools.Provisioner, action exec.Action) exec.Result {
	t.Helper()
	result, err := p.Executor.Execute(ctx, action)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestAnActionRunsWithTheToolItsRepositoryPins(t *testing.T) {
	p := provisioner(t)
	root := t.TempDir()
	testpins.Go(t, filepath.Join(root, "repo"))
	if err := os.MkdirAll(filepath.Join(root, "repo", "module"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "repo", "module", "nonce"), []byte(time.Now().String()), 0o644); err != nil {
		t.Fatal(err)
	}

	result, lines := ranWith(t, p, exec.Action{
		Label:            "//repo/module:version",
		Root:             root,
		Inputs:           []string{"repo/module/nonce"},
		WorkingDirectory: "repo/module",
		Arguments:        []string{"go", "env", "GOVERSION", "GOROOT", "GOCACHE"},
		Platform:         p.Platform,
	}, "repo", []string{"go"})
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "go1.") {
		t.Fatalf("stdout = %q", result.Stdout)
	}
	if !strings.Contains(lines[1], "/.frontseat/mise/installs/go/") {
		t.Errorf("GOROOT = %s; want the tree in the action's root", lines[1])
	}
	if !strings.Contains(lines[2], "/.frontseat/home/") {
		t.Errorf("GOCACHE = %s; want it in the action's own home", lines[2])
	}
	t.Logf("ran in %s: %s", result.Duration.Round(time.Millisecond), strings.Join(lines, " "))
}

// Rust is a compiler in one tree and, beside it, the library it links a
// wasm module against. Both are tools an action names, and cargo is told
// where the second is by the repository's own mise.toml.
func TestAnActionRunsWithRustAndIsToldWhereTheWasmLibraryIs(t *testing.T) {
	p := provisioner(t)
	root := t.TempDir()
	testpins.Own(t, root)

	result, lines := ranWith(t, p, exec.Action{
		Label:     "//:rust",
		Root:      root,
		Arguments: []string{"/bin/sh", "-c", `rustc --version && cargo --version && echo "$CARGO_TARGET_WASM32_WASIP1_RUSTFLAGS"`},
		Platform:  p.Platform,
	}, "", []string{"rust", "rust-std-wasm32-wasip1"})
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "rustc 1.") || !strings.HasPrefix(lines[1], "cargo 1.") {
		t.Fatalf("stdout = %q", result.Stdout)
	}
	if !strings.Contains(lines[2], "/.frontseat/mise/installs/http-rust-std-wasm32-wasip1/") || !strings.Contains(lines[2], "wasm32-wasip1/lib/self-contained") {
		t.Errorf("cargo is told %q; want the library's two directories, in the action's root", lines[2])
	}
	t.Logf("ran in %s: %s, %s", result.Duration.Round(time.Millisecond), lines[0], lines[1])
}

// reads is a command that passes only if it can read one of the worker's
// own files, which is no part of any action's root.
var reads = []string{"/bin/sh", "-c", ": > /slot/planted"}

// rooted is an action over a root that holds one file no earlier run had,
// and the pins of go, for an action that names it.
func rooted(t *testing.T, p *tools.Provisioner, arguments ...string) exec.Action {
	t.Helper()
	root := t.TempDir()
	testpins.Go(t, root)
	if err := os.WriteFile(filepath.Join(root, "nonce"), []byte(time.Now().String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return exec.Action{Label: "//test:probe", Root: root, Inputs: []string{"nonce"}, Arguments: arguments, Platform: p.Platform}
}

// named are the tools of each way an action is given its command: with
// the repository's environment alone, where it names none, and with a
// tool's too, where it names one. What confines an action is proven of
// both.
var named = map[string][]string{"naming no tool": nil, "naming a tool": {"go"}}

// What confines an action is the worker's own frontseat-confine, and
// nothing the action is handed: run as a worker runs it, in the workers'
// image as the runner's user, the action's root mounted where it is, it
// writes its root and not the worker's disk. The control is the same
// command run as the action's own, with nothing confining it, which
// writes there.
func TestAnActionIsConfinedInTheWorkersImage(t *testing.T) {
	testrequired.Stack(t)
	testbox.Runtime(t)
	p := provisioner(t)
	const script = `echo ran > ran; if (: > /tmp/escaped) 2>/dev/null; then echo escaped; else echo held; fi`
	action := used(t, context.Background(), p, rooted(t, p, "/bin/sh", "-c", script), "", nil, false, nil)
	for at := range action.Held {
		t.Errorf("the action is handed %s: what confines it is the worker's", at)
	}
	beside := filepath.Dir(action.Root)
	if err := os.Chmod(beside, 0o777); err != nil {
		t.Fatal(err)
	}
	bb := confiner(t)
	workers := strings.TrimPrefix(testgrid.Platform()["container-image"], "docker://")
	run := func(command ...string) string {
		t.Helper()
		args := append([]string{"run", "--rm", "--user", "65534", "-v", beside + ":" + beside, "-v", bb + ":/bb:ro", "-w", action.Root, "--entrypoint", command[0], workers}, command[1:]...)
		out, err := osexec.Command("docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if got := run("/bin/sh", "-c", script); got != "escaped" {
		t.Fatalf("the command, with nothing confining it, said %q: it could not write the worker's disk, so this test proves nothing", got)
	}
	if got := run(action.Arguments...); got != "held" {
		t.Errorf("the action, as the worker runs it, said %q, want held", got)
	}
}

// What finds an action's root leaves nothing in what its command is
// given: no OLDPWD, which a cd sets to where the action's root is on the
// worker, a path no action's key holds. Of every way an action is given
// its command, with tools and without, and of a fetch.
func TestAnActionsCommandIsGivenNoTraceOfFindingItsRoot(t *testing.T) {
	p := provisioner(t)
	for way, tools := range named {
		for _, fetch := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, fetch %v", way, fetch), func(t *testing.T) {
				action := used(t, context.Background(), p, rooted(t, p, "/bin/sh", "-c", `env | sed -n 's/^\(OLDPWD\)=.*/\1/p'; echo given`), "", tools, fetch, nil)
				done := executed(t, context.Background(), p, action)
				if done.ExitCode != 0 || strings.TrimSpace(string(done.Stdout)) != "given" {
					t.Errorf("exit %d: the command was given %q%s", done.ExitCode, done.Stdout, done.Stderr)
				}
			})
		}
	}
}

// An action writes nothing of the runner sandbox it runs in but its own
// root, whatever tools it names: not the slot, where the next action's
// directory and the runner's socket are.
func TestAnActionIsConfinedWhateverToolsItNames(t *testing.T) {
	p := provisioner(t)
	ctx := context.Background()

	// Nothing runs the probe unconfined: the grid's runner refuses it.
	// That the confined probe could write at all is shown below, by the
	// same action in its own root.
	refusedUnconfined(t, ctx, p, rooted(t, p, reads...))

	for name, tools := range named {
		t.Run(name, func(t *testing.T) {
			confined := used(t, ctx, p, rooted(t, p, reads...), "", tools, false, nil)
			kept := executed(t, ctx, p, confined)
			if kept.ExitCode == 0 {
				t.Error("it wrote the slot: it was not confined")
			}

			// And it is the writing that was stopped, not the action.
			harmless := used(t, ctx, p, rooted(t, p, "/bin/sh", "-c", ": < nonce && echo ran"), "", tools, false, nil)
			ran := executed(t, ctx, p, harmless)
			if ran.ExitCode != 0 || !strings.Contains(string(ran.Stdout), "ran") {
				t.Errorf("a confined action that reads only its own root: exit %d, stdout %q, stderr %q", ran.ExitCode, ran.Stdout, ran.Stderr)
			}
		})
	}
}

// underMise is the variable mise gives every command it runs (mise's
// src/cli/exec.rs): where a command's environment holds it, mise ran
// between the action and its command.
const underMise = "__MISE_DIFF"

// environments runs an action that prints its own environment, then the
// one mise x gives a command in its place, and returns each.
func environments(t *testing.T, p *tools.Provisioner, repository string, tools []string) (own, mises map[string]string) {
	t.Helper()
	root := t.TempDir()
	testpins.Go(t, filepath.Join(root, repository))
	config := filepath.Join(root, repository, "mise.toml")
	pinned, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	// The repository says something of its own in the environment, and
	// nothing earlier ran with it: a value, a directory of its own on the
	// path and a path, both said from where its mise.toml is, and a value
	// made of what the action's own environment holds.
	pinned = append(pinned, []byte("\n[env]\nFROM_THE_REPOSITORY = \"said "+time.Now().Format(time.RFC3339Nano)+"\"\n"+
		"_.path = [\"./bin\"]\nBESIDE_THE_PINS = \"{{config_root}}/beside\"\nFROM_THE_ACTION = \"{{env.GIVEN_BY_THE_ACTION | default(value='unset')}} and more\"\nUNSET_BY_THE_REPOSITORY = false\n")...)
	if err := os.WriteFile(config, pinned, 0o644); err != nil {
		t.Fatal(err)
	}
	// The action reads the repository's pins, so mise x, asked inside
	// it, reads what the action was given its environment by.
	action := exec.Action{
		Label: "//" + repository + ":env", Root: root, WorkingDirectory: repository, Platform: p.Platform,
		Inputs:      []string{path.Join(repository, "mise.toml"), path.Join(repository, "mise.lock")},
		Arguments:   []string{"/bin/sh", "-c", "env; echo -----; mise x -- env"},
		Environment: map[string]string{"GIVEN_BY_THE_ACTION": "given", "UNSET_BY_THE_REPOSITORY": "given by the action"},
	}
	action, err = p.Use(context.Background(), action, repository, tools, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := executed(t, context.Background(), p, action)
	if done.ExitCode != 0 {
		t.Fatalf("exit %d: %s", done.ExitCode, done.Stderr)
	}
	before, after, found := strings.Cut(string(done.Stdout), "-----\n")
	if !found {
		t.Fatalf("stdout = %q", done.Stdout)
	}
	read := func(printed string) map[string]string {
		vars := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(printed), "\n") {
			if name, value, is := strings.Cut(line, "="); is {
				vars[name] = value
			}
		}
		return vars
	}
	return read(before), read(after)
}

// An action runs its command itself, not under mise, which would be a
// process of its own at every action; and it runs with what mise would
// have given it, its tools' and its repository's [env], whether or not
// it names a tool. What mise x gives a command in the action's place is
// the measure.
func TestAnActionRunsWithTheEnvironmentMiseGivesAndNotUnderMise(t *testing.T) {
	p := provisioner(t)
	for way, tools := range named {
		t.Run(way, func(t *testing.T) {
			own, mises := environments(t, p, "repo", tools)
			if _, under := own[underMise]; under {
				t.Errorf("the command ran under mise: %s is set", underMise)
			}
			// The control: mise gives something, so the comparison is of
			// something.
			if !strings.HasPrefix(mises["FROM_THE_REPOSITORY"], "said ") {
				t.Fatalf("mise x gave no [env] of the repository, so this test proves nothing: %v", mises)
			}
			if len(tools) > 0 && !strings.Contains(mises["GOROOT"], "/.frontseat/mise/installs/go/") {
				t.Fatalf("mise x gave no GOROOT of the tool, so this test proves nothing: %v", mises)
			}
			if !strings.HasSuffix(mises["BESIDE_THE_PINS"], "/repo/beside") || !strings.Contains(mises["PATH"], "/repo/bin") || mises["FROM_THE_ACTION"] != "given and more" {
				t.Fatalf("mise x said nothing of the repository's directory or of the action's environment, so this test proves nothing: %v", mises)
			}
			if _, kept := mises["UNSET_BY_THE_REPOSITORY"]; kept {
				t.Fatalf("mise x kept what the repository unsets, so this test proves nothing: %v", mises)
			}
			for name, value := range mises {
				if name == underMise {
					continue
				}
				if own[name] != value {
					t.Errorf("%s is %q; mise gives %q", name, own[name], value)
				}
			}
			for name, value := range own {
				if _, given := mises[name]; !given {
					t.Errorf("%s is %q; mise gives it no value", name, value)
				}
			}
		})
	}
}

// appended writes text at the end of the file.
func appended(t *testing.T, file, text string) {
	t.Helper()
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, append(content, text...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// hijacking is a program, committed to a repository, that writes the
// slot of the runner sandbox it runs in and says so: what the
// repository's mise.toml puts first on the path in place of mkdir, which
// the shell around an action runs.
const hijacking = "#!/bin/sh\nif (: > /slot/hijacked) 2>/dev/null; then rm -f /slot/hijacked; echo WROTE-THE-SLOT; fi\nexec /bin/mkdir \"$@\"\n"

// What a repository's mise.toml says reaches its action's command, and
// nothing of what confines the action: neither a program of its own
// first on the path, nor a variable that says where the action's root
// is. The grid runs the program with nothing confining it not at all
// (refusedUnconfined); the control is the command's own output, which
// says the repository's program ran.
func TestARepositorysEnvironmentReachesNothingThatConfinesItsAction(t *testing.T) {
	p := provisioner(t)
	ctx := context.Background()
	repository := func(t *testing.T) string {
		t.Helper()
		root := t.TempDir()
		testpins.Go(t, filepath.Join(root, "repo"))
		appended(t, filepath.Join(root, "repo", "mise.toml"), "\n[env]\n_.path = [\"./bin\"]\nroot = \"/tmp/elsewhere\"\nNONCE = \""+time.Now().Format(time.RFC3339Nano)+"\"\n")
		if err := os.MkdirAll(filepath.Join(root, "repo", "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "repo", "bin", "mkdir"), []byte(hijacking), 0o755); err != nil {
			t.Fatal(err)
		}
		return root
	}
	action := func(root string, arguments ...string) exec.Action {
		return exec.Action{Label: "//repo:probe", Root: root, WorkingDirectory: "repo", Inputs: []string{"repo/bin/mkdir"}, Arguments: arguments, Platform: p.Platform}
	}

	root := repository(t)
	refusedUnconfined(t, ctx, p, action(root, "/bin/sh", "-c", "PATH=$PWD/bin:$PATH; mkdir -p made"))

	for way, tools := range named {
		t.Run(way, func(t *testing.T) {
			// The action's command finds the repository's program first,
			// as mise said, and runs it confined.
			confined := used(t, ctx, p, action(repository(t), "/bin/sh", "-c", `command -v mkdir; echo "root is $root"`), "repo", tools, false, nil)
			done := executed(t, ctx, p, confined)
			if done.ExitCode != 0 {
				t.Fatalf("exit %d: %s", done.ExitCode, done.Stderr)
			}
			printed := string(done.Stdout)
			if strings.Contains(printed, "WROTE-THE-SLOT") {
				t.Errorf("the repository's program ran where nothing confined it:\n%s", printed)
			}
			if !strings.Contains(printed, "/repo/bin/mkdir") || !strings.Contains(printed, "root is /tmp/elsewhere") {
				t.Errorf("the command did not run with what the repository's mise.toml says:\n%s", printed)
			}
		})
	}
}

// An action's first word is the program it runs, whatever it looks like:
// one that reads as a variable, A=1, is not set and the next word run in
// its place. The control is a first word that is a program.
func TestAnActionRunsItsFirstWordAsItsProgram(t *testing.T) {
	p := provisioner(t)
	ctx := context.Background()
	for way, tools := range named {
		t.Run(way, func(t *testing.T) {
			run := func(arguments ...string) exec.Result {
				t.Helper()
				action := used(t, ctx, p, rooted(t, p, arguments...), "", tools, false, nil)
				done := executed(t, ctx, p, action)
				return done
			}
			if control := run("/bin/sh", "-c", "echo ran with $A"); control.ExitCode != 0 || !strings.Contains(string(control.Stdout), "ran with") {
				t.Fatalf("a program as the first word did not run, so this test proves nothing: exit %d: %s", control.ExitCode, control.Stderr)
			}
			if done := run("A=1", "/bin/sh", "-c", "echo ran with $A"); done.ExitCode == 0 || strings.Contains(string(done.Stdout), "ran with") {
				t.Errorf("A=1 was taken as a variable and the next word run: exit %d, stdout %q", done.ExitCode, done.Stdout)
			}
		})
	}
}

// A tool is held to its lock. What is fetched for it must have the
// checksum the lock holds, or nothing is run with it: a lock that only
// had to be there, and not to be right, would pin nothing.
func TestAToolThatIsNotWhatTheLockSaysIsRefused(t *testing.T) {
	p := provisioner(t)
	root := t.TempDir()
	testpins.Go(t, root)
	lock := filepath.Join(root, "mise.lock")
	pinned, err := os.ReadFile(lock)
	if err != nil {
		t.Fatal(err)
	}
	wrong := regexp.MustCompile(`sha256:[0-9a-f]{8}`).ReplaceAll(pinned, []byte("sha256:00000000"))
	if bytes.Equal(wrong, pinned) {
		t.Fatal("the lock holds no checksum to make wrong")
	}
	if err := os.WriteFile(lock, wrong, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = p.Use(context.Background(), exec.Action{Root: root, Arguments: []string{"go", "version"}, Platform: p.Platform}, "", []string{"go"}, false, nil)

	if err == nil || !strings.Contains(err.Error(), "the tool go: mise install exited") {
		t.Errorf("err = %v, want the fetch of go refused", err)
	}
}

func TestAToolTheLockHoldsNoChecksumOfIsRefused(t *testing.T) {
	p := provisioner(t)
	root := t.TempDir()
	testpins.Go(t, root)

	_, err := p.Use(context.Background(), exec.Action{Root: root, Arguments: []string{"node", "--version"}}, "", []string{"node"}, false, nil)
	if err == nil || !strings.Contains(err.Error(), `pins no tool "node"`) {
		t.Errorf("err = %v", err)
	}
}

// reached prints each of the places it is given that answered it over
// HTTPS: a host the sandbox's policy refuses is refused at its proxy, and
// one with no network at all is not found. Of each that did not answer,
// it says on its standard error how curl ended and when it had looked
// the host up, connected and shaken hands, so that a place that should
// have answered says where it stopped.
const reached = `for to in "$@"; do
  took=$(curl -s -o /dev/null --max-time 10 -w '%{time_namelookup} %{time_connect} %{time_appconnect}' "$to")
  ended=$?
  if [ "$ended" -eq 0 ]; then echo "$to"; else echo "$to: curl exited $ended; looked up, connected, shook hands at (s): $took" >&2; fi
done`

// A fetch is sent where the network is reached (exec.Reaching, as stage
// sends one), and reaches the hosts the egress slots are let reach and
// nothing else; an action that is no fetch reaches nothing at all.
func TestAFetchReachesTheHostsItMayAndNothingElse(t *testing.T) {
	p := provisioner(t)
	ctx := context.Background()
	const listed = "https://proxy.golang.org/"
	places := []string{listed, "https://example.com/", "https://1.1.1.1/"}

	for name, kind := range map[string]struct {
		fetch bool
		want  string
	}{
		"a fetch":                    {true, listed},
		"an action that is no fetch": {false, ""},
	} {
		for way, tools := range named {
			t.Run(name+" "+way, func(t *testing.T) {
				action := used(t, ctx, p, rooted(t, p, append([]string{"/bin/sh", "-c", reached, "sh"}, places...)...), "", tools, kind.fetch, nil)
				if kind.fetch {
					action.Platform = exec.Reaching(action.Platform)
				}

				result, err := p.Executor.Execute(ctx, action)

				if err != nil {
					t.Fatal(err)
				}
				if result.ExitCode != 0 {
					t.Fatalf("exit %d: %s", result.ExitCode, result.Stderr)
				}
				if got := strings.Join(strings.Fields(string(result.Stdout)), " "); got != kind.want {
					t.Errorf("it reached %q, want %q; what did not answer:\n%s", got, kind.want, result.Stderr)
				}
			})
		}
	}
}

// hooked is a repository that pins a tool with a step of the
// repository's own to run once the tool is installed: what Rust's pin
// has, for the Rust project's installer.
func hooked(t *testing.T, step string) string {
	t.Helper()
	root := t.TempDir()
	lock, err := os.ReadFile(filepath.Join("testdata", "nono", "mise.lock"))
	if err != nil {
		t.Fatal(err)
	}
	pin := "# " + time.Now().String() + "\n[tools.\"github:nolabs-ai/nono\"]\nversion = \"0.81.0\"\npostinstall = '''\n" + step + "\n'''\n"
	for file, content := range map[string][]byte{"mise.toml": []byte(pin), "mise.lock": lock} {
		if err := os.WriteFile(filepath.Join(root, file), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// A repository's pins can say what to run when a tool is installed, and
// a repository is not the worker's to trust. What is run to install a
// tool is confined as an action is: to its own root.
func TestAToolIsInstalledConfinedToItsOwnRoot(t *testing.T) {
	p := provisioner(t)
	use := func(step string) error {
		_, err := p.Use(context.Background(), exec.Action{Root: hooked(t, step), Arguments: []string{"nono", "--version"}, Platform: p.Platform}, "", []string{"github:nolabs-ai/nono"}, false, nil)
		return err
	}

	if err := use(`: > "$MISE_TOOL_INSTALL_PATH/hooked"`); err != nil {
		t.Fatalf("a step that writes in its own root was refused, so this test proves nothing: %v", err)
	}
	for name, step := range map[string]string{
		"wrote the slot":          `if (: > /slot/planted) 2>/dev/null; then echo "it did what it may not" >&2; exit 1; fi`,
		"wrote beside its root":   `if (: > "$PWD/../escaped") 2>/dev/null; then echo "it did what it may not" >&2; exit 1; fi`,
		"wrote the worker's disk": `if (: > /tmp/escaped) 2>/dev/null; then echo "it did what it may not" >&2; exit 1; fi`,
	} {
		if err := use(step); err != nil {
			t.Errorf("the step %s: %v", name, err)
		}
	}
}

// wrote prints each of the places it is given that it could write.
const wrote = `for at in "$@"; do if (: > "$at") 2>/dev/null; then echo "$at"; fi; done`

// A runner sandbox runs one action after another, and what one action
// left beside its root, in the slot, the next would find. An action, and
// a fetch, writes its own root and the devices that keep nothing: not
// beside its root, nor a temporary directory of the system's.
func TestAnActionWritesItsOwnRootAndNothingOfTheWorkers(t *testing.T) {
	p := provisioner(t)
	ctx := context.Background()
	places := []string{"made", "/dev/null", "/tmp/left", "/var/tmp/left", "../left"}

	refusedUnconfined(t, ctx, p, rooted(t, p, append([]string{"/bin/sh", "-c", wrote, "sh"}, places...)...))

	for name, fetch := range map[string]bool{"an action": false, "a fetch": true} {
		for way, tools := range named {
			confined := used(t, ctx, p, rooted(t, p, append([]string{"/bin/sh", "-c", wrote, "sh"}, places...)...), "", tools, fetch, nil)
			result := executed(t, ctx, p, confined)
			if got := strings.Join(strings.Fields(string(result.Stdout)), " "); got != "made /dev/null" {
				t.Errorf("%s %s wrote %q, want its own root and the device that keeps nothing (exit %d: %s)", name, way, got, result.ExitCode, result.Stderr)
			}
		}
	}
}

// The repository's pins are what its tools are installed by; a file
// written for an action at their path would stand in their place.
func TestNothingWrittenForAnActionStandsInPlaceOfThePins(t *testing.T) {
	p := provisioner(t)
	root := t.TempDir()
	testpins.Go(t, filepath.Join(root, "repo"))
	for _, at := range []string{"repo/mise.toml", "repo/mise.lock", "repo/go.sum"} {
		for _, read := range []bool{false, true} {
			action := exec.Action{
				Label:     "//repo:go.fetch/a.com/x@v1.0.0",
				Root:      root,
				Arguments: []string{"go", "version"},
				Platform:  p.Platform,
				Written:   map[string]exec.Written{at: {Content: []byte("[tools]\n")}},
			}
			if read {
				// The repository's own file, as an input: it is given the
				// repository's pins whole, and none written over them.
				if at == "repo/go.sum" {
					continue
				}
				action.Written, action.Inputs = nil, []string{at}
				used, err := p.Use(context.Background(), action, "repo", []string{"go"}, true, nil)
				if err != nil || !slices.Contains(used.Inputs, "repo/mise.toml") || !slices.Contains(used.Inputs, "repo/mise.lock") || len(used.Written) != 0 {
					t.Errorf("an action that reads %s: %v, inputs %v, written %d; want the pins whole", at, err, used.Inputs, len(used.Written))
				}
				continue
			}
			_, err := p.Use(context.Background(), action, "repo", []string{"go"}, true, nil)
			switch {
			case at == "repo/go.sum" && err != nil:
				t.Errorf("a file beside the pins was refused: %v", err)
			case at != "repo/go.sum" && (err == nil || !strings.Contains(err.Error(), "tool pins")):
				t.Errorf("%s was not refused as the pins: %v", at, err)
			}
		}
	}
}

// An action is given the pins of its own tools: repositories whose pins
// differ in a task or in another tool give an action of go the same
// thing to run with, and one whose environment differs does not.
func TestAnActionIsGivenThePinsOfItsToolsAlone(t *testing.T) {
	p := provisioner(t)
	given := func(edit func(config string) string) map[string]exec.Written {
		t.Helper()
		root := t.TempDir()
		testpins.Own(t, filepath.Join(root, "repo"))
		at := filepath.Join(root, "repo", "mise.toml")
		config, err := os.ReadFile(at)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(at, []byte(edit(string(config))), 0o644); err != nil {
			t.Fatal(err)
		}
		action, err := p.Use(context.Background(), exec.Action{
			Label: "//repo:go.compile", Root: root, Arguments: []string{"go", "version"}, Platform: p.Platform,
		}, "repo", []string{"go"}, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, input := range action.Inputs {
			if strings.HasSuffix(input, "mise.toml") || strings.HasSuffix(input, "mise.lock") {
				t.Errorf("the repository's %s is an input, whole", input)
			}
		}
		return action.Written
	}
	same := func(edit func(string) string) bool {
		a, b := given(func(c string) string { return c }), given(edit)
		if len(a) != 2 || len(b) != 2 {
			t.Fatalf("written %d and %d files; want the two pins", len(a), len(b))
		}
		for at, each := range a {
			if !bytes.Equal(each.Content, b[at].Content) {
				return false
			}
		}
		return true
	}
	if !same(func(c string) string { return c + "\n[tasks.another]\nrun = \"true\"\n" }) {
		t.Error("a task changed what an action of go is given")
	}
	if !same(func(c string) string { return strings.Replace(c, "[tools]\n", "[tools]\njq = \"1.7.1\"\n", 1) }) {
		t.Error("another tool's pin changed what an action of go is given")
	}
	if same(func(c string) string { return strings.Replace(c, "[env]\n", "[env]\nFRONTSEAT_PROBE = \"1\"\n", 1) }) {
		t.Error("the environment did not change what an action of go is given")
	}
}

// What an action must be given as written (a Resource's exports, which
// a repository's catalog says) is given so, or the action is not run:
// where mise sets it, for a tool or for the repository's mise.toml, or
// where what runs and confines the action sets it itself, it would be
// replaced, and is refused by name; so in a repository with no
// mise.toml. The control is a name nothing else sets, which reaches the
// command as written, beside one the action's own environment set and
// mise replaces as it does for every action.
func TestWhatAnActionMustBeGivenAsWrittenIsRefusedWhereAnythingElseSetsIt(t *testing.T) {
	p := provisioner(t)
	ctx := context.Background()
	repository := func(t *testing.T, pinned bool) string {
		t.Helper()
		root := t.TempDir()
		testpins.Go(t, filepath.Join(root, "repo"))
		config := filepath.Join(root, "repo", "mise.toml")
		if !pinned {
			if err := os.Remove(config); err != nil {
				t.Fatal(err)
			}
			return root
		}
		appended(t, config, "\n[env]\nDATABASE_URL = \"the repository's\"\nPLUGINS = \"the repository's\"\n")
		return root
	}
	action := func(root string) exec.Action {
		return exec.Action{
			Label: "//repo:go.test", Root: root, WorkingDirectory: "repo", Platform: p.Platform,
			Arguments:   []string{"/bin/sh", "-c", `echo "$CACHE_URL|$PLUGINS"`},
			Environment: map[string]string{"PLUGINS": "the plugin's"},
		}
	}
	for _, c := range []struct {
		way    string
		tools  []string
		pinned bool
		kept   []string
	}{
		{"naming no tool", nil, true, []string{"DATABASE_URL", "HOME", "TMPDIR", "PATH", "MISE_ENV_FILE", "PLUGINS"}},
		{"naming a tool", []string{"go"}, true, []string{"DATABASE_URL", "GOROOT", "PATH", "MISE_DISABLE_TOOLS"}},
		{"with no mise.toml", nil, false, []string{"PATH", "HOME", "A-B", ""}},
	} {
		t.Run(c.way, func(t *testing.T) {
			root := repository(t, c.pinned)
			for _, name := range c.kept {
				_, err := p.Use(ctx, action(root), "repo", c.tools, false, map[string]string{name: "the Resource's"})
				if err == nil || !strings.Contains(err.Error(), "must be given ") {
					t.Errorf("%q, set by something else or no name, was not refused by name: %v", name, err)
				}
			}
			given := used(t, ctx, p, action(root), "repo", c.tools, false, map[string]string{"CACHE_URL": "the Resource's"})
			done := executed(t, ctx, p, given)
			plugins := "the plugin's"
			if c.pinned {
				plugins = "the repository's"
			}
			if got, want := strings.TrimSpace(string(done.Stdout)), "the Resource's|"+plugins; done.ExitCode != 0 || got != want {
				t.Fatalf("exit %d: the action was told %q, want %q: %s", done.ExitCode, got, want, done.Stderr)
			}
		})
	}
}

// Nothing an action is given, its own environment or what it is given as
// written, reaches what starts or confines it: a library its plugin names
// to be preloaded is loaded by its command alone, never by what starts it
// before it is confined, and reaches the command as written. The command
// is given no environment of the grid's: all of it is in what
// frontseat-confine is told. The control is the same library put in the
// environment the grid starts the action with, which the command loads
// and is told: the count sees a load where there is one.
func TestNothingAnActionIsGivenReachesWhatStartsOrConfinesIt(t *testing.T) {
	p := provisioner(t)
	ctx := context.Background()
	root := t.TempDir()
	testpins.Go(t, filepath.Join(root, "repo"))
	const missing = "/no/such/library.so"
	// Each run of the test runs the action, and is never answered by
	// what the grid kept of one before: what is proven is what runs.
	fresh := time.Now().Format(time.RFC3339Nano)
	action := func(environment map[string]string) exec.Action {
		return exec.Action{
			Label: "//repo:go.test", Root: root, WorkingDirectory: "repo", Platform: p.Platform,
			Arguments:   []string{"/bin/sh", "-c", `printf '%s|%s\n' "$LD_PRELOAD" "$NODE_OPTIONS"`, fresh},
			Environment: environment,
		}
	}
	for _, c := range []struct {
		way         string
		own, within map[string]string
	}{
		{"in its own environment", map[string]string{"LD_PRELOAD": missing, "NODE_OPTIONS": "--max-old-space-size=64"}, nil},
		{"given as written", nil, map[string]string{"LD_PRELOAD": missing, "NODE_OPTIONS": "--max-old-space-size=64"}},
	} {
		t.Run(c.way, func(t *testing.T) {
			given := used(t, ctx, p, action(c.own), "repo", nil, false, c.within)
			if len(given.Environment) > 0 {
				t.Errorf("the grid is told to start the action with %v", given.Environment)
			}
			done := executed(t, ctx, p, given)
			if got, want := strings.TrimSpace(string(done.Stdout)), missing+"|--max-old-space-size=64"; got != want {
				t.Errorf("the command was told %q, want %q: %s", got, want, done.Stderr)
			}
			// The command, /bin/sh, is the one program that loads it.
			if loads := strings.Count(string(done.Stderr), missing); loads != 1 {
				t.Errorf("what the action was given was loaded %d times, not by its command alone: %s", loads, done.Stderr)
			}
		})
	}
	t.Run("the control: in the environment the grid starts it with", func(t *testing.T) {
		given := used(t, ctx, p, action(nil), "repo", nil, false, nil)
		given.Environment = map[string]string{"LD_PRELOAD": missing}
		done := executed(t, ctx, p, given)
		if loads := strings.Count(string(done.Stderr), missing); loads < 1 || !strings.Contains(string(done.Stdout), missing) {
			t.Fatalf("a library in the grid's environment was loaded %d times, and the command told %q: the count sees no load, so this test proves nothing: %s", loads, done.Stdout, done.Stderr)
		}
	})
}

// A confiner built for the workers is of the form the daemon writes its
// specs for: the form is reckoned alike on the daemon's platform and the
// workers'. And confined there, a file of commands with no #! line whose
// path reads as an option is run as a file by the workers' own shell.
func TestTheWorkersConfinerIsOfTheDaemonsFormAndRunsAScriptAsAFile(t *testing.T) {
	testrequired.Stack(t)
	testbox.Runtime(t)
	bb := confiner(t)
	workers := strings.TrimPrefix(testgrid.Platform()["container-image"], "docker://")
	out, err := osexec.Command("docker", "run", "--rm", "-v", bb+":/bb:ro", "--entrypoint", confine.Confiner, workers, "form").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != confine.Form {
		t.Fatalf("the workers' confiner says it is of form %q (%v); the daemon writes for %q", out, err, confine.Form)
	}

	root := t.TempDir()
	here := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(here, "-c"), 0o777); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, here, filepath.Join(here, "-c")} {
		if err := os.Chmod(dir, 0o777); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(here, "-c", "said"), []byte("echo \"said $1 in $PWD\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec, err := confine.Encode(confine.Spec{Home: ".frontseat/home", Temporary: ".frontseat/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	out, err = osexec.Command("docker", "run", "--rm", "--user", "65534", "-v", root+":"+root, "-v", bb+":/bb:ro", "-w", here,
		"--entrypoint", confine.Confiner, workers, spec, "..", "-c/said", "it").CombinedOutput()
	if got := strings.TrimSpace(string(out)); err != nil || got != "said it in "+here {
		t.Errorf("the workers' shell ran -c/said as %q (%v), want it run as a file", got, err)
	}
}

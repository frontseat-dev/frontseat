package compare_test

import (
	"context"
	"flag"
	"fmt"
	"github.com/frontseat-dev/frontseat/server/testsupport/testidentity"
	"github.com/frontseat-dev/frontseat/server/testsupport/testowner"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/frontseat-dev/frontseat/server/testsupport/testforge"

	"github.com/frontseat-dev/frontseat/sdk/go"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/server/testsupport/testworkload"
)

var (
	asked   = flag.Bool("compare", false, "run the comparison: build the same workspace with frontseat and with each other tool on this machine")
	modules = flag.Int("modules", 1000, "how many modules the workspace has")
	group   = flag.Int("group", 10, "how many modules are in a group, whose first the rest need")
	builds  = flag.Int("builds", 5, "how many times each case is built after the first build")
	pause   = flag.Duration("pause", 0, "how long to wait between changing a file and building")
	only    = flag.String("only", "", "the one tool to measure, by name; all of them if empty")
	salt    = flag.String("salt", "", "what makes the workspace one no cache has seen; the time, if empty")
)

// A tool is a build tool and its workspace.
type tool struct {
	name string
	// about says what it is, or why it is left out.
	about string
	// change writes a file of its workspace, where the workspace is.
	change func(t *testing.T, name, content string)
	// counted is how long the tool itself says a build took, from what
	// the command printed: its daemon's count, without the command's own
	// starting and reaching it. Nil for a tool that does not say.
	counted *regexp.Regexp
	// build builds everything, and returns what the command printed.
	build func(t *testing.T) (printed string)
	// ran says how many actions a build ran, from what it printed. It is
	// asked after the build has been timed.
	ran func(t *testing.T, printed string) string
	// endpoint is the build API, for frontseat; empty for other tools.
	endpoint string
	// fixture is the private Frontseat workspace, as its owner reaches
	// it, and sandbox the sandbox it owns; nil and empty for other tools.
	fixture *frontseat.Workspace
	sandbox string
	// dir is the workspace of a tool that builds one on this machine, and
	// try builds one target of it, and says what it printed and whether it
	// failed; empty and nil for other tools.
	dir string
	try func(t *testing.T, target string) (string, error)
	// close lets go of what the tool holds, its daemon or server, before
	// the test ends: so that a round's are not held through the next.
	close func()
}

// built is one build: how long the whole command took, what the tool
// says it ran, and whether it was late: whether a build straight after
// it, with nothing changed in between, still found something to run,
// which is a change the first build did not see.
type built struct {
	took time.Duration
	// counted is how long the tool itself says it took; zero where it
	// does not say.
	counted time.Duration
	ran     string
	late    bool
	// want is what it should have run; empty where nobody says.
	want string
}

// missed says whether a build did not build what had changed: it ran
// other than what the change makes there is to run, or the build after
// it found something left.
func (b built) missed() bool { return b.late || (b.want != "" && b.ran != b.want) }

// timed builds with a tool once.
func (tl tool) timed(t *testing.T) built {
	t.Helper()
	start := time.Now()
	printed := tl.build(t)
	took := time.Since(start)
	one := built{took: took, ran: tl.ran(t, printed)}
	if tl.counted != nil {
		if found := tl.counted.FindStringSubmatch(printed); found != nil {
			one.counted, _ = time.ParseDuration(found[1])
		}
	}
	return one
}

// several builds a case several times, with what makes it that case done
// before each, and returns the builds from the quickest to the slowest.
// Each is followed by a build that is not timed, which finds nothing to
// run unless the one before it missed what had changed.
func (tl tool) several(t *testing.T, want string, before func(n int)) []built {
	t.Helper()
	out := make([]built, 0, *builds)
	for n := range *builds {
		before(n)
		time.Sleep(*pause)
		one := tl.timed(t)
		after := tl.ran(t, tl.build(t))
		one.want, one.late = want, after != "0"
		t.Logf("%s, build %d: %d ms, ran %s; the build after it ran %s", tl.name, n+1, one.took.Milliseconds(), one.ran, after)
		out = append(out, one)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].took < out[j].took })
	return out
}

// said is builds as a table shows them: the middle one, the range, and
// what the tool ran in each.
func said(all []built) string {
	times := map[string]int{}
	late := 0
	for _, b := range all {
		times[b.ran]++
		if b.missed() {
			late++
		}
	}
	var each []string
	for ran, n := range times {
		if len(times) == 1 {
			each = append(each, ran)
			continue
		}
		each = append(each, fmt.Sprintf("%s in %d", ran, n))
	}
	sort.Strings(each)
	middle := all[len(all)/2].took
	missed := ""
	switch {
	case late > 0 && all[0].want == "0":
		missed = fmt.Sprintf("; RAN something with nothing changed in %d of %d", late, len(all))
	case late > 0:
		missed = fmt.Sprintf("; MISSED the change in %d of %d", late, len(all))
	}
	own := ""
	if counted := all[len(all)/2].counted; counted > 0 {
		own = fmt.Sprintf(", of which the tool counts %d", counted.Milliseconds())
	}
	return fmt.Sprintf("%d ms (%d to %d%s); ran %s%s", middle.Milliseconds(), all[0].took.Milliseconds(), all[len(all)-1].took.Milliseconds(), own, strings.Join(each, ", "), missed)
}

// onThisMachine changes a file of a workspace that is a directory of
// this machine.
func onThisMachine(dir string) func(t *testing.T, name, content string) {
	return func(t *testing.T, name, content string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, filepath.FromSlash(name))), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(content), 0o666); err != nil {
			t.Fatal(err)
		}
	}
}

// output runs a command in a directory and returns what it printed, to
// either stream.
func output(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// program returns a tool's program: the one named in the environment,
// or the one of its name on the path; and whether it runs.
func program(env, name string) (string, bool) {
	path := os.Getenv(env)
	if path == "" {
		path = name
	}
	if err := exec.Command(path, "--version").Run(); err != nil {
		return path, false
	}
	return path, true
}

// stopBazel lets go of what a Bazel at a path kept for a workspace in a
// directory: its outputs, and its server.
func stopBazel(path, dir string) {
	for _, args := range [][]string{{"clean", "--expunge"}, {"shutdown"}} {
		cmd := exec.Command(path, args...)
		cmd.Dir = dir
		_ = cmd.Run()
	}
}

func bazel(t *testing.T, shape testworkload.Shape) tool {
	path, runs := program("COMPARE_BAZEL", "bazel")
	if !runs {
		return tool{name: "Bazel", about: "left out: " + path + " does not run here (COMPARE_BAZEL names another)"}
	}
	// Which Bazel it is is asked once, and written into the workspace:
	// a launcher that is not told asks the network before every command.
	version := strings.TrimSpace(output(t, t.TempDir(), nil, path, "--version"))
	dir := t.TempDir()
	if err := testworkload.Bazel(dir, shape, strings.TrimPrefix(version, "bazel "), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopBazel(path, dir) })
	processes := regexp.MustCompile(`INFO: (\d+) process`)
	return tool{name: "Bazel", about: version + ", its workspace and its actions on this machine", change: onThisMachine(dir), counted: regexp.MustCompile(`Elapsed time: ([0-9.]+s)`), build: func(t *testing.T) string {
		return output(t, dir, nil, path, "build", "//...")
	}, ran: func(_ *testing.T, out string) string {
		if found := processes.FindStringSubmatch(out); found != nil {
			// One of them is Bazel's own, and runs nothing.
			n, _ := strconv.Atoi(found[1])
			return strconv.Itoa(n - 1)
		}
		return "?"
	}}
}

func buck2(t *testing.T, shape testworkload.Shape) tool {
	path, runs := program("COMPARE_BUCK2", "buck2")
	if !runs {
		return tool{name: "Buck2", about: "left out: " + path + " does not run here (COMPARE_BUCK2 names another)"}
	}
	dir := t.TempDir()
	if err := testworkload.Buck2(dir, shape); err != nil {
		t.Fatal(err)
	}
	version := strings.TrimSpace(output(t, dir, nil, path, "--version"))
	t.Cleanup(func() {
		cmd := exec.Command(path, "kill")
		cmd.Dir = dir
		_ = cmd.Run()
	})
	return tool{name: "Buck2", about: version + ", its workspace and its actions on this machine", change: onThisMachine(dir), build: func(t *testing.T) string {
		return output(t, dir, nil, path, "build", "//...")
	}, ran: func(_ *testing.T, out string) string {
		// A build that ran nothing prints no count, so what the last
		// build ran is asked of Buck2 itself; and what is not a build
		// that succeeded has no count at all.
		if !strings.Contains(out, "BUILD SUCCEEDED") {
			return "?"
		}
		cmd := exec.Command(path, "log", "what-ran")
		cmd.Dir = dir
		listed, err := cmd.Output()
		if err != nil {
			return "?"
		}
		ran := 0
		for _, line := range strings.Split(string(listed), "\n") {
			if strings.Contains(line, "\t") {
				ran++
			}
		}
		return strconv.Itoa(ran)
	}}
}

// frontseatInASandbox is frontseat as a person runs it: the command, and a sandbox
// that holds the daemon and the workspace, with the actions
// on the local grid. The source is cloned into the sandbox, and a file
// is changed there, over the sandbox's SSH, as an editor changes one:
// nothing of the workspace is on this machine.
func frontseatInASandbox(t *testing.T, shape testworkload.Shape) tool {
	const repository = "compare"
	remotes := t.TempDir()
	if err := os.Chmod(remotes, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(remotes, repository)
	if err := testworkload.Frontseat(source, shape, repository); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--quiet", "--initial-branch", "main"},
		{"add", "."},
		{"-c", "user.name=compare", "-c", "user.email=compare@example.com", "commit", "--quiet", "-m", "the workspace"},
	} {
		output(t, source, nil, "git", args...)
	}
	// The command line, as this module's go.mod names it a tool: so the
	// module requires the client, and what changes the client is what
	// these comparisons are built from.
	command := filepath.Join(t.TempDir(), "frontseat")
	output(t, "", nil, "go", "build", "-o", command, "github.com/frontseat-dev/frontseat/cli/cmd/frontseat")

	srv, plane, served := servedPlane(t)
	beneath.Store(served.Sandboxes)
	t.Cleanup(srv.Close)
	var shell io.Closer
	release := sync.OnceFunc(func() {
		if shell != nil {
			_ = shell.Close()
		}
		// Bind cleanup while the original listener is still open, so the
		// two addresses cannot coincide. Closing the original refuses a
		// request that arrives later and waits for the calls it admitted;
		// a shell's tunnel is no such call, which is why the shell is
		// closed first and the plane last.
		second := httptest.NewServer(plane)
		srv.Close()
		// This plane is private to this fixture, so every workspace it
		// lists is the fixture's own. The test context is cancelled before
		// this runs; deletion has its own deadline and must finish while
		// the plane still runs.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		after, err := frontseat.New(second.URL, served.Owner)
		if err != nil {
			t.Fatal(err)
		}
		listed, err := frontseat.Collect(after.Workspaces(ctx))
		if err != nil {
			t.Errorf("listing the comparison's private workspaces for cleanup: %v", err)
		} else {
			for _, listedWorkspace := range listed {
				// Until it is gone, as the command line ends one.
				if err := after.Workspace(listedWorkspace.GetName()).Delete(ctx); err != nil && connect.CodeOf(err) != connect.CodeNotFound {
					t.Errorf("ending comparison workspace %s: %v", listedWorkspace.GetName(), err)
				}
			}
		}
		second.Close()
		plane.Close()
	})
	// Registered before creation, so a setup that fails and a creation
	// whose reply is lost are covered too.
	t.Cleanup(release)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	fs, err := frontseat.New(srv.URL, served.Owner)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := fs.Open(ctx, frontseat.WorkspaceSpec{
		Image:         testbox.Image(t),
		Repositories:  []frontseat.Repository{{Remote: testforge.Serve(t, remotes) + "/" + repository, Ref: "main"}},
		AuthorizedKey: testidentity.Authorized(),
	})
	if err != nil {
		t.Fatal(err)
	}
	made, daemon := opened.Workspace, opened.Status.GetStatus().GetBuildUri()
	lives, err := workspaceSandbox(ctx, made.Name())
	if err != nil {
		t.Fatal(err)
	}
	connection, err := testidentity.Shell(context.Background(), made)
	if err != nil {
		t.Fatal(err)
	}
	shell = connection

	run := regexp.MustCompile(`(?m)^(build_[0-9a-f]+)  `)
	return tool{
		name:     "frontseat",
		endpoint: daemon,
		fixture:  made,
		sandbox:  lives,
		close:    release,
		counted:  regexp.MustCompile(`(?m)^build_[0-9a-f]+  .* in ([0-9.]+(?:ms|s))$`),
		about:    "this checkout: the daemon and the workspace in a sandbox, a file changed there over SSH, its actions on the local grid",
		build: func(t *testing.T) string {
			return output(t, "", []string{"XDG_CONFIG_HOME=" + testowner.LayOut(t, served.TokenFile, served.URL)}, command, "build", "--workspace", made.Name())
		},
		ran: func(t *testing.T, out string) string {
			found := run.FindStringSubmatch(out)
			if found == nil {
				return "?"
			}
			got, err := made.BuildByID(found[1]).Describe(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			return strconv.Itoa(int(got.GetCost().GetActionsExecuted()))
		},
		// A change is written by a command in the sandbox: nothing is copied
		// into one.
		change: func(t *testing.T, name, content string) {
			session, err := connection.NewSession()
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			session.Stdin = strings.NewReader(content)
			at := path.Join("/work", repository, name)
			if err := session.Run("cat > '" + strings.ReplaceAll(at, "'", `'\''`) + "'"); err != nil {
				t.Fatalf("writing %s in the sandbox: %v", at, err)
			}
		},
	}
}

// TestCompare builds the same workspace with each tool, and says how
// long each took: the first build, a build with nothing changed, one
// with a file changed that nothing else needs, and one with a file
// changed that its group needs. It is run when asked for (-compare):
// it takes minutes, and measures the machine it runs on.
func TestCompare(t *testing.T) {
	if !*asked {
		t.Skip("the comparison is run when asked for: go test ./specs/compare/ -run TestCompare -v -args -compare")
	}
	shape := testworkload.Shape{Modules: *modules, Group: *group, Salt: *salt}
	if shape.Salt == "" {
		shape.Salt = time.Now().Format(time.RFC3339Nano)
	}
	// A module nothing needs, and the first of its group.
	alone, needed := testworkload.Module(1), testworkload.Module(0)
	var table strings.Builder
	fmt.Fprintf(&table, "\n%d modules of %d actions, in groups of %d; %d builds a case after the first, the middle one shown, with the quickest and the slowest\n\n", shape.Modules, len(testworkload.Actions), shape.Group, *builds)
	fmt.Fprintf(&table, "| | the first build | nothing changed | a file nothing else needs changed | a file its group needs changed |\n|---|---|---|---|---|\n")
	var about []string
	if *builds < 1 {
		t.Fatal("-builds is how many times each case is built, and is one or more")
	}
	for _, each := range []struct {
		name string
		make func(*testing.T, testworkload.Shape) tool
	}{{"frontseat", frontseatInASandbox}, {"Bazel", bazel}, {"Buck2", buck2}} {
		if *only != "" && !strings.EqualFold(*only, each.name) {
			continue
		}
		tl := each.make(t, shape)
		about = append(about, tl.name+": "+tl.about)
		if tl.build == nil {
			continue
		}
		// What each case must run: every action, none, those of one
		// module, and those of a group.
		every, one, group := len(testworkload.Actions)*shape.Modules, len(testworkload.Actions), len(testworkload.Actions)*min(shape.Group, shape.Modules)
		first := tl.timed(t)
		first.want = strconv.Itoa(every)
		unchanged := tl.several(t, "0", func(int) {})
		change := func(module string) func(int) {
			return func(n int) {
				tl.change(t, module+"/word.go", testworkload.Word(fmt.Sprintf("%s changed %d at %d", module, n, time.Now().UnixNano())))
			}
		}
		alone := tl.several(t, strconv.Itoa(one), change(alone))
		shared := tl.several(t, strconv.Itoa(group), change(needed))
		began := fmt.Sprintf("%d ms; ran %s", first.took.Milliseconds(), first.ran)
		if first.missed() {
			began += fmt.Sprintf(", NOT the %s there are", first.want)
		}
		fmt.Fprintf(&table, "| %s | %s | %s | %s | %s |\n", tl.name, began, said(unchanged), said(alone), said(shared))
	}
	fmt.Fprintf(&table, "\n%s\n", strings.Join(about, "\n"))
	t.Log(table.String())
}

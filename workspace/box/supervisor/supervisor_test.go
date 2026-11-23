package supervisor_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/frontseat-dev/frontseat/common/record/buildfile"
	sourcev1 "github.com/frontseat-dev/frontseat/common/told/sourceapi/frontseat/source/v1alpha1"
	"github.com/frontseat-dev/frontseat/common/told/sourceapi/frontseat/source/v1alpha1/sourcev1alpha1connect"
	"github.com/frontseat-dev/frontseat/workspace/box/supervisor"
)

// held is a box's supervisor over a directory of the test's, with the
// sandbox's lock in a place of the test's, serving until the test ends.
func held(t *testing.T, name string) *supervisor.Supervisor {
	t.Helper()
	root := t.TempDir()
	s, err := supervisor.Open(supervisor.Config{Source: "127.0.0.1:0", Workspace: filepath.Join(root, "work"), Name: name, Lock: filepath.Join(root, "kept", "box.lock")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-served:
		case <-time.After(30 * time.Second):
			t.Error("the supervisor was still serving thirty seconds after it was stopped")
		}
	})
	return s
}

// A box's supervisor works for the control plane, and knows no build: it
// serves the source API, which says what the box holds, and no call of
// the build API leads anywhere on it. The control is the source API,
// answered on the same address.
func TestASupervisorServesTheSourceAPIAndNoBuildAPI(t *testing.T) {
	s := held(t, "payments")
	at := "http://" + s.Source()

	told := sourcev1alpha1connect.NewSourceServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, at)))
	now, err := told.WaitSource(t.Context(), &sourcev1.WaitSourceRequest{})
	if err != nil {
		t.Fatalf("the control: the source API did not answer: %v", err)
	}
	if now.GetSource() == nil {
		t.Fatalf("the control: the source API said nothing of what the box holds: %v", now)
	}

	for _, call := range []string{"/frontseat.build.v1alpha1.BuildService/CreateBuild", "/frontseat.build.v1alpha1.GraphService/ListEntities", "/frontseat.build.v1alpha1.RunService/CreateRun"} {
		answer, err := http.Post(at+call, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		_ = answer.Body.Close()
		if answer.StatusCode != http.StatusNotFound {
			t.Errorf("%s on a supervisor answered %s; want it not found: a supervisor serves no build", call, answer.Status)
		}
	}
}

// A sandbox holds one supervisor: the first to start there holds the
// sandbox's one lock, and another is refused, saying which workspace is
// managed already, whatever it was told. The control is the supervisor
// that starts once the first is gone.
func TestASandboxHoldsOneSupervisor(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "kept", "box.lock")
	first, err := supervisor.Sole(lock, "payments")
	if err != nil {
		t.Fatalf("the first supervisor of a sandbox was refused: %v", err)
	}
	// A second is refused whatever it was told, a name or none.
	for _, told := range []string{"payments", "", "another"} {
		if _, err := supervisor.Sole(lock, told); err == nil || !strings.Contains(err.Error(), "payments") || !strings.Contains(err.Error(), "one supervisor") {
			t.Fatalf("a second in the sandbox, told %q: %v, want it refused, naming the workspace", told, err)
		}
	}
	first()
	again, err := supervisor.Sole(lock, "")
	if err != nil {
		t.Fatalf("one started once the first was gone was refused: %v", err)
	}
	if _, err := supervisor.Sole(lock, "payments"); err == nil || !strings.Contains(err.Error(), "one supervisor") {
		t.Fatalf("one beside another told no workspace: %v, want it refused", err)
	}
	again()
}

// A supervisor is told the workspace it holds, by the control plane that
// made its sandbox: one told none is refused, since the command line
// knows a box by the workspace its lock names. Asked wrongly, a word
// after its flags or a flag that is none of its own, it says so and
// serves nothing. The control is TestASupervisorServesTheSourceAPIAndNoBuildAPI.
func TestASupervisorToldNoWorkspaceOrAskedWronglyServesNothing(t *testing.T) {
	root := t.TempDir()
	if _, err := supervisor.Open(supervisor.Config{Source: "127.0.0.1:0", Workspace: root, Lock: filepath.Join(root, "box.lock")}); err == nil || !strings.Contains(err.Error(), "--name") {
		t.Errorf("a supervisor told no workspace: %v; want it refused, naming --name", err)
	}
	for _, wrongly := range [][]string{{"serve"}, {"--name", "payments", "up"}, {"--listen", "127.0.0.1:0"}} {
		if err := supervisor.Run(t.Context(), wrongly); err != supervisor.ErrUsage {
			t.Errorf("asked %q: %v; want %v", wrongly, err, supervisor.ErrUsage)
		}
	}
}

// A program of the box stands in its workspace: its working directory is the
// workspace's, so that what it starts begins there, and what it was told
// by a path beside where it was started is still found.
func TestAProgramStandsInItsWorkspace(t *testing.T) {
	started := t.TempDir()
	t.Chdir(started)
	for _, made := range []string{"work", "kept"} {
		if err := os.Mkdir(made, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	workspace, state, nothing := "work", "kept", ""
	plugins := []string{"kept/a.wasm", "/opt/b.wasm"}
	if err := supervisor.Stood(&workspace, plugins, &state, &nothing); err != nil {
		t.Fatal(err)
	}
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	real := func(path string) string {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	if real(here) != real(filepath.Join(started, "work")) || real(workspace) != real(here) {
		t.Errorf("it stands in %s and its workspace is %s, want both the workspace's directory", here, workspace)
	}
	if !filepath.IsAbs(state) || real(state) != real(filepath.Join(started, "kept")) {
		t.Errorf("its state is kept in %s, want the directory beside where it was started", state)
	}
	if !filepath.IsAbs(plugins[0]) || plugins[1] != "/opt/b.wasm" || nothing != "" {
		t.Errorf("what it was told by a path is now %q and %v, and what it was told nothing of %q", plugins[0], plugins[1], nothing)
	}
	missing := "no-such-directory"
	if err := supervisor.Stood(&missing, nil); err == nil {
		t.Error("a workspace that is no directory was stood in")
	}
}

// A supervisor writes what a build in its workspace is told, as the
// control plane told it by its arguments, the grid, its asset service
// and its workers' platform, with the box's plugins, where a build there
// reads its settings, before it serves anything. Told no grid, it writes
// nothing: the control.
func TestASupervisorWritesWhatABuildInItsWorkspaceIsTold(t *testing.T) {
	root := t.TempDir()
	settings := filepath.Join(root, "kept", "build.yaml")
	s, err := supervisor.Open(supervisor.Config{Source: "127.0.0.1:0", Workspace: filepath.Join(root, "work"), Name: "payments", Lock: filepath.Join(root, "kept", "box.lock"), Settings: settings,
		Build: buildfile.File{Plugins: "/opt/frontseat/plugins", Grid: buildfile.Grid{Address: "grid:8980", Assets: "grid:8983"}, Platform: map[string]string{"OSFamily": "linux", "ISA": "aarch64"}}})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	told, err := buildfile.Read(settings)
	if err != nil {
		t.Fatal(err)
	}
	if told.Version != buildfile.Version || told.Plugins != "/opt/frontseat/plugins" || told.Grid.Address != "grid:8980" || told.Grid.Assets != "grid:8983" || told.Platform["ISA"] != "aarch64" || len(told.Platform) != 2 {
		t.Errorf("a build is told %+v; want the grid, its assets, the platform and the plugins as the supervisor was told them", told)
	}

	beside := t.TempDir()
	none := filepath.Join(beside, "build.yaml")
	quiet, err := supervisor.Open(supervisor.Config{Source: "127.0.0.1:0", Workspace: filepath.Join(beside, "work"), Name: "payments", Lock: filepath.Join(beside, "box.lock"), Settings: none})
	if err != nil {
		t.Fatal(err)
	}
	quiet.Close()
	if _, err := os.Stat(none); err == nil {
		t.Error("the control: a supervisor told no grid wrote settings")
	}
	// Told a grid and nothing of what its workers are, it writes nothing
	// and does not start: a build would guess this machine's.
	guessed := t.TempDir()
	if _, err := supervisor.Open(supervisor.Config{Source: "127.0.0.1:0", Workspace: filepath.Join(guessed, "work"), Name: "payments", Lock: filepath.Join(guessed, "box.lock"), Settings: filepath.Join(guessed, "build.yaml"),
		Build: buildfile.File{Plugins: "/opt/frontseat/plugins", Grid: buildfile.Grid{Address: "grid:8980"}}}); err == nil || !strings.Contains(err.Error(), "--platform") {
		t.Errorf("a supervisor told a grid and no platform: %v; want it refused, naming --platform", err)
	}
	if _, err := os.Stat(filepath.Join(guessed, "build.yaml")); err == nil {
		t.Error("a supervisor told a grid and no platform wrote settings")
	}
	// A property that is none, of no name, or given twice, is refused
	// where it is asked.
	for _, wrongly := range [][]string{{"--name", "payments", "--platform", "ISA"}, {"--name", "payments", "--platform", "=linux"}, {"--name", "payments", "--platform", "ISA=a", "--platform", "ISA=b"}} {
		if err := supervisor.Run(t.Context(), wrongly); err != supervisor.ErrUsage {
			t.Errorf("asked %q: %v; want %v", wrongly, err, supervisor.ErrUsage)
		}
	}
}

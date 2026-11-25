package e2e

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/daemon/life"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/testbuild"
	"github.com/frontseat-dev/frontseat/sdk/go"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/workspace/server/controlplanetest"
)

// local serves a control plane over the local stack's OpenShell gateway,
// as frontseat-server does, and returns it. Without a grid the test is
// skipped, before a gateway is asked for.
func local(t *testing.T) controlplanetest.Served {
	t.Helper()
	return controlplanetest.Start(t)
}

// endLeft ends what a frontseat-server of the test's, stopped, left in
// the local stack's gateway: the sandboxes recorded in its state.
func endLeft(t *testing.T, identity, state string) {
	t.Helper()
	if err := controlplanetest.EndLeft(identity, state); err != nil {
		t.Log(err)
	}
}

// machine is a machine of a test's own, as the command line knows one:
// a configuration directory, which names a control plane nothing serves
// and holds no token, so that a command there has no workspace to ask.
type machine struct {
	t *testing.T
	// command is the command line's program, and kept its configuration
	// directory's frontseat, where it keeps what it knows and its build
	// daemons their state.
	command, config, kept string
}

// aMachine is a machine told nothing of its grid.
func aMachine(t *testing.T, command string) *machine {
	t.Helper()
	config := t.TempDir()
	kept := filepath.Join(config, "frontseat")
	if err := os.MkdirAll(filepath.Join(kept, "local"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kept, "local", "server"), []byte("http://127.0.0.1:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &machine{t: t, command: command, config: config, kept: kept}
}

// asked runs the command line in a directory, on the machine, and
// returns what it printed and how it ended.
func (m *machine) asked(directory string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, m.command, args...)
	cmd.Dir = directory
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+m.config)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// does is asked, of a command that is to succeed: one that does not
// fails the test with what it printed and what the machine's build
// daemons said.
func (m *machine) does(directory string, args ...string) string {
	m.t.Helper()
	out, err := m.asked(directory, args...)
	if err != nil {
		logs, _ := filepath.Glob(filepath.Join(m.kept, "build", "*", "daemon.log"))
		for _, each := range logs {
			said, _ := os.ReadFile(each)
			m.t.Logf("%s:\n%s", each, said)
		}
		m.t.Fatalf("frontseat %v, with no workspace: %v\n%s", args, err, out)
	}
	return out
}

// toldItsGrid tells the machine its grid, as a job or a person tells one
// (build.yaml, docs/build-settings.md): the plugins this checkout built
// into dist/, the local stack's grid and asset service, its workers'
// platform, the instance asked of, where one is named, and the other
// instances it reads, in order (grid.reads). The build
// daemons the command line then starts there are stopped when the test
// ends.
func (m *machine) toldItsGrid(root, grid, assets, instance string, reads ...string) {
	m.t.Helper()
	settings := fmt.Sprintf("version: 1\nplugins: %s\ngrid:\n  address: %s\n  assets: %s\n", filepath.Join(root, "dist", "plugins"), grid, assets)
	if instance != "" {
		settings += fmt.Sprintf("  instance: %s\n", instance)
	}
	if len(reads) > 0 {
		settings += fmt.Sprintf("  reads: [%s]\n", strings.Join(reads, ", "))
	}
	settings += "platform:\n"
	for name, value := range testgrid.Platform() {
		settings += fmt.Sprintf("  %s: %q\n", name, value)
	}
	if err := os.WriteFile(filepath.Join(m.kept, "build.yaml"), []byte(settings), 0o600); err != nil {
		m.t.Fatal(err)
	}
	// Whatever becomes of a build, the daemon it started is stopped, by
	// what it said of itself: it is this test's own.
	m.t.Cleanup(func() {
		for _, state := range m.daemons() {
			said, err := life.Told(state)
			if err != nil {
				continue
			}
			request, err := http.NewRequest(http.MethodPost, "http://"+said.Address+life.StopPath, nil)
			if err != nil {
				m.t.Error(err)
				continue
			}
			request.Header.Set("Authorization", "Bearer "+said.Token)
			if answer, err := http.DefaultClient.Do(request); err == nil {
				_ = answer.Body.Close()
			}
			// It has ended once it has let go of its directory: until
			// then it still writes there, what it knows of the files it
			// built among it, and the directory is not the test's to
			// remove.
			for asked := time.Now(); life.Held(state); time.Sleep(20 * time.Millisecond) {
				if time.Since(asked) > 30*time.Second {
					m.t.Errorf("the build daemon asked to stop still holds %s half a minute on", state)
					break
				}
			}
		}
	})
}

// daemons are the state directories of the build daemons the command
// line started on the machine.
func (m *machine) daemons() []string {
	states, _ := filepath.Glob(filepath.Join(m.kept, "build", "*"))
	return states
}

// builds are what each build of the machine's one build daemon says of
// its actions, newest first, read from the daemon as the command line
// reaches it: at the address and with the token it says of itself.
func (m *machine) builds() []testbuild.Results {
	m.t.Helper()
	states := m.daemons()
	if len(states) != 1 {
		m.t.Fatalf("the machine keeps the state of %d build daemons (%v), want its one", len(states), states)
	}
	said, err := life.Told(states[0])
	if err != nil {
		m.t.Fatal(err)
	}
	fs := sdkClient(m.t, "http://"+said.Address, frontseat.WithToken(said.Token))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	listed, err := frontseat.Collect(fs.Here().Builds(ctx))
	if err != nil {
		m.t.Fatal(err)
	}
	var all []testbuild.Results
	for _, each := range listed {
		results, err := testbuild.ResultsOf(ctx, fs.Here().BuildByID(each.GetId()))
		if err != nil {
			m.t.Fatal(err)
		}
		all = append(all, results)
	}
	return all
}

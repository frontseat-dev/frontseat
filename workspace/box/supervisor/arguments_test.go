package supervisor

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/common/record/buildfile"
)

// What the control plane tells a supervisor of a build by its arguments
// is what a build is told, each where it belongs and nowhere else: the
// grid in the grid's address, its asset service in the grid's assets,
// the plugins and each property of the workers. The control is a
// supervisor told nothing of a build, which is told none.
func TestWhatASupervisorIsToldOfABuildByItsArguments(t *testing.T) {
	told, err := asked([]string{"--name", "payments", "--grid", "grid:8980", "--assets", "assets:8983", "--plugins", "/opt/frontseat/plugins", "--platform", "OSFamily=linux", "--platform", "ISA=aarch64"})
	if err != nil {
		t.Fatal(err)
	}
	want := buildfile.File{Plugins: "/opt/frontseat/plugins", Grid: buildfile.Grid{Address: "grid:8980", Assets: "assets:8983"}, Platform: map[string]string{"OSFamily": "linux", "ISA": "aarch64"}}
	if !reflect.DeepEqual(told.Build, want) || told.Name != "payments" {
		t.Errorf("told %+v of a build, for the workspace %q; want %+v, for payments", told.Build, told.Name, want)
	}
	none, err := asked([]string{"--name", "payments"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(none.Build, buildfile.File{}) {
		t.Errorf("the control: told nothing of a build, it is told %+v", none.Build)
	}
}

// A supervisor told the grid's instance its builds run under, and the
// other instances they read, writes them into what a build in its
// workspace is told (grid.instance, grid.reads), the instances read in
// the order given, whole. The control is the same supervisor told
// neither, which writes neither.
func TestASupervisorToldItsInstanceAndWhatItReadsWritesThem(t *testing.T) {
	written := func(args ...string) string {
		t.Helper()
		settings, err := begun(t, append([]string{"--grid", "grid:8980"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		said, err := os.ReadFile(settings)
		if err != nil {
			t.Fatal(err)
		}
		return string(said)
	}
	const told = "version: 1\nplugins: /opt/frontseat/plugins\ngrid:\n    address: grid:8980\n    instance: frontseat/p\n    reads:\n        - frontseat/b\n        - frontseat/c\nplatform:\n    OSFamily: linux\n"
	if got := written("--instance", "frontseat/p", "--reads", "frontseat/b", "--reads", "frontseat/c"); got != told {
		t.Errorf("a build is told\n%s\nwant\n%s", got, told)
	}
	const none = "version: 1\nplugins: /opt/frontseat/plugins\ngrid:\n    address: grid:8980\nplatform:\n    OSFamily: linux\n"
	if got := written(); got != none {
		t.Errorf("the control: told no instance, a build is told\n%s\nwant\n%s", got, none)
	}
}

// A supervisor is refused at its start the instances its build daemon
// would refuse when it reads what the supervisor wrote, by the one check
// of them both make (buildfile.Grid.CheckInstances), and writes nothing;
// and one told an instance, or instances to read, and no grid is refused
// by name, never starting as told no grid. The control is the same
// supervisor told instances its daemon reads, which starts.
func TestASupervisorIsRefusedTheInstancesItsDaemonWouldRefuse(t *testing.T) {
	if _, err := begun(t, "--grid", "grid:8980", "--instance", "frontseat/p", "--reads", "frontseat/b"); err != nil {
		t.Fatalf("the control: %v", err)
	}
	for name, said := range map[string]struct {
		args    []string
		refused string
	}{
		"an instance with a space":      {[]string{"--grid", "grid:8980", "--instance", "frontseat p"}, "grid.instance holds a space"},
		"an instance of a word kept":    {[]string{"--grid", "grid:8980", "--instance", "frontseat/blobs"}, "grid.instance has a part that is a word the Remote Execution API keeps"},
		"its own instance read":         {[]string{"--grid", "grid:8980", "--instance", "frontseat/p", "--reads", "frontseat/p"}, "grid.reads' entry 1 is the daemon's own instance"},
		"an instance read of no name":   {[]string{"--grid", "grid:8980", "--instance", "frontseat/p", "--reads", ""}, "grid.reads' entry 1 names no instance"},
		"an instance read twice":        {[]string{"--grid", "grid:8980", "--reads", "frontseat/b", "--reads", "frontseat/b"}, "grid.reads' entry 2 is its entry 1 again"},
		"an instance read with a space": {[]string{"--grid", "grid:8980", "--reads", "frontseat b"}, "grid.reads' entry 1 holds a space"},
		"an instance and no grid":       {[]string{"--instance", "frontseat/p"}, "grid.address names none"},
		"instances read and no grid":    {[]string{"--reads", "frontseat/b"}, "grid.address names none"},
	} {
		settings, err := begun(t, said.args...)
		if err == nil || !strings.Contains(err.Error(), said.refused) {
			t.Errorf("%s: %v; want it refused, saying %q", name, err, said.refused)
		}
		if _, there := os.Stat(settings); there == nil {
			t.Errorf("%s: a supervisor refused its instances wrote what a build is told", name)
		}
		for i, arg := range said.args[1:] {
			if told := said.args[i]; (told == "--instance" || told == "--reads") && arg != "" && err != nil && strings.Contains(err.Error(), arg) {
				t.Errorf("%s: %v repeats %q, which it refused", name, err, arg)
			}
		}
	}
}

// begun is a supervisor of a workspace of the test's own started, and
// closed, with these arguments beside its name, plugins and platform:
// where it keeps what a build is told, and what refused its start.
func begun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	told, err := asked(append([]string{"--name", "payments", "--plugins", "/opt/frontseat/plugins", "--platform", "OSFamily=linux"}, args...))
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	root := t.TempDir()
	told.Source, told.Workspace, told.Lock, told.Settings = "127.0.0.1:0", filepath.Join(root, "work"), filepath.Join(root, "kept", "box.lock"), filepath.Join(root, "kept", "build.yaml")
	s, err := Open(*told)
	if err == nil {
		s.Close()
	}
	return told.Settings, err
}

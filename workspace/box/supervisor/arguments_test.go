package supervisor

import (
	"reflect"
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

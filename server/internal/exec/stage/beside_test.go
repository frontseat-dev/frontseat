package stage

import (
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/exec"
)

// What a test's runner sandbox holds is written where nothing of the
// action's own stands: not a file of the repository it reads, not one
// written for it, not one another action left. A file beside the place
// is the control.
func TestNothingOfTheActionsStandsWhereItsRunnerSandboxIsWritten(t *testing.T) {
	for _, c := range []struct {
		what    string
		inputs  []string
		written map[string]exec.Written
		held    map[string]exec.Held
		taken   bool
	}{
		{"a file of the repository read there", []string{RunnerSandbox}, nil, nil, true},
		{"a file written for the action there", nil, map[string]exec.Written{RunnerSandbox: {}}, nil, true},
		{"what another action left there", nil, nil, map[string]exec.Held{RunnerSandbox: {}}, true},
		{"a source inside a provider's private root", []string{RuntimeDirectory + "/service-0/config"}, nil, nil, true},
		{"a generated ancestor", nil, nil, map[string]exec.Held{".frontseat": {Directory: true}}, true},
		{"a written file inside a provider's private root", nil, map[string]exec.Written{RuntimeDirectory + "/service-0/config": {}}, nil, true},
		{"files beside it", []string{"lab/a/go.mod", ".frontseat/runner-sandbox.binpb.old"}, map[string]exec.Written{"lab/a/x": {}}, map[string]exec.Held{".frontseat/home/go/bin": {}}, false},
		{"nothing", nil, nil, nil, false},
	} {
		if got := taken(c.inputs, c.written, c.held); got != c.taken {
			t.Errorf("%s: taken %v, want %v", c.what, got, c.taken)
		}
	}
}

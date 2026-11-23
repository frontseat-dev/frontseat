package tools_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools"
	"github.com/frontseat-dev/frontseat/local/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/testsupport/testpins"
)

// sent keeps the platform each action was sent with, by its label, and
// answers as installing does.
type sent struct {
	mu        sync.Mutex
	platforms map[string]map[string]string
}

func (s *sent) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	s.mu.Lock()
	s.platforms[action.Label] = action.Platform
	s.mu.Unlock()
	return installing{}.Execute(ctx, action)
}

// The install of a tool fetches it, so it is sent to the runner sandboxes
// that reach the network; what asks mise for the tool's environment
// reaches nothing and is sent to the others. Neither changes the
// provisioner's platform, which is every action's.
func TestTheInstallOfAToolIsSentWhereTheNetworkIsReached(t *testing.T) {
	root := t.TempDir()
	testpins.Go(t, filepath.Join(root, "repo"))
	grid := &sent{platforms: map[string]map[string]string{}}
	p := &tools.Provisioner{Executor: grid, Platform: testgrid.Platform()}
	action := exec.Action{Label: "//repo/module:compile", Root: root, WorkingDirectory: "repo/module", Arguments: []string{"go", "build", "./..."}, Platform: p.Platform}
	if _, err := p.Use(context.Background(), action, "repo", []string{"go"}, false, nil); err != nil {
		t.Fatal(err)
	}
	installs := 0
	for label, platform := range grid.platforms {
		if strings.HasPrefix(label, "mise install ") {
			installs++
			if platform[exec.NetworkProperty] != exec.Egress {
				t.Errorf("%s was sent with %v, which reaches no network", label, platform)
			}
		} else if _, named := platform[exec.NetworkProperty]; named {
			// The control: what does not fetch is not sent there.
			t.Errorf("%s, which fetches nothing, was sent with %v", label, platform)
		}
	}
	if installs == 0 {
		t.Fatalf("no tool was installed: asked %v", grid.platforms)
	}
	if _, named := p.Platform[exec.NetworkProperty]; named {
		t.Error("the provisioner's platform, every action's, was changed")
	}
}

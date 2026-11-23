// Package toolstest answers, for a test whose grid runs nothing, what
// the tools package asks a grid itself: the environment mise gives an
// action's tools (tools.Provisioner's environ), and the directories an
// installation makes. One answer, for every such grid of the tests.
package toolstest

import (
	"encoding/json"
	"path"
	"sort"
	"strings"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
)

// root is the root a grid that runs nothing says it asked mise in.
const root = "/worker/build/0/root"

// Asks reports whether an action asks mise what its tools put in the
// environment.
func Asks(action exec.Action) bool {
	return action.Label == "mise env" || strings.HasPrefix(action.Label, "mise env ")
}

// Environment is what mise would answer it: each tool it holds on the
// path, before the system's directories.
func Environment(action exec.Action) exec.Result {
	var dirs []string
	for at := range action.Held {
		if strings.Contains(at, "/mise/installs/") {
			dirs = append(dirs, path.Join(root, at, "bin"))
		}
	}
	sort.Strings(dirs)
	printed, _ := json.Marshal(map[string]string{"PATH": strings.Join(append(dirs, "/usr/bin", "/bin"), ":")})
	return exec.Result{Stdout: append([]byte(root+"\n"), printed...)}
}

// Installed is what a grid answers an action that installs tools: each
// output it names, made, as a directory.
func Installed(action exec.Action) exec.Result {
	left := map[string]exec.Held{}
	for _, output := range action.Outputs {
		left[output] = exec.Held{Digest: exec.Digest{Hash: strings.Repeat("0", 64), Size: 1}, Directory: true}
	}
	return exec.Result{Outputs: left}
}

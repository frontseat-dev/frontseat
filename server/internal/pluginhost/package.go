package pluginhost

import (
	"fmt"
	"strings"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// CheckPackage holds an action that says it makes what its project
// ships (Action.package) to what the contract says of one: each file it
// leaves is an artifact of a release, so it leaves at least one thing,
// each a file it is sure to leave; and it is a step of a build that
// makes something, and no test, check, fetch, shared action or program
// to run. What is the action, as a mistake names it.
func CheckPackage(what string, action *pluginv1.Action) error {
	if !action.GetPackage() {
		return nil
	}
	for _, kind := range []struct {
		is   bool
		what string
	}{{action.GetTest(), "a test"}, {action.GetCheck(), "a check"}, {action.GetFetch(), "a fetch"}, {action.GetShared(), "a shared action"}, {action.GetProgram(), "a program to run"}} {
		if kind.is {
			return fmt.Errorf("%s is a package, and is %s: a package is a step of a build that makes what ships", what, kind.what)
		}
	}
	if len(action.GetOutputs()) == 0 {
		return fmt.Errorf("%s is a package and leaves nothing: what it leaves is what ships", what)
	}
	for _, output := range action.GetOutputs() {
		switch {
		case strings.HasSuffix(output.GetPath(), "/"):
			return fmt.Errorf("%s is a package and leaves the directory %q: an artifact is a file", what, output.GetPath())
		case output.GetOptional():
			return fmt.Errorf("%s is a package and may not leave %q: what ships is left, or the package failed", what, output.GetPath())
		}
	}
	return nil
}

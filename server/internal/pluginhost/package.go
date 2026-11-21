package pluginhost

import (
	"fmt"
	"strings"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// otherKind is the first of the kinds an action says it is that neither
// a package nor what publishes may be as well; empty where it is none.
func otherKind(action *pluginv1.Action) string {
	switch {
	case action.GetTest():
		return "a test"
	case action.GetCheck():
		return "a check"
	case action.GetFetch():
		return "a fetch"
	case action.GetShared():
		return "a shared action"
	case action.GetProgram():
		return "a program to run"
	}
	return ""
}

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
	if kind := otherKind(action); kind != "" {
		return fmt.Errorf("%s is a package, and is %s: a package is a step of a build that makes what ships", what, kind)
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

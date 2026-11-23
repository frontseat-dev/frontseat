package pluginhost

import (
	"fmt"
	"strings"

	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
)

// CheckPackage holds an action of the kind Package to what the contract
// says of one: each file it leaves is an artifact of a release, so it
// leaves at least one thing, each a file it is sure to leave. What is
// the action, as a mistake names it.
func CheckPackage(what string, action *pluginv1.Action) error {
	if action.GetPackage() == nil {
		return nil
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

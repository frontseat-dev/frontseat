package plan

import "strings"

// NoTests is what a build of tests is refused with where what it was
// pointed at holds no action that runs tests: Bazel ends a `bazel test`
// of no test targets so, with exit code 4 (NO_TESTS_FOUND).
type NoTests struct {
	// Targets are what the build was pointed at; none is the workspace.
	Targets []string
}

func (n *NoTests) Error() string {
	if len(n.Targets) == 0 {
		return "nothing the build was asked for runs tests: no action of it is one its plugin says runs tests"
	}
	return "nothing " + strings.Join(n.Targets, ", ") + " points at runs tests: no action of it is one its plugin says runs tests"
}

// Tests keeps, of what a build was asked, the actions that run tests, as
// the plugin that declared each says (its test flag), whatever they are
// named; and of what it needed, what those need. needed holds all that
// asked needs. Where asked holds no test, it is *NoTests.
func Tests(needed, asked []Action, targets []string) (runs, tests []Action, err error) {
	for _, action := range asked {
		if action.IsTest() {
			tests = append(tests, action)
		}
	}
	if len(tests) == 0 {
		return nil, nil, &NoTests{Targets: targets}
	}
	return With(needed, tests), tests, nil
}

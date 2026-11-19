package stage

import (
	"encoding/json"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/testsupport/testfiles"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/confine"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source/sourcetest"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/testsupport/testmodule"
)

// What a test is told its repository is at is said by one name, which the
// test support reads it by.
func TestATestIsToldItsRepositoryByTheNameTheTestSupportReads(t *testing.T) {
	if SourceVariable != testmodule.SourceVariable {
		t.Errorf("the host tells a test %s, and the test support reads %s", SourceVariable, testmodule.SourceVariable)
	}
}

// A test is told where its repository is, from the directory it runs in,
// whatever that is: a project at the repository's root, one beneath it,
// and a command beneath its project. What it reads beyond its package it
// finds there, and never by looking up. The control is a step of a build,
// which is told nothing of it.
func TestATestIsToldWhereItsRepositoryIs(t *testing.T) {
	snapshot := sourcetest.Taken(t, map[string]string{"go.mod": "module m\n", "server/internal/pluginhost/host.go": "package pluginhost\n"})
	for _, at := range []struct{ project, directory, want string }{
		{".", "", "."},
		{"server", "", ".."},
		{"server", "internal/pluginhost", "../../.."},
	} {
		for _, test := range []bool{true, false} {
			action := plan.Action{ProjectRoot: at.project, Declared: &pluginv1.Action{Test: test, Command: &pluginv1.Command{Arguments: []string{"true"}, Directory: at.directory}}}
			told, is := toldOf(t, snapshot, action)
			switch {
			case test && told != at.want:
				t.Errorf("a test of %s running in %q is told its repository is at %q, want %q", at.project, at.directory, told, at.want)
			case !test && is:
				t.Errorf("the control: a step is told its repository is at %q", told)
			}
		}
	}
}

// In a workspace of several repositories, a test is told where its own
// repository is in its root, which is not the root's top: the control is
// one of the workspace's first repository, told the top.
func TestATestOfAnotherRepositoryIsToldWhereThatRepositoryIs(t *testing.T) {
	root := t.TempDir()
	testfiles.Tree(t, root, map[string]string{"go.mod": "module m\n", "repos/other/lib/lib.go": "package lib\n"})
	snapshot, err := new(source.Keeper).Take(t.Context(), source.Tree{Root: root, Paths: map[string]string{"": ".", "other": "repos/other"}})
	if err != nil {
		t.Fatal(err)
	}
	for repository, want := range map[string]string{"other": "..", "": ".."} {
		action := plan.Action{Repository: repository, ProjectRoot: "lib", Declared: &pluginv1.Action{Test: true, Command: &pluginv1.Command{Arguments: []string{"true"}}}}
		if told, _ := toldOf(t, snapshot, action); told != want {
			t.Errorf("a test of lib in the repository %q is told %q, want %q", repository, told, want)
		}
	}
}

// What a test is told of its repository is the runner's to say: a
// Component's catalog file, a service's exports or the plugin's command
// that names it is refused by name. The control is another variable,
// given each way, which the test is given.
func TestATestsRepositoryIsSaidByNothingElse(t *testing.T) {
	snapshot := sourcetest.Taken(t, nil)
	for _, name := range []string{SourceVariable, "ANOTHER"} {
		for way, given := range map[string]func(*plan.Action) map[string]string{
			"its catalog file": func(a *plan.Action) map[string]string {
				a.Environment = map[string]string{name: "elsewhere"}
				return nil
			},
			"a service": func(*plan.Action) map[string]string { return map[string]string{name: "elsewhere"} },
			"its plugin": func(a *plan.Action) map[string]string {
				a.Declared.Command.Environment = map[string]string{name: "elsewhere"}
				return nil
			},
		} {
			action := plan.Action{Declared: &pluginv1.Action{Test: true, Command: &pluginv1.Command{Arguments: []string{"true"}}}}
			exported := given(&action)
			_, err := of(t.Context(), snapshot, action, installed(), nil, nil, false, exported)
			switch {
			case name == SourceVariable && (err == nil || !strings.Contains(err.Error(), "sets "+SourceVariable+" itself")):
				t.Errorf("a test given %s by %s was not refused as what its runner sets: %v", name, way, err)
			case name != SourceVariable && err != nil:
				t.Errorf("the control: a test given %s by %s was refused: %v", name, way, err)
			}
		}
	}
}

// toldOf is what a staged action is told of its repository, and whether
// it is told it.
func toldOf(t *testing.T, snapshot *source.Snapshot, action plan.Action) (string, bool) {
	t.Helper()
	staged, err := Of(t.Context(), snapshot, action, installed(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var spec confine.Spec
	if err := json.Unmarshal([]byte(staged.Arguments[1]), &spec); err != nil {
		t.Fatal(err)
	}
	for _, each := range spec.Environment {
		if each.Name == SourceVariable {
			return strings.Join(each.Parts, ""), true
		}
	}
	return "", false
}

package stage

import (
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/reapi/reapitest"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools/toolstest"
	"github.com/frontseat-dev/frontseat/build/engine/pattern"
	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
	"github.com/frontseat-dev/frontseat/testsupport/testpins"
)

// held is something another action left, by a word of its own.
func held(word string) exec.Held {
	return exec.Held{Digest: exec.Digest{Hash: word, Size: int64(len(word))}, Directory: true}
}

// workspace writes repositories, each by the directory the workspace
// holds it in and its files, has each of more write what else it writes
// beneath the workspace's directory, and returns the snapshot a run
// takes of them, each named as names says.
func workspace(t *testing.T, names map[string]string, files map[string]map[string]string, more ...func(root string)) *source.Snapshot {
	t.Helper()
	root := t.TempDir()
	for folder, each := range files {
		testfiles.Tree(t, filepath.Join(root, filepath.FromSlash(folder)), each)
	}
	for _, write := range more {
		write(root)
	}
	snapshot, err := new(source.Keeper).Take(t.Context(), source.Tree{Root: root, Paths: names})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// greeting is the files of a repository with one Go package, greet.
func greeting(word string) map[string]string {
	return map[string]string{"go.mod": "module example.com/greeting\n", "greet/greet.go": "package greet\n\nconst Word = \"" + word + "\"\n", "greet/greet_test.go": "package greet\n"}
}

// greetTest is the test of greet in the repository the workspace names
// name: it reads the repository's go.mod above its project, what its
// own build left and what it keeps in its home, and leaves a report.
func greetTest(name string) plan.Action {
	return plan.Action{
		Repository:     name,
		ProjectRoot:    "greet",
		Sources:        []pattern.Group{{Directory: "greet", Patterns: []string{"**/*.go"}}},
		WorkspaceFiles: []string{"go.mod"},
		Declared: &pluginv1.Action{
			Name:    "test",
			Command: &pluginv1.Command{Arguments: []string{"go", "test", "./..."}},
			Outputs: []*pluginv1.Output{{Path: "report/"}, {Path: "~/go/bin/", Optional: true}},
			Kind:    &pluginv1.Action_Test{Test: &pluginv1.Test{}},
		},
	}
}

// An action of a repository is one action, by one key, wherever the
// repository is built: a checkout built on its own, a workspace that
// holds it alone under a name, and one that holds it beside another
// repository under another name and in a directory of another name.
// What it is handed of its own repository's actions, and what is kept
// in its home, are handed by where they sit in the workspace, and sit
// in its root where they do for the checkout; the pins of its tool are
// written where they are for the checkout, and mise is asked what that
// tool puts in its environment by one action too. The control is a
// change to one of its files, which makes it another action wherever it
// is.
func TestAnActionOfARepositoryIsOneActionWhereverTheRepositoryIs(t *testing.T) {
	type where struct {
		name  string
		names map[string]string
		// folder is where the repository is, and other another's files.
		folder string
		other  map[string]map[string]string
	}
	places := map[string]where{
		"a checkout built on its own": {"", map[string]string{"": "."}, ".", nil},
		"alone in a workspace":        {"greeting", map[string]string{"greeting": "greeting"}, "greeting", nil},
		"beside another repository": {"hello", map[string]string{"hello": "repos/greeting", "other": "other"}, "repos/greeting", map[string]map[string]string{
			"other": {"go.mod": "module example.com/other\n"},
		}},
	}
	// keyed is the key of the test of a word where the repository is,
	// and that of the action that asked mise for its environment.
	keyed := func(word string, at where) (test, environment string) {
		t.Helper()
		files := map[string]map[string]string{at.folder: greeting(word)}
		for folder, each := range at.other {
			files[folder] = each
		}
		snapshot := workspace(t, at.names, files, func(root string) { testpins.Go(t, filepath.Join(root, filepath.FromSlash(at.folder))) })
		grid := &toolstest.Configured{}
		left := map[string]exec.Held{
			Place(at.names[at.name], "greet", "made"):         held("made"),
			Place(at.names[at.name], "greet", "~/go/pkg/mod"): held("modules"),
		}
		action := greetTest(at.name)
		action.Declared.Tools = []string{"go"}
		staged, err := Of(t.Context(), snapshot, action, &tools.Provisioner{Executor: grid, Platform: installed().Platform}, nil, left)
		if err != nil {
			t.Fatal(err)
		}
		if len(grid.Asked()) != 1 {
			t.Fatalf("mise was asked %d times for the environment of the test's tool, want once", len(grid.Asked()))
		}
		return reapitest.Key(t, staged), reapitest.Key(t, grid.Asked()[0])
	}
	alone, asked := keyed("hello", places["a checkout built on its own"])
	for name, at := range places {
		test, environment := keyed("hello", at)
		if test != alone {
			t.Errorf("the test is %s %s and %s as a checkout built on its own, want one action", test, name, alone)
		}
		if environment != asked {
			t.Errorf("mise is asked for the test's environment by %s %s and by %s for a checkout built on its own, want one action", environment, name, asked)
		}
		if other, _ := keyed("bonjour", at); other == alone {
			t.Errorf("the control: %s, the test of another word is the action of the first (%s)", name, other)
		}
	}
}

// What an action is handed of another repository is one action whatever
// directory the workspace holds either repository in. What an action of
// that repository left sits beneath tools.External, in a directory of
// the workspace's name for that repository; the files of a project of it
// the action is handed (plan.Handed) sit in the action's own project,
// where its tool looks for them, as a provided artifact and a generated
// source do. The controls: a change to the file it is handed, or to what
// was left, makes it another action.
func TestWhatAnActionIsHandedOfAnotherRepositoryIsOneActionWhereverEitherIs(t *testing.T) {
	keys := map[string]string{}
	for where, c := range map[string]struct {
		app, lib, word, left string
	}{
		"each in a directory of its name":          {"app", "lib", "hello", "made"},
		"each in a directory of another":           {"code/app", "deps/elsewhere", "hello", "made"},
		"the control: a file it is handed differs": {"app", "lib", "bonjour", "made"},
		"the control: what was left differs":       {"app", "lib", "hello", "other"},
	} {
		snapshot := workspace(t, map[string]string{"app": c.app, "lib": c.lib}, map[string]map[string]string{
			c.app: {"go.mod": "module example.com/app\n", "main/main.go": "package main\n"},
			c.lib: greeting(c.word),
		})
		action := plan.Action{
			Repository:  "app",
			ProjectRoot: "main",
			Sources:     []pattern.Group{{Directory: "main", Patterns: []string{"**/*.go"}}},
			Handed:      []plan.Handed{{Repository: "lib", Root: "greet", Patterns: []string{"**/*.go"}, Into: ".deps/greet"}},
			Declared:    &pluginv1.Action{Name: "build", Command: &pluginv1.Command{Arguments: []string{"go", "build", "./..."}}, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}}},
		}
		left := map[string]exec.Held{Place(c.lib, "greet", "made"): held(c.left)}
		staged, err := Of(t.Context(), snapshot, action, installed(), nil, left)
		if err != nil {
			t.Fatalf("%s: %v", where, err)
		}
		if _, there := staged.Held[path.Join(tools.External, "lib", "greet", "made")]; !there || len(staged.Held) != 1 {
			t.Errorf("%s: what lib left is not beneath %s/lib alone: %v", where, tools.External, staged.Held)
		}
		if copied, there := staged.Copies["main/.deps/greet"]; !there || len(staged.Copies) != 1 || !slices.Equal(copied.Files, []string{"greet.go", "greet_test.go"}) {
			t.Errorf("%s: the files of lib's project are not in main/.deps/greet alone: %v", where, staged.Copies)
		}
		keys[where] = reapitest.Key(t, staged)
	}
	if was, is := keys["each in a directory of its name"], keys["each in a directory of another"]; was != is {
		t.Errorf("the action is %s with each repository in a directory of its name and %s in another, want one action", was, is)
	}
	for _, control := range []string{"the control: a file it is handed differs", "the control: what was left differs"} {
		if keys[control] == keys["each in a directory of its name"] {
			t.Errorf("%s, and the action is the same one", control)
		}
	}
}

// Where a place of the workspace sits in an action's root: its own
// repository's at the top, another's beneath tools.External by its name,
// what is in no repository's directory where it is, and its home where
// it is, even where a repository is at the workspace's top.
func TestAPlaceOfTheWorkspaceSitsInAnActionsRootByItsRepository(t *testing.T) {
	workspace := source.Tree{Paths: map[string]string{"app": "app", "lib": "deps/lib"}}
	alone := source.Tree{Paths: map[string]string{"": "."}}
	for _, c := range []struct {
		tree                 source.Tree
		repository, at, want string
	}{
		{workspace, "app", "app/greet/made", "greet/made"},
		{workspace, "app", "deps/lib/greet/made", ".frontseat/external/lib/greet/made"},
		{workspace, "lib", "deps/lib/greet/made", "greet/made"},
		{workspace, "lib", "deps/lib", "."},
		{workspace, "lib", "deps/library/made", "deps/library/made"},
		{workspace, "lib", ".frontseat/home/go/pkg/mod", ".frontseat/home/go/pkg/mod"},
		{alone, "", "greet/made", "greet/made"},
		{alone, "", ".frontseat/home", ".frontseat/home"},
	} {
		if got := place(c.tree, c.repository, c.at); got != c.want {
			t.Errorf("for an action of %q, %s sits at %q, want %q", c.repository, c.at, got, c.want)
		}
	}
}

// Two places of the workspace that would sit at one place of an
// action's root are refused, naming both, and neither is dropped for the
// other: what is in no repository's directory sits where it is, which
// is where its own repository's would. The control is what another
// repository left at the same path in it, which sits beneath
// tools.External, beside it.
func TestTwoPlacesOfTheWorkspaceAtOnePlaceOfTheRootAreRefused(t *testing.T) {
	snapshot := workspace(t, map[string]string{"app": "app", "lib": "lib"}, map[string]map[string]string{
		"app": {"go.mod": "module example.com/app\n", "main/main.go": "package main\n"},
		"lib": greeting("hello"),
	})
	action := plan.Action{
		Repository: "app", ProjectRoot: "main",
		Sources:  []pattern.Group{{Directory: "main", Patterns: []string{"**/*.go"}}},
		Declared: &pluginv1.Action{Name: "build", Command: &pluginv1.Command{Arguments: []string{"go", "build", "./..."}}, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}}},
	}
	for _, c := range []struct{ other, refused string }{
		{"main/made", "it is handed app/main/made and main/made of the workspace, which would both be at main/made in its root"},
		{"lib/main/made", ""},
	} {
		left := map[string]exec.Held{"app/main/made": held("own"), c.other: held("other")}
		staged, err := Of(t.Context(), snapshot, action, installed(), nil, left)
		switch {
		case c.refused != "" && (err == nil || !strings.Contains(err.Error(), c.refused)):
			t.Errorf("handed %s beside app/main/made: %v, want it refused saying %s", c.other, err, c.refused)
		case c.refused == "" && (err != nil || len(staged.Held) != 2):
			t.Errorf("the control: handed %s beside app/main/made, it holds %v: %v", c.other, staged.Held, err)
		}
	}
}

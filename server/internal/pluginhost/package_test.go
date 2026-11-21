package pluginhost

import (
	"strings"
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// A package leaves files it is sure to leave, and is a step of a build
// that makes something; the control is one that does, and an action
// that is no package, which is held to none of it.
func TestAPackageLeavesFilesAndIsAStepThatMakesSomething(t *testing.T) {
	packaged := func(change func(*pluginv1.Action)) *pluginv1.Action {
		action := &pluginv1.Action{Name: "package/linux-amd64", Package: true, Command: &pluginv1.Command{Arguments: []string{"tool"}}, Outputs: []*pluginv1.Output{{Path: "_package/greet-linux-amd64"}}}
		change(action)
		return action
	}
	for name, c := range map[string]struct {
		action *pluginv1.Action
		says   string
	}{
		"a test":                {packaged(func(a *pluginv1.Action) { a.Test = true }), "the action p is a package, and is a test: a package is a step of a build that makes what ships"},
		"a check":               {packaged(func(a *pluginv1.Action) { a.Check = true }), "is a package, and is a check"},
		"a fetch":               {packaged(func(a *pluginv1.Action) { a.Fetch = true }), "is a package, and is a fetch"},
		"a shared action":       {packaged(func(a *pluginv1.Action) { a.Shared = true }), "is a package, and is a shared action"},
		"a program to run":      {packaged(func(a *pluginv1.Action) { a.Program = true }), "is a package, and is a program to run"},
		"nothing left":          {packaged(func(a *pluginv1.Action) { a.Outputs = nil }), "the action p is a package and leaves nothing: what it leaves is what ships"},
		"a directory":           {packaged(func(a *pluginv1.Action) { a.Outputs = []*pluginv1.Output{{Path: "dist/"}} }), `the action p is a package and leaves the directory "dist/": an artifact is a file`},
		"what it may not leave": {packaged(func(a *pluginv1.Action) { a.Outputs[0].Optional = true }), `the action p is a package and may not leave "_package/greet-linux-amd64"`},
	} {
		refusedAs(t, name, CheckPackage("the action p", c.action), c.says)
	}
	for name, action := range map[string]*pluginv1.Action{
		"a package":  packaged(func(*pluginv1.Action) {}),
		"two files":  packaged(func(a *pluginv1.Action) { a.Outputs = append(a.Outputs, &pluginv1.Output{Path: "~/out/greet.tgz"}) }),
		"no package": {Name: "compile", Outputs: []*pluginv1.Output{{Path: "dist/", Optional: true}}, Test: true},
	} {
		if err := CheckPackage("the action p", action); err != nil {
			t.Errorf("%s: refused, %v", name, err)
		}
	}
}

// A package that is one of several is named "package", a slash and what
// it is of, and only a package is named so.
func TestOnlyAPackageIsNamedAsOne(t *testing.T) {
	for name, c := range map[string]struct {
		action *pluginv1.Action
		says   string
	}{
		"a package":             {&pluginv1.Action{Name: "package/greet-linux-amd64", Package: true}, ""},
		"a package of a path":   {&pluginv1.Action{Name: "package/cmd/greet-linux-amd64", Package: true}, ""},
		"a package by one name": {&pluginv1.Action{Name: "wasm", Package: true}, ""},
		"no package":            {&pluginv1.Action{Name: "package/greet-linux-amd64"}, `the action "package/greet-linux-amd64" is named as a package of the project, and is no package`},
		"of nothing":            {&pluginv1.Action{Name: "package/", Package: true}, `the action name "package/" is not package, a slash and what it is a package of`},
		"of what leaves it":     {&pluginv1.Action{Name: "package/../x", Package: true}, `is not package, a slash and what it is a package of`},
		"of a space":            {&pluginv1.Action{Name: "package/a b", Package: true}, `is not package, a slash and what it is a package of`},
	} {
		err := checkNamed(c.action)
		if c.says == "" && err != nil || c.says != "" && (err == nil || !strings.Contains(err.Error(), c.says)) {
			t.Errorf("%s: %v, want %q", name, err, c.says)
		}
	}
}

// refusedAs fails a test where a check did not refuse, saying so.
func refusedAs(t *testing.T, name string, err error, says string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), says) {
		t.Errorf("%s: %v, want %q", name, err, says)
	}
}

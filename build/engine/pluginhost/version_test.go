package pluginhost

import (
	"maps"
	"slices"
	"strings"
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
)

func versionedAction(versioned bool, arguments []string, environment map[string]string) *pluginv1.Action {
	return &pluginv1.Action{Name: "compile", Versioned: versioned, Command: &pluginv1.Command{Arguments: arguments, Environment: environment}, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}}}
}

// elsewhereIn is a versioned action that names the version in its
// command, as it may, and in something else of it, which change writes.
func elsewhereIn(change func(*pluginv1.Action)) *pluginv1.Action {
	action := versionedAction(true, []string{"tool", "{frontseat.version}"}, nil)
	change(action)
	return action
}

// A versioned action is given the version where its command names it,
// in its arguments and in the values of its environment, and nowhere
// else; the action as it was declared is left as it was, since what is
// kept of an evaluation is of no release.
func TestAVersionedActionIsGivenTheVersionWhereItNamesIt(t *testing.T) {
	declared := versionedAction(true,
		[]string{"go", "install", "-ldflags=-X main.version={frontseat.version} -X main.commit={frontseat.commit}", "{frontseat.tag}", "{frontseat.unit}", "plain"},
		map[string]string{"VERSION": "v{frontseat.version}", "PLAIN": "as it was"})
	filled := FillVersion(declared, Version{Version: "1.4.0", Tag: "greet/v1.4.0", Commit: "0123abcd", Unit: "component:lab/greet"})
	if want := []string{"go", "install", "-ldflags=-X main.version=1.4.0 -X main.commit=0123abcd", "greet/v1.4.0", "component:lab/greet", "plain"}; !slices.Equal(filled.GetCommand().GetArguments(), want) {
		t.Errorf("arguments filled as %q, want %q", filled.GetCommand().GetArguments(), want)
	}
	if want := map[string]string{"VERSION": "v1.4.0", "PLAIN": "as it was"}; !maps.Equal(filled.GetCommand().GetEnvironment(), want) {
		t.Errorf("environment filled as %v, want %v", filled.GetCommand().GetEnvironment(), want)
	}
	if declared.GetCommand().GetArguments()[3] != "{frontseat.tag}" || declared.GetCommand().GetEnvironment()["VERSION"] != "v{frontseat.version}" {
		t.Errorf("the action as declared was changed: %v", declared.GetCommand())
	}
	// A build that is no release fills in the same values each time.
	development := FillVersion(declared, Development)
	if want := []string{"go", "install", "-ldflags=-X main.version=0.0.0-dev -X main.commit=", "", "", "plain"}; !slices.Equal(development.GetCommand().GetArguments(), want) {
		t.Errorf("a build that is no release filled %q, want %q", development.GetCommand().GetArguments(), want)
	}
	// Control: an action that is not versioned is the one declared.
	plain := versionedAction(false, []string{"go", "version"}, nil)
	if FillVersion(plain, Version{Version: "1.4.0"}) != plain {
		t.Error("an action that is not versioned was made anew")
	}
}

// Only a versioned action names what the host fills, only where it is
// filled, and only what the host knows; one that is versioned names
// something, and is a step of a build.
func TestOnlyAVersionedActionNamesTheVersion(t *testing.T) {
	fix := versionedAction(true, []string{"tool", "{frontseat.version}"}, nil)
	fix.Kind = &pluginv1.Action_Check{Check: &pluginv1.Check{Fix: &pluginv1.Command{Arguments: []string{"tool", "-w", "{frontseat.version}"}}, Rewrites: []string{"*.go"}}}
	directory := versionedAction(true, []string{"tool", "{frontseat.version}"}, nil)
	directory.Command.Directory = "{frontseat.unit}"
	kinds := map[string]func(*pluginv1.Action){
		"a fetch":          func(a *pluginv1.Action) { a.Kind = &pluginv1.Action_Fetch{Fetch: &pluginv1.Fetch{}} },
		"a shared fetch":   func(a *pluginv1.Action) { a.Kind = &pluginv1.Action_SharedFetch{SharedFetch: &pluginv1.SharedFetch{}} },
		"a program to run": func(a *pluginv1.Action) { a.Kind = &pluginv1.Action_Program{Program: &pluginv1.Program{}} },
	}
	refused := map[string]struct {
		action *pluginv1.Action
		says   string
	}{
		"not versioned, in an argument": {versionedAction(false, []string{"tool", "{frontseat.version}"}, nil), `the action compile names {frontseat.version}, which only a versioned action is given`},
		"not versioned, in a value":     {versionedAction(false, []string{"tool"}, map[string]string{"V": "{frontseat.commit}"}), `the action compile names {frontseat.commit}, which only a versioned action is given`},
		"unknown":                       {versionedAction(true, []string{"tool", "{frontseat.date}"}, nil), `the action compile names {frontseat.date}, which the host fills nothing for: it fills {frontseat.version}, {frontseat.tag}, {frontseat.commit}, {frontseat.unit}`},
		"unknown, and not versioned":    {versionedAction(false, []string{"tool", "{frontseat.date}"}, nil), `the action compile names {frontseat.date}, which the host fills nothing for`},
		"in a name of the environment":  {versionedAction(true, []string{"tool", "{frontseat.version}"}, map[string]string{"{frontseat.unit}": "x"}), `the action compile names {frontseat.unit} in a name of its environment, where nothing is filled`},
		"in its directory":              {directory, `the action compile names {frontseat.unit} in its directory, where nothing is filled`},
		"in its fix":                    {fix, `the action compile names {frontseat.version} in its fix, where nothing is filled`},
		"versioned, and names nothing":  {versionedAction(true, []string{"tool"}, map[string]string{"V": "1"}), `the action compile is versioned and names nothing of the version`},
		"versioned, and runs nothing":   {&pluginv1.Action{Name: "compile", Versioned: true}, `the action compile is versioned and names nothing of the version`},
		"not closed":                    {versionedAction(false, []string{"tool", "{frontseat.version"}, nil), `the action compile names {frontseat.version, which the host fills nothing for`},
		"a capital":                     {versionedAction(true, []string{"tool", "{frontseat.version}", "{frontseat.Version}"}, nil), `the action compile names {frontseat., which the host fills nothing for`},
		"more than letters":             {versionedAction(true, []string{"tool", "{frontseat.version}", "{frontseat.commit_sha}"}, nil), `the action compile names {frontseat.commit, which the host fills nothing for`},
		"no name":                       {versionedAction(true, []string{"tool", "{frontseat.}"}, nil), `the action compile names {frontseat.}, which the host fills nothing for`},
		"in what it leaves":             {elsewhereIn(func(a *pluginv1.Action) { a.Outputs = []*pluginv1.Output{{Path: "out/{frontseat.version}/"}} }), `the action compile names something of the version in what it leaves, where nothing is filled`},
		"in a tool":                     {elsewhereIn(func(a *pluginv1.Action) { a.Tools = []string{"go@{frontseat.version}"} }), `the action compile names something of the version in a tool, where nothing is filled`},
		"in what it reads":              {elsewhereIn(func(a *pluginv1.Action) { a.Inputs = []*pluginv1.Input{SourceInput("{frontseat.unit}/**")} }), `the action compile names something of the version in what it reads, where nothing is filled`},
	}
	for kind, make := range kinds {
		action := versionedAction(true, []string{"tool", "{frontseat.version}"}, nil)
		make(action)
		refused[kind] = struct {
			action *pluginv1.Action
			says   string
		}{action, `the action compile is versioned, and is ` + kind + `: only a step of a build is given the version`}
	}
	for name, tc := range refused {
		err := CheckVersion("the action compile", tc.action)
		if tc.says == "" {
			if err != nil {
				t.Errorf("%s: refused, %v", name, err)
			}
			continue
		}
		if err == nil || !strings.HasPrefix(err.Error(), tc.says) {
			t.Errorf("%s: %v, want %q", name, err, tc.says)
		}
	}
	// Controls: versioned and naming it in an argument, in a value, in
	// both; and not versioned, naming nothing.
	for name, action := range map[string]*pluginv1.Action{
		"an argument": versionedAction(true, []string{"tool", "--version={frontseat.version}"}, nil),
		"a value":     versionedAction(true, []string{"tool"}, map[string]string{"VERSION": "{frontseat.version}"}),
		"plain":       versionedAction(false, []string{"tool", "{not.ours}", "{}"}, map[string]string{"V": "1"}),
	} {
		if err := CheckVersion("the action compile", action); err != nil {
			t.Errorf("%s: refused, %v", name, err)
		}
	}
}

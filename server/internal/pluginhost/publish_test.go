package pluginhost

import (
	"slices"
	"strings"
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

func publishing(change func(*pluginv1.Action)) *pluginv1.Action {
	action := &pluginv1.Action{
		Name: "publish/release", Tools: []string{"gh"},
		Command: &pluginv1.Command{Arguments: []string{"gh", "release", "create", "{frontseat.tag}", "{frontseat.artifacts}", "--repo", "{frontseat.remote}", "--notes-file", "{frontseat.notes}", "--prerelease={frontseat.prerelease}"}},
		Kind:    &pluginv1.Action_Publish{Publish: &pluginv1.Publish{Environment: []string{"GH_TOKEN"}}},
	}
	change(action)
	return action
}

// An action that publishes is given a release: its version, and what it
// is to publish, each artifact an argument of its own. The action as it
// was declared is left as it was.
func TestAnActionThatPublishesIsGivenTheReleaseAndItsArtifacts(t *testing.T) {
	declared := publishing(func(a *pluginv1.Action) { a.Command.Environment = map[string]string{"GH_REPO": "{frontseat.remote}"} })
	filled := FillPublish(declared, Version{Version: "1.4.0", Tag: "greet/v1.4.0", Commit: "0123abcd", Unit: "component:lab/greet"},
		Published{Artifacts: []string{"/given/checksums.txt", "/given/greet-linux-amd64"}, Notes: "/given/notes.md", Remote: "https://forge.example/acme/code", Prerelease: true})
	want := []string{"gh", "release", "create", "greet/v1.4.0", "/given/checksums.txt", "/given/greet-linux-amd64", "--repo", "https://forge.example/acme/code", "--notes-file", "/given/notes.md", "--prerelease=true"}
	if !slices.Equal(filled.GetCommand().GetArguments(), want) {
		t.Errorf("it is run as %q, want %q", filled.GetCommand().GetArguments(), want)
	}
	if filled.GetCommand().GetEnvironment()["GH_REPO"] != "https://forge.example/acme/code" || declared.GetCommand().GetArguments()[4] != Artifacts || declared.GetCommand().GetEnvironment()["GH_REPO"] != Remote {
		t.Errorf("it is told %v, and the action as declared is %v", filled.GetCommand().GetEnvironment(), declared.GetCommand())
	}
	// With nothing to publish it is given no artifact, and is run all
	// the same: its tool says what it makes of that.
	none := FillPublish(declared, Version{Tag: "t"}, Published{})
	if got := none.GetCommand().GetArguments(); !slices.Equal(got[:5], []string{"gh", "release", "create", "t", "--repo"}) {
		t.Errorf("with no artifact it is run as %q", got)
	}
}

// An action that publishes runs something and leaves nothing; what it is given of where it runs is named as a tool reads
// it, and is nothing the host sets; and only one that publishes names
// what one is given. The control is one that does.
func TestAnActionThatPublishesRunsSomethingAndLeavesNothing(t *testing.T) {
	for name, c := range map[string]struct {
		action *pluginv1.Action
		says   string
	}{
		"said to be versioned":         {publishing(func(a *pluginv1.Action) { a.Versioned = true }), "the action p publishes, and is said to be versioned, which one that publishes is"},
		"runs nothing":                 {publishing(func(a *pluginv1.Action) { a.Command = nil }), "the action p publishes and runs nothing"},
		"a program to be filled":       {publishing(func(a *pluginv1.Action) { a.Command.Arguments = []string{"{frontseat.artifacts}"} }), "names what it runs by something to be filled"},
		"leaves something":             {publishing(func(a *pluginv1.Action) { a.Outputs = []*pluginv1.Output{{Path: "out/x"}} }), `the action p publishes and says it leaves "out/x"`},
		"reads something":              {publishing(func(a *pluginv1.Action) { a.Inputs = []*pluginv1.Input{SourceInput("**")} }), "the action p publishes and says what it reads"},
		"says where it runs":           {publishing(func(a *pluginv1.Action) { a.Command.Directory = "x" }), "the action p publishes and says where it runs"},
		"a variable not one":           {publishing(func(a *pluginv1.Action) { a.GetPublish().Environment = []string{"gh token"} }), `is given the variable "gh token", which is no name a tool reads one by`},
		"a variable in small letters":  {publishing(func(a *pluginv1.Action) { a.GetPublish().Environment = []string{"gh_token"} }), "which is no name a tool reads one by"},
		"the path":                     {publishing(func(a *pluginv1.Action) { a.GetPublish().Environment = []string{"PATH"} }), "is given the variable PATH, which the host sets or keeps"},
		"a stand-in":                   {publishing(func(a *pluginv1.Action) { a.GetPublish().Environment = []string{"FRONTSEAT_CRED_A"} }), "is given the variable FRONTSEAT_CRED_A, which the host sets or keeps"},
		"a variable twice":             {publishing(func(a *pluginv1.Action) { a.GetPublish().Environment = []string{"GH_TOKEN", "GH_TOKEN"} }), "is given the variable GH_TOKEN twice"},
		"given and set":                {publishing(func(a *pluginv1.Action) { a.Command.Environment = map[string]string{"GH_TOKEN": "x"} }), "is given the variable GH_TOKEN, and sets it too"},
		"artifacts within an argument": {publishing(func(a *pluginv1.Action) { a.Command.Arguments[4] = "--files={frontseat.artifacts}" }), "names {frontseat.artifacts} within an argument: it is an argument by itself"},
		"artifacts in a value":         {publishing(func(a *pluginv1.Action) { a.Command.Environment = map[string]string{"FILES": "{frontseat.artifacts}"} }), "names {frontseat.artifacts} in the value of FILES"},
	} {
		refusedAs(t, name, CheckPublish("the action p", c.action), c.says)
	}
	for name, action := range map[string]*pluginv1.Action{
		"one that publishes":             publishing(func(*pluginv1.Action) {}),
		"given nothing of where it runs": publishing(func(a *pluginv1.Action) { a.GetPublish().Environment = nil }),
		"one that does not":              {Name: "compile", Kind: &pluginv1.Action_Test{Test: &pluginv1.Test{}}, Outputs: []*pluginv1.Output{{Path: "out/"}}},
	} {
		if err := CheckPublish("the action p", action); err != nil {
			t.Errorf("%s: refused, %v", name, err)
		}
		if err := CheckVersion("the action p", action); err != nil {
			t.Errorf("%s: what it names is refused, %v", name, err)
		}
	}
	// What one that publishes is given, no other names: not a versioned
	// action, which is given the version alone.
	versioned := versionedAction(true, []string{"tool", "{frontseat.version}", "{frontseat.notes}"}, nil)
	if err := CheckVersion("the action p", versioned); err == nil || !strings.Contains(err.Error(), "the action p names {frontseat.notes}, which only an action that publishes is given") {
		t.Errorf("a versioned action that names what a publish is given: %v", err)
	}
	plain := versionedAction(false, []string{"tool", "{frontseat.artifacts}"}, nil)
	if err := CheckVersion("the action p", plain); err == nil || !strings.Contains(err.Error(), "which only an action that publishes is given") {
		t.Errorf("an action that names what a publish is given: %v", err)
	}
}

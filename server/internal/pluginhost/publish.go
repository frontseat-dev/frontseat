package pluginhost

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"google.golang.org/protobuf/proto"
)

// variable is what a variable an action that publishes is given is named:
// as a tool reads one, in capitals, digits and underscores.
var variable = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// CheckPublish holds an action that says it publishes (Action.publish)
// to what the contract says of one: it runs something, leaves nothing,
// and is nothing else, since no build runs it and nothing reads it; the
// variables it is given are each named as a tool reads one, once, and
// none is one the host sets; and the artifacts it is given are an
// argument by themselves, since they are many. What is the action, as a
// mistake names it.
func CheckPublish(what string, action *pluginv1.Action) error {
	publish := action.GetPublish()
	if publish == nil {
		return nil
	}
	kind := otherKind(action)
	switch {
	case kind != "":
	case action.GetPackage():
		kind = "a package"
	case action.GetVersioned():
		kind = "said to be versioned, which one that publishes is"
	}
	if kind != "" {
		return fmt.Errorf("%s publishes, and is %s: one that publishes is that alone", what, kind)
	}
	switch {
	case len(action.GetCommand().GetArguments()) == 0:
		return fmt.Errorf("%s publishes and runs nothing", what)
	case strings.Contains(action.GetCommand().GetArguments()[0], begins):
		return fmt.Errorf("%s publishes and names what it runs by something to be filled: the program it runs is written out", what)
	case len(action.GetOutputs()) > 0:
		return fmt.Errorf("%s publishes and says it leaves %q: what it does is done outside, and nothing reads it", what, action.GetOutputs()[0].GetPath())
	case len(action.GetInputs()) > 0:
		return fmt.Errorf("%s publishes and says what it reads: it is given its unit's artifacts, and reads nothing else", what)
	case action.GetCommand().GetDirectory() != "":
		return fmt.Errorf("%s publishes and says where it runs: it runs beside what it is given", what)
	}
	for i, name := range publish.GetEnvironment() {
		switch {
		case !variable.MatchString(name):
			return fmt.Errorf("%s is given the variable %q, which is no name a tool reads one by", what, name)
		case name == "PATH" || name == "HOME" || name == "TMPDIR" || strings.HasPrefix(name, "MISE_") || strings.HasPrefix(name, "FRONTSEAT_"):
			return fmt.Errorf("%s is given the variable %s, which the host sets or keeps", what, name)
		case slices.Contains(publish.GetEnvironment()[:i], name):
			return fmt.Errorf("%s is given the variable %s twice", what, name)
		}
		if _, set := action.GetCommand().GetEnvironment()[name]; set {
			return fmt.Errorf("%s is given the variable %s, and sets it too", what, name)
		}
	}
	// The artifacts are many, and an argument each: named within an
	// argument, or in a value, they would be one text of no meaning.
	for _, argument := range action.GetCommand().GetArguments() {
		if strings.Contains(argument, Artifacts) && argument != Artifacts {
			return fmt.Errorf("%s names %s within an argument: it is an argument by itself, each artifact one", what, Artifacts)
		}
	}
	for name, value := range action.GetCommand().GetEnvironment() {
		if strings.Contains(value, Artifacts) {
			return fmt.Errorf("%s names %s in the value of %s: it is an argument by itself, each artifact one", what, Artifacts, name)
		}
	}
	return nil
}

// Published is what an action that publishes is given of the release it
// publishes, beside its version.
type Published struct {
	// Artifacts are the files it is given, each by where it is.
	Artifacts []string
	// Notes is the file that holds the unit's release notes.
	Notes string
	// Remote is where the unit's repository was brought from.
	Remote string
	// Prerelease is whether the release is a prerelease.
	Prerelease bool
}

// FillPublish is an action that publishes given a release: the action
// as declared, with what its command names of the version and of what
// it is given filled in, the artifacts an argument each. The action as
// declared is not changed.
func FillPublish(action *pluginv1.Action, version Version, of Published) *pluginv1.Action {
	// The artifacts are an argument each, placed before the rest is
	// filled: a file's name is not read for what it might name.
	spread := proto.Clone(action).(*pluginv1.Action)
	if spread.Command != nil {
		var arguments []string
		for _, argument := range spread.Command.GetArguments() {
			if argument == Artifacts {
				arguments = append(arguments, make([]string, len(of.Artifacts))...)
				continue
			}
			arguments = append(arguments, argument)
		}
		spread.Command.Arguments = arguments
	}
	out := given(spread, version, Notes, of.Notes, Remote, of.Remote, Prerelease, strconv.FormatBool(of.Prerelease))
	at := 0
	for _, argument := range action.GetCommand().GetArguments() {
		if argument == Artifacts {
			copy(out.Command.Arguments[at:], of.Artifacts)
			at += len(of.Artifacts)
			continue
		}
		at++
	}
	return out
}

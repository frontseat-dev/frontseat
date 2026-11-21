package pluginhost

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strings"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"google.golang.org/protobuf/proto"
)

// Version is what a versioned action is given (Action.versioned): the
// version of the release it is built for, the tag that release is
// given, the commit it is planned at and the unit released. It holds no
// time, user or machine: the build a release makes of a commit is the
// same wherever it is made.
type Version struct {
	Version, Tag, Commit, Unit string
}

// Development is what a build that is no release gives: the same each
// time, so that a versioned action is one action from a commit to the
// next, and is built once.
var Development = Version{Version: "0.0.0-dev"}

// filled is what the host fills, in the order the contract lists it,
// each by what a command writes for it between "{frontseat." and "}".
var filled = []struct {
	name string
	of   func(Version) string
}{
	{"version", func(v Version) string { return v.Version }},
	{"tag", func(v Version) string { return v.Tag }},
	{"commit", func(v Version) string { return v.Commit }},
	{"unit", func(v Version) string { return v.Unit }},
}

// begins is how a command begins what it writes to be filled. Whatever
// begins so is the host's to read: text that does not go on to name
// something the host fills, and close, is refused, so that nothing
// written to be filled is ever sent as it was written.
const begins = "{frontseat."

// beginning is begins, as a file's content is searched for it.
var beginning = []byte(begins)

// named is what follows where text begins something to be filled: the
// name written, with its braces, and whether the host fills it. What
// does not close, or holds anything but small letters, is written as
// far as it could be read.
func named(rest string) (written string, known bool) {
	end := 0
	for end < len(rest) && rest[end] >= 'a' && rest[end] <= 'z' {
		end++
	}
	if end == len(rest) || rest[end] != '}' {
		return begins + rest[:end], false
	}
	name := rest[:end]
	for _, each := range filled {
		if each.name == name {
			return begins + name + "}", true
		}
	}
	return begins + name + "}", false
}

// each calls yield with everything text writes to be filled, in order,
// until yield says to stop.
func each(text string, yield func(written string, known bool) bool) {
	for {
		at := strings.Index(text, begins)
		if at < 0 {
			return
		}
		text = text[at+len(begins):]
		if !yield(named(text)) {
			return
		}
	}
}

// mentions is whether anything of a command begins what the host
// fills.
func mentions(command *pluginv1.Command) bool {
	for _, argument := range command.GetArguments() {
		if strings.Contains(argument, begins) {
			return true
		}
	}
	for name, value := range command.GetEnvironment() {
		if strings.Contains(name, begins) || strings.Contains(value, begins) {
			return true
		}
	}
	return strings.Contains(command.GetDirectory(), begins)
}

// elsewhere is where, outside its commands, an action begins what the
// host fills: nothing of what it is named, reads, leaves, is given or
// runs with is filled. It is empty where it does nowhere.
func elsewhere(action *pluginv1.Action) string {
	if strings.Contains(action.GetName(), begins) {
		return "its name"
	}
	for _, tool := range action.GetTools() {
		if strings.Contains(tool, begins) {
			return "a tool"
		}
	}
	for _, rewrite := range action.GetRewrites() {
		if strings.Contains(rewrite, begins) {
			return "what its fix may change"
		}
	}
	for _, output := range action.GetOutputs() {
		if strings.Contains(output.GetPath(), begins) {
			return "what it leaves"
		}
	}
	for _, input := range action.GetInputs() {
		dependency := input.GetDependency()
		if strings.Contains(input.GetSource(), begins) || strings.Contains(input.GetInputSet(), begins) || strings.Contains(input.GetOutput(), begins) || strings.Contains(dependency.GetInto(), begins) {
			return "what it reads"
		}
	}
	for _, file := range action.GetFiles() {
		if strings.Contains(file.GetPath(), begins) || bytes.Contains(file.GetContent(), beginning) {
			return "a file it is given"
		}
	}
	for _, download := range action.GetDownloads() {
		if strings.Contains(download.GetUrl(), begins) || strings.Contains(download.GetInto(), begins) {
			return "what it downloads"
		}
	}
	return ""
}

// CheckVersion holds an action to what the contract says of the
// version: only a versioned action names what the host fills, only in
// its arguments and the values of its environment, and only what the
// host knows; one that is versioned names something, and is a step of a
// build. What is the action, as a mistake names it.
func CheckVersion(what string, action *pluginv1.Action) error {
	if where := elsewhere(action); where != "" {
		return fmt.Errorf("%s names something of the version in %s, where nothing is filled", what, where)
	}
	// Nearly every action is not versioned and names nothing: it is
	// passed on what it holds, with nothing made to look at it.
	if !action.GetVersioned() && !mentions(action.GetCommand()) && !mentions(action.GetFix()) {
		return nil
	}
	if action.GetVersioned() {
		for _, kind := range []struct {
			is   bool
			what string
		}{{action.GetFetch(), "a fetch"}, {action.GetShared(), "a shared action"}, {action.GetProgram(), "a program to run"}} {
			if kind.is {
				return fmt.Errorf("%s is versioned, and is %s: only a step of a build is given the version", what, kind.what)
			}
		}
	}
	command := action.GetCommand()
	var mistake error
	unfilled := func(where string, texts ...string) bool {
		for _, text := range texts {
			each(text, func(written string, _ bool) bool {
				mistake = fmt.Errorf("%s names %s in %s, where nothing is filled", what, written, where)
				return false
			})
			if mistake != nil {
				return true
			}
		}
		return false
	}
	environment := slices.Sorted(maps.Keys(command.GetEnvironment()))
	fix := action.GetFix()
	fixed := append(slices.Clone(fix.GetArguments()), fix.GetDirectory())
	for _, name := range slices.Sorted(maps.Keys(fix.GetEnvironment())) {
		fixed = append(fixed, name, fix.GetEnvironment()[name])
	}
	if unfilled("a name of its environment", environment...) || unfilled("its directory", command.GetDirectory()) || unfilled("its fix", fixed...) {
		return mistake
	}
	texts := slices.Clone(command.GetArguments())
	for _, name := range environment {
		texts = append(texts, command.GetEnvironment()[name])
	}
	any := false
	for _, text := range texts {
		each(text, func(written string, known bool) bool {
			switch {
			case !known:
				fills := make([]string, len(filled))
				for i, each := range filled {
					fills[i] = begins + each.name + "}"
				}
				mistake = fmt.Errorf("%s names %s, which the host fills nothing for: it fills %s", what, written, strings.Join(fills, ", "))
			case !action.GetVersioned():
				mistake = fmt.Errorf("%s names %s, which only a versioned action is given", what, written)
			}
			any = true
			return mistake == nil
		})
		if mistake != nil {
			return mistake
		}
	}
	if action.GetVersioned() && !any {
		return fmt.Errorf("%s is versioned and names nothing of the version", what)
	}
	return nil
}

// FillVersion is a versioned action given a version: the action as
// declared, with what its command names of the version filled in. An
// action that is not versioned is returned as it is, and none is
// changed: what was declared is kept for the builds to come.
func FillVersion(action *pluginv1.Action, version Version) *pluginv1.Action {
	if !action.GetVersioned() {
		return action
	}
	pairs := make([]string, 0, 2*len(filled))
	for _, each := range filled {
		pairs = append(pairs, begins+each.name+"}", each.of(version))
	}
	fill := strings.NewReplacer(pairs...)
	out := proto.Clone(action).(*pluginv1.Action)
	for i, argument := range out.GetCommand().GetArguments() {
		out.Command.Arguments[i] = fill.Replace(argument)
	}
	for name, value := range out.GetCommand().GetEnvironment() {
		out.Command.Environment[name] = fill.Replace(value)
	}
	return out
}

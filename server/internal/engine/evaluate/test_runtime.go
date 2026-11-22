package evaluate

import (
	"fmt"
	"maps"
	"slices"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	"github.com/frontseat-dev/frontseat/server/internal/engine/graph"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"google.golang.org/protobuf/proto"
)

type testOwner struct {
	repository string
	entity     entity.Ref
}

// testRuntimes adds only data to inferred tests, after catalog ownership is
// known. The adapter's command, directory, selection, outputs and generated
// acceptance are retained. Every plugin and shard follows this same path.
func testRuntimes(out *Evaluation, configured map[string][]config.TestRuntime, defaults map[string]config.Defaults, pinned map[string]map[string]string) error {
	if len(configured) == 0 && len(defaults) == 0 {
		return nil
	}
	type prepared struct {
		config.TestRuntime
		reading catalogReading
	}
	settings := map[testOwner]prepared{}
	known := map[testOwner]bool{}
	for _, entity := range out.Entities {
		known[testOwner{entity.Repository, entity.Ref}] = true
	}
	for _, repository := range slices.Sorted(maps.Keys(configured)) {
		for _, written := range configured[repository] {
			key := testOwner{repository, written.Entity}
			if !known[key] {
				return &model.Mistake{File: written.File, Message: fmt.Sprintf("test runtime settings name %s, which the catalog has not discovered in this repository", written.Entity)}
			}
			if _, twice := settings[key]; twice {
				return &model.Mistake{File: written.File, Message: fmt.Sprintf("%s declares test runtime data twice", written.Entity)}
			}
			reading, err := readCatalogInputs(repository, written.Directory, written.File, "test", written.Inputs, written.Needs)
			if err != nil {
				return err
			}
			for _, tool := range written.Tools {
				if pinned[repository][tool] == "" {
					return &model.Mistake{File: written.File, Message: fmt.Sprintf("test.tools names %q, which this repository's mise.toml does not pin", tool)}
				}
			}
			settings[key] = prepared{written, reading}
		}
	}
	// What a repository's defaults give every test is pinned, as what a
	// Component gives its own is.
	for _, repository := range slices.Sorted(maps.Keys(defaults)) {
		for _, tool := range defaults[repository].Test.Tools {
			if pinned[repository][tool] == "" {
				return &model.Mistake{File: defaults[repository].File, Message: fmt.Sprintf("defaults.test.tools names %q, which this repository's mise.toml does not pin", tool)}
			}
		}
	}
	// Many atoms share one project. Resolve its owner once, not for every
	// shard; graph.Owner retains the normal nested ownership semantics.
	owners := map[string]testOwner{}
	for i := range out.Actions {
		action := &out.Actions[i]
		if !action.IsTest() || action.IsShared() || action.InStep != nil {
			continue
		}
		key, found := owners[action.Label.Path]
		if !found {
			owner, _ := graph.Owner(out.Entities, action.Label.Path)
			key = testOwner{owner.Repository, owner.Ref}
			owners[action.Label.Path] = key
		}
		setting, found := settings[key]
		repository := defaults[key.repository].Test
		if !found && len(repository.Tools) == 0 && len(repository.Environment) == 0 {
			continue
		}
		// A test is given the repository's defaults and its Component's
		// own: the tools of both, and the variables of both, its
		// Component's over the defaults'. A variable is said to be the
		// file's that wrote it. Where the repository says none, what the
		// Component says is given as it is, and nothing is copied.
		environment, tools := setting.Environment, setting.Tools
		file := func(string) string { return setting.File }
		if len(repository.Environment) > 0 {
			environment = maps.Clone(repository.Environment)
			maps.Copy(environment, setting.Environment)
			file = func(name string) string {
				if _, own := setting.Environment[name]; own {
					return setting.File
				}
				return defaults[key.repository].File
			}
		}
		if len(repository.Tools) > 0 {
			tools = slices.Clone(repository.Tools)
			for _, tool := range setting.Tools {
				if !slices.Contains(tools, tool) {
					tools = append(tools, tool)
				}
			}
		}
		if read := setting.reading.groups(); len(read) > 0 {
			action.Sources = append(slices.Clone(action.Sources), read...)
		}
		action.References = append(slices.Clone(action.References), setting.reading.references...)
		if len(tools) > 0 {
			// Cached plugin replies are immutable. Only the plan's copy is
			// extended; the next build must not inherit this configuration.
			action.Declared = proto.Clone(action.Declared).(*pluginv1.Action)
			for _, tool := range tools {
				if !slices.Contains(action.Declared.Tools, tool) {
					action.Declared.Tools = append(action.Declared.Tools, tool)
				}
			}
		}
		// A name the action's own command sets, or its fix's, cannot be
		// given as written as well: said here, whether or not the build
		// fixes, and of the file that wrote it.
		// Asked without sorting, since most are given none of them: the
		// first by name is the one said.
		twice := ""
		for name := range environment {
			_, command := action.Declared.GetCommand().GetEnvironment()[name]
			_, fix := action.Declared.GetFix().GetEnvironment()[name]
			if (command || fix) && (twice == "" || name < twice) {
				twice = name
			}
		}
		if twice != "" {
			return &model.Mistake{File: file(twice), Message: fmt.Sprintf("test.environment gives %s, which %s sets for itself", twice, action.Label)}
		}
		action.Environment = environment
	}
	return nil
}

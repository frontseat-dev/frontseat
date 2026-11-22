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
func testRuntimes(out *Evaluation, configured map[string][]config.TestRuntime, pinned map[string]map[string]string) error {
	if len(configured) == 0 {
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
			reading, err := readCatalogInputs(repository, written.Directory, written.File, "test.inputs", written.Inputs)
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
		if !found {
			continue
		}
		if read := setting.reading.groups(); len(read) > 0 {
			action.Sources = append(slices.Clone(action.Sources), read...)
		}
		action.References = append(slices.Clone(action.References), setting.reading.references...)
		if len(setting.Tools) > 0 {
			// Cached plugin replies are immutable. Only the plan's copy is
			// extended; the next build must not inherit this configuration.
			action.Declared = proto.Clone(action.Declared).(*pluginv1.Action)
			for _, tool := range setting.Tools {
				if !slices.Contains(action.Declared.Tools, tool) {
					action.Declared.Tools = append(action.Declared.Tools, tool)
				}
			}
		}
		// A name the action's own command sets cannot be given as written
		// as well: said here, and of the file that wrote it. A test has no
		// fix: only a check has one.
		// Asked without sorting, since most are given none of them: the
		// first by name is the one said.
		twice := ""
		for name := range setting.Environment {
			_, command := action.Declared.GetCommand().GetEnvironment()[name]
			if command && (twice == "" || name < twice) {
				twice = name
			}
		}
		if twice != "" {
			return &model.Mistake{File: setting.File, Message: fmt.Sprintf("test.environment gives %s, which %s sets for itself", twice, action.Label)}
		}
		action.Environment = setting.Environment
	}
	return nil
}

package plan

import (
	"maps"
	"slices"

	"github.com/frontseat-dev/frontseat/build/engine/pattern"
	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"google.golang.org/protobuf/proto"
)

// CloneActions gives a caller its own action values. Shared contexts remain
// shared only within the new values; nothing mutable reaches the originals.
func CloneActions(actions []Action) []Action {
	out := slices.Clone(actions)
	contexts := map[*pluginv1.Context]*pluginv1.Context{}
	for i := range out {
		cloneAction(&out[i], contexts)
	}
	return out
}

func cloneAction(a *Action, contexts map[*pluginv1.Context]*pluginv1.Context) {
	a.Declared = proto.CloneOf(a.Declared)
	if a.Context != nil {
		cloned, exists := contexts[a.Context]
		if !exists {
			cloned = proto.CloneOf(a.Context)
			contexts[a.Context] = cloned
		}
		a.Context = cloned
	}
	a.Sources = pattern.CloneGroups(a.Sources)
	a.References = slices.Clone(a.References)
	a.Reads = slices.Clone(a.Reads)
	a.IdleWithout = slices.Clone(a.IdleWithout)
	a.Needs = slices.Clone(a.Needs)
	a.ArtifactOnly = slices.Clone(a.ArtifactOnly)
	a.Artifacts = slices.Clone(a.Artifacts)
	for j := range a.Artifacts {
		v := &a.Artifacts[j]
		v.Source.Patterns = slices.Clone(v.Source.Patterns)
		v.Patterns = slices.Clone(v.Patterns)
		v.Generated = pattern.CloneSelections(v.Generated)
	}
	a.GeneratedSources = CloneGenerated(a.GeneratedSources)
	a.WorkspaceFiles = slices.Clone(a.WorkspaceFiles)
	a.Handed = slices.Clone(a.Handed)
	for j := range a.Handed {
		a.Handed[j].Artifacts = slices.Clone(a.Handed[j].Artifacts)
		a.Handed[j].Patterns = slices.Clone(a.Handed[j].Patterns)
		a.Handed[j].Generated = pattern.CloneSelections(a.Handed[j].Generated)
	}
	a.Environment = maps.Clone(a.Environment)
	a.Arranged = slices.Clone(a.Arranged)
	for j := range a.Arranged {
		a.Arranged[j].Content = slices.Clone(a.Arranged[j].Content)
	}
	if a.InStep != nil {
		v := *a.InStep
		v.Fixed = slices.Clone(v.Fixed)
		v.Entities = slices.Clone(v.Entities)
		for j := range v.Entities {
			v.Entities[j].Says = slices.Clone(v.Entities[j].Says)
			v.Entities[j].Should = slices.Clone(v.Entities[j].Should)
		}
		a.InStep = &v
	}
	if a.Beside != nil {
		v := *a.Beside
		v.Sandbox = slices.Clone(v.Sandbox)
		v.Environment = maps.Clone(v.Environment)
		v.TestNeeds = slices.Clone(v.TestNeeds)
		v.Programs = maps.Clone(v.Programs)
		for name, program := range v.Programs {
			cloneAction(&program, contexts)
			v.Programs[name] = program
		}
		a.Beside = &v
	}
}

// CloneGenerated copies generated authority, including its pattern lists.
func CloneGenerated(generated []GeneratedSource) []GeneratedSource {
	out := slices.Clone(generated)
	for i := range out {
		out[i].Patterns = slices.Clone(out[i].Patterns)
	}
	return out
}

package pluginhost_test

import (
	"github.com/frontseat-dev/frontseat/build/engine/pattern"
	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"testing"
)

func TestGeneratedNestedExclusionDoesNotLeakThroughSnapshotSibling(t *testing.T) {
	sets := map[string]*pluginv1.InputSet{
		"generated": {Inputs: []*pluginv1.Input{pluginhost.SourceInput("**/*.go")}, AcceptsGenerated: true},
		"wrapper":   {Inputs: []*pluginv1.Input{pluginhost.SetInput("generated"), pluginhost.SourceInput("!private.go")}},
		"snapshot":  {Inputs: []*pluginv1.Input{pluginhost.SourceInput("private.go")}},
	}
	inputs := []*pluginv1.Input{pluginhost.SetInput("wrapper"), pluginhost.SetInput("snapshot")}
	raw := map[string][]*pluginv1.Input{}
	for name, set := range sets {
		raw[name] = set.Inputs
	}
	final, err := pluginhost.ResolveInputs(inputs, raw)
	if err != nil {
		t.Fatal(err)
	}
	patterns, _ := pluginhost.SplitInputs(final)
	if !pattern.Select(patterns, "private.go") {
		t.Fatal("snapshot-only sibling control did not select private.go")
	}
	generated, err := pluginhost.GeneratedInputs(inputs, sets)
	if err != nil {
		t.Fatal(err)
	}
	allows := func(file string) bool {
		for _, selection := range generated {
			if selection.Accepts(file) {
				return true
			}
		}
		return false
	}
	if !allows("public.go") {
		t.Fatal("generated public.go allowed control was lost")
	}
	if allows("private.go") {
		t.Fatal("nested generated exclusion was erased by a snapshot-only sibling selection")
	}
}

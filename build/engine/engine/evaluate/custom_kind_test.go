package evaluate

import (
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/config"
)

// An entity's own action is of the one kind it says, as a plugin's is,
// and is named as it is written, whatever its kind: the kind is the
// contract's, and the name is the file's. Two kinds, and what only a
// check says written of another kind, are refused where they are
// written, by the configuration (config's TestAnActionIsOfOneKind).
func TestAnEntitysOwnActionIsOfTheKindItSays(t *testing.T) {
	own := func(change func(*config.Action)) config.Action {
		action := config.Action{Name: "own", File: "catalog-info.yaml", Command: []string{"tool"}, Outputs: []string{"out/own.tgz"}, Kind: config.KindBuild}
		change(&action)
		return action
	}
	for name, action := range map[string]config.Action{
		"a step of a build": own(func(*config.Action) {}),
		"a check": own(func(a *config.Action) {
			a.Kind, a.Fix, a.Rewrites = config.KindCheck, []string{"tool", "-w"}, []string{"*.go"}
		}),
		"a package": own(func(a *config.Action) { a.Kind = config.KindPackage }),
		"a program": own(func(a *config.Action) {
			a.Kind, a.Outputs, a.Command, a.Tools = config.KindProgram, nil, []string{"tool", "serve"}, []string{"tool"}
		}),
		"what publishes": own(func(a *config.Action) { a.Kind, a.Outputs = config.KindPublish, nil }),
	} {
		got, err := custom("lab", action)
		if err != nil {
			t.Errorf("%s: refused, %v", name, err)
			continue
		}
		declared := got.Declared
		is := map[string]bool{
			config.KindBuild:   declared.GetBuild() != nil,
			config.KindCheck:   declared.GetCheck().GetFix() != nil,
			config.KindPackage: declared.GetPackage() != nil,
			config.KindProgram: declared.GetProgram() != nil,
			config.KindPublish: declared.GetPublish() != nil,
		}
		if !is[action.Kind] {
			t.Errorf("%s: declared as %v, want of kind %s", name, declared.GetKind(), action.Kind)
		}
		if got.Label.Name != "own" || declared.GetName() != "own" {
			t.Errorf("%s: named %s (%s), want own, as it is written", name, got.Label.Name, declared.GetName())
		}
	}
}

package evaluate

import (
	"errors"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	"github.com/frontseat-dev/frontseat/server/internal/model"
)

// An entity's own action is of one kind, as a plugin's is: one written
// as two is refused, of the file that wrote it, and what only a check
// says is refused of any other. The control is each kind written alone.
func TestAnEntitysOwnActionIsOfOneKind(t *testing.T) {
	own := func(change func(*config.Action)) config.Action {
		action := config.Action{Name: "own", File: "catalog-info.yaml", Command: []string{"tool"}, Outputs: []string{"out/own.tgz"}}
		change(&action)
		return action
	}
	for name, c := range map[string]struct {
		action config.Action
		says   string
	}{
		"a package and a check":     {own(func(a *config.Action) { a.Package, a.Check = true, true }), "an action is of one kind"},
		"a program and a check":     {own(func(a *config.Action) { a.Program, a.Check, a.Outputs = true, true, nil }), "an action is of one kind"},
		"what publishes and checks": {own(func(a *config.Action) { a.Publish, a.Check, a.Outputs = true, true, nil }), "an action is of one kind"},
		"a fix of no check":         {own(func(a *config.Action) { a.Fix = []string{"tool", "-w"} }), "has a fix, and is not a check"},
		"rewrites of no check":      {own(func(a *config.Action) { a.Rewrites = []string{"*.go"} }), "says what it may change, and is not a check"},
	} {
		_, err := custom("lab", c.action)
		var mistake *model.Mistake
		if !errors.As(err, &mistake) || mistake.File != "catalog-info.yaml" || !strings.Contains(mistake.Message, c.says) {
			t.Errorf("%s: %v, want it refused of its file saying %q", name, err, c.says)
		}
	}
	for name, action := range map[string]config.Action{
		"a step of a build": own(func(*config.Action) {}),
		"a check":           own(func(a *config.Action) { a.Check, a.Fix, a.Rewrites = true, []string{"tool", "-w"}, []string{"*.go"} }),
		"a package":         own(func(a *config.Action) { a.Package = true }),
	} {
		if _, err := custom("lab", action); err != nil {
			t.Errorf("%s: refused, %v", name, err)
		}
	}
}

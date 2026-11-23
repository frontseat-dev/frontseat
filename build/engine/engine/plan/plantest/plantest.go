// Package plantest gives a test the planned actions it is about, each
// written by its name and what it needs, for the tests of the plan and
// of what schedules it.
package plantest

import (
	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/model"
)

// Action is an action of the project "p", by name, with what it needs.
func Action(name string, needs ...string) plan.Action {
	a := plan.Action{Label: model.Label{Path: "p", Plugin: "x", Name: name}}
	for _, need := range needs {
		a.Needs = append(a.Needs, model.Label{Path: "p", Plugin: "x", Name: need})
	}
	return a
}

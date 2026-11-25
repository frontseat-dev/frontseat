package testbuild

import (
	"context"
	"fmt"
	"slices"

	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
)

// Results are what a build says of its actions, each by its label.
type Results map[string]*buildv1.ActionResult

// ResultsOf reads what a build that has ended says of its actions.
func ResultsOf(ctx context.Context, build *frontseat.Build) (Results, error) {
	listed, err := frontseat.Collect(build.Actions(ctx))
	if err != nil {
		return nil, err
	}
	results := make(Results, len(listed))
	for _, each := range listed {
		results[each.GetLabel()] = each
	}
	return results, nil
}

// Executed are the actions a worker ran for the build, by label, in
// order: those whose result no cache answered.
func (r Results) Executed() []string {
	var executed []string
	for label, result := range r {
		if result.GetProvenance() == buildv1.Provenance_PROVENANCE_EXECUTED {
			executed = append(executed, label)
		}
	}
	slices.Sort(executed)
	return executed
}

// Rebuilt says, of each action a worker ran for the build, what another
// build that came before made of it, a line each, in order: none where
// the build ran nothing. An action is known between two builds by its
// digest on the grid. Run again under the digest the build before had,
// it is the same action and the grid did not answer from what it kept;
// under another, it is another action, whose command, inputs or
// platform differ, and both digests are said, to be compared on the
// grid. The build before prints an action as named says, where the two
// name their repositories otherwise; nil where they print alike.
func (r Results) Rebuilt(before Results, named func(label string) string) []string {
	var lines []string
	for _, label := range r.Executed() {
		was := label
		if named != nil {
			was = named(label)
		}
		here := r[label].GetActionDigest().GetHash()
		earlier, built := before[was]
		switch there := earlier.GetActionDigest().GetHash(); {
		case !built:
			lines = append(lines, fmt.Sprintf("%s was executed (%.12s): the build before had no action %s", label, here, was))
		case there == here:
			lines = append(lines, fmt.Sprintf("%s was executed again as the same action (%.12s): the grid did not answer from what it kept", label, here))
		default:
			lines = append(lines, fmt.Sprintf("%s was executed as another action than %s before: %.12s here, %.12s before: its command, inputs or platform differ", label, was, here, there))
		}
	}
	return lines
}

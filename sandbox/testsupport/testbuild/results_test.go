package testbuild

import (
	"slices"
	"testing"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
)

// result is what a build says of one action: where its result came
// from, and its digest on the grid.
func result(from buildv1.Provenance, digest string) *buildv1.ActionResult {
	return &buildv1.ActionResult{Provenance: from, ActionDigest: &buildv1.Digest{Hash: digest}}
}

// What a build ran that a build before it had built is said by what
// became of the action between the two: the same action run again, by
// its digest, another action under the same label, or one the build
// before never had. What a cache answered is said nothing of: that is
// the control, and a build that ran nothing says nothing.
func TestWhatABuildRanThatWasBuiltBeforeIsSaidByWhatBecameOfTheAction(t *testing.T) {
	const ran, kept = buildv1.Provenance_PROVENANCE_EXECUTED, buildv1.Provenance_PROVENANCE_GRID_CACHE
	before := Results{
		"//code/a:go.compile": result(ran, "aaaaaaaaaaaaaaaa"),
		"//code/a:go.lint":    result(ran, "bbbbbbbbbbbbbbbb"),
		"//code/a:go.format":  result(ran, "cccccccccccccccc"),
	}
	after := Results{
		"//a:go.compile": result(ran, "aaaaaaaaaaaaaaaa"),
		"//a:go.lint":    result(ran, "dddddddddddddddd"),
		"//a:go.format":  result(kept, "cccccccccccccccc"),
		"//a:go.test/.":  result(ran, "eeeeeeeeeeeeeeee"),
	}

	if got, want := after.Executed(), []string{"//a:go.compile", "//a:go.lint", "//a:go.test/."}; !slices.Equal(got, want) {
		t.Errorf("a worker is said to have run %v, want %v", got, want)
	}
	got := after.Rebuilt(before, func(label string) string { return "//code/" + label[len("//"):] })
	want := []string{
		"//a:go.compile was executed again as the same action (aaaaaaaaaaaa): the grid did not answer from what it kept",
		"//a:go.lint was executed as another action than //code/a:go.lint before: dddddddddddd here, bbbbbbbbbbbb before: its command, inputs or platform differ",
		"//a:go.test/. was executed (eeeeeeeeeeee): the build before had no action //code/a:go.test/.",
	}
	if !slices.Equal(got, want) {
		t.Errorf("of what was built again:\n%q\nwant\n%q", got, want)
	}
	// The control: what no worker ran is said nothing of, whatever the
	// build before made of it, and labels that are printed alike are
	// looked up as they are.
	if said := (Results{"//a:go.format": result(kept, "ffffffffffffffff")}).Rebuilt(before, nil); len(said) != 0 {
		t.Errorf("of a build that ran nothing: %q, want nothing said", said)
	}
	if said := before.Rebuilt(before, nil); len(said) != 3 {
		t.Errorf("of a build run again whole, %d lines (%q), want one for each of its 3 actions", len(said), said)
	}
}

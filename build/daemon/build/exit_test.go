package build

import (
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"testing"
)

// An action that never reached a grid ran no test, whatever kind it is.
func TestATestThatWasNeverRunIsABuildThatFailed(t *testing.T) {
	label := "//a:go.test"
	for name, c := range map[string]struct {
		provenance buildv1.Provenance
		want       buildv1.ExitCode
	}{
		"it ran":       {buildv1.Provenance_PROVENANCE_EXECUTED, exitTestsFailed},
		"it never ran": {buildv1.Provenance_PROVENANCE_UNSPECIFIED, exitBuildFailure},
	} {
		r := &run{tests: map[string]bool{label: true}, results: map[string]*buildv1.ActionResult{label: {Label: label, Outcome: buildv1.Outcome_OUTCOME_FAILED, Provenance: c.provenance}}}
		if got := r.exitCode(); got != c.want {
			t.Errorf("%s: %s, want %s", name, got, c.want)
		}
	}
}

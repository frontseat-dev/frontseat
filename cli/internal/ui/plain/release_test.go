package plain_test

import (
	"bytes"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/cli/internal/ui/plain"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
)

func unit(name string) *typev1.EntityRef {
	return &typev1.EntityRef{Kind: "component", Namespace: "default", Name: name}
}

func TestAPlanPrintsEachUnitAndWhyItMovesOrHolds(t *testing.T) {
	var out bytes.Buffer
	plain.Printer{Out: &out}.Plan(&buildv1.ReleasePlan{
		Etag: "94d84b5ff042",
		Units: []*buildv1.ReleaseUnit{
			{
				Entity: unit("greet"), Members: []*typev1.EntityRef{unit("greet")}, LastVersion: "0.77.0", NextVersion: "0.77.1", Tag: "greet/v0.77.1", Branch: "release/0.77",
				Reasons: []*buildv1.ReleaseReason{{Kind: buildv1.ReasonKind_REASON_KIND_OWN_CHANGE, Summary: "fix: trim the greeting", Commit: &buildv1.RepositoryCommit{Commit: "3f2a9c1d5e6f7a8b9c0d"}}},
			},
			{
				Entity: unit("log"), Members: []*typev1.EntityRef{unit("log")}, NextVersion: "0.77.0", Tag: "log/v0.77.0", Branch: "release/0.77",
				Reasons: []*buildv1.ReleaseReason{{Kind: buildv1.ReasonKind_REASON_KIND_FIRST_RELEASE}},
			},
			{
				Entity: &typev1.EntityRef{Kind: "system", Namespace: "default", Name: "kiosk"}, Members: []*typev1.EntityRef{unit("banner"), unit("ticker")}, LastVersion: "0.77.0",
				Reasons: []*buildv1.ReleaseReason{{Kind: buildv1.ReasonKind_REASON_KIND_UNCHANGED}},
			},
			{
				Entity: unit("hello"), Members: []*typev1.EntityRef{unit("hello")},
				Reasons: []*buildv1.ReleaseReason{{Kind: buildv1.ReasonKind_REASON_KIND_BRANCH, Summary: "main is the default branch, which releases nothing"}},
			},
			{
				Entity: unit("done"), Members: []*typev1.EntityRef{unit("done")}, LastVersion: "1.0.0", Tag: "done/v1.0.0",
				Reasons: []*buildv1.ReleaseReason{{Kind: buildv1.ReasonKind_REASON_KIND_RELEASED}},
			},
		},
	})
	want := `greet  0.77.0 -> 0.77.1  tag greet/v0.77.1  on release/0.77
  fix: trim the greeting  (3f2a9c1d5e6f)
log  unreleased -> 0.77.0  tag log/v0.77.0  on release/0.77
  its first release
system:kiosk  (banner, ticker)  holds at 0.77.0: nothing of it changed since its last release
hello  holds: main is the default branch, which releases nothing
done  released as 1.0.0  tag done/v1.0.0
plan 94d84b5ff042
`
	if out.String() != want {
		t.Errorf("a plan prints\n%s\nwant\n%s", out.String(), want)
	}
}

func TestAReleasePrintsWhatItTaggedAndHowItEnded(t *testing.T) {
	tagged := []*buildv1.ReleasedUnit{
		{Entity: unit("greet"), Version: "0.77.1", Tags: []*buildv1.ReleaseTag{{Repository: "services", Name: "greet/v0.77.1", Commit: "3f2a9c1d5e6f7a8b9c0d"}}},
		{Entity: unit("log"), Version: "0.77.0", Already: true, Tags: []*buildv1.ReleaseTag{{Repository: "services", Name: "log/v0.77.0", Commit: "3f2a9c1d5e6f7a8b9c0d"}}},
	}
	for name, c := range map[string]struct {
		release *buildv1.Release
		want    string
	}{
		"tagged and not published": {
			&buildv1.Release{Id: "rel_1", State: buildv1.Release_STATE_UNPUBLISHED, Units: tagged},
			"greet  0.77.1  tagged greet/v0.77.1 in services  (3f2a9c1d5e6f)\nlog  0.77.0  was tagged already log/v0.77.0 in services  (3f2a9c1d5e6f)\nrel_1  unpublished: its tags are in the workspace and are not pushed\n",
		},
		"failed": {
			&buildv1.Release{Id: "rel_2", State: buildv1.Release_STATE_FAILED, Failure: "the production build run_9 ended BUILD_FAILURE; nothing is tagged"},
			"rel_2  FAILED: the production build run_9 ended BUILD_FAILURE; nothing is tagged\n",
		},
		"nothing moved": {
			&buildv1.Release{Id: "rel_3", State: buildv1.Release_STATE_SUCCEEDED},
			"rel_3  succeeded\n",
		},
	} {
		var out bytes.Buffer
		plain.Printer{Out: &out}.Release(c.release)
		if out.String() != c.want {
			t.Errorf("%s prints\n%s\nwant\n%s", name, out.String(), c.want)
		}
	}
}

// What a daemon says is shown as text, never acted on: a commit's first
// line or a failure cannot clear the screen, or write a line that reads
// as this printer's.
func TestWhatADaemonSaysCannotBeTakenForALineOfThePrinters(t *testing.T) {
	var out bytes.Buffer
	p := plain.Printer{Out: &out}
	p.Plan(&buildv1.ReleasePlan{Etag: "e\nplan forged", Units: []*buildv1.ReleaseUnit{{
		Entity: unit("greet"), LastVersion: "1.0.0", NextVersion: "1.0.1", Tag: "greet/v1.0.1", Branch: "release/1.0",
		Reasons: []*buildv1.ReleaseReason{{Kind: buildv1.ReasonKind_REASON_KIND_OWN_CHANGE, Summary: "fix: x\x1b[2J\x1b]0;owned\x07\rrel_9  succeeded", Commit: &buildv1.RepositoryCommit{Commit: "abc"}}},
	}}})
	p.Release(&buildv1.Release{Id: "rel_1", State: buildv1.Release_STATE_FAILED, Failure: "boom\nrel_1  unpublished: all is well"})
	p.Event(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_ActionFinished_{ActionFinished: &buildv1.BuildEvent_ActionFinished{Result: &buildv1.ActionResult{Outcome: buildv1.Outcome_OUTCOME_FAILED, Label: "//a:go.test", Message: "bad\npassed  //a:go.test"}}}})
	printed := out.String()
	for _, hidden := range []string{"\x1b", "\x07", "\r"} {
		if strings.Contains(printed, hidden) {
			t.Errorf("what was printed holds %q", hidden)
		}
	}
	lines := strings.Split(strings.TrimSuffix(printed, "\n"), "\n")
	if len(lines) != 6 {
		t.Fatalf("it printed %d lines, want 6:\n%s", len(lines), printed)
	}
	for _, forged := range []string{"plan forged", "rel_9  succeeded", "rel_1  unpublished", "passed  //a:go.test"} {
		for _, line := range lines {
			if strings.HasPrefix(line, forged) {
				t.Errorf("a line of the daemon's reads as the printer's: %q", line)
			}
		}
	}
	if !strings.Contains(printed, `fix: x\x1b[2J`) || !strings.Contains(printed, `boom\x0arel_1`) {
		t.Errorf("what was hidden is not shown as what it is:\n%s", printed)
	}
}

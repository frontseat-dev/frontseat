package plain_test

import (
	"bytes"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/frontseat-dev/frontseat/cli/internal/ui/plain"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
)

// Whoever watches a run sees that it started over, and why: the lines
// above that one are of actions the run has forgotten.
func TestARunThatStartsOverSaysSo(t *testing.T) {
	var out bytes.Buffer
	p := plain.Printer{Out: &out}

	p.Event(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_StartedOver_{StartedOver: &buildv1.BuildEvent_StartedOver{
		Label:  "//hello:go.test",
		Reason: "the grid keeps nothing under that digest",
	}}})

	want := "starting over: the grid no longer held what //hello:go.test was handed (the grid keeps nothing under that digest)\n"
	if got := out.String(); got != want {
		t.Errorf("printed %q, want %q", got, want)
	}
}

func TestAFinishedRunPrintsItsFailureMessage(t *testing.T) {
	var out strings.Builder
	printer := plain.Printer{Out: &out}
	message := "platform/gateway/go.mod: the go.mod names no module. Add a module directive."
	printer.Event(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_Finished_{Finished: &buildv1.BuildEvent_Finished{Build: &buildv1.Build{
		ExitCode: &buildv1.ExitCode{Name: "BUILD_FAILURE", Code: 1}, Failure: message,
	}}}})
	if strings.Count(out.String(), message) != 1 {
		t.Fatalf("failure message was lost or repeated: %q", out.String())
	}
}

// What an action that failed was told, a person is told: its exit code
// says nothing of what it did not leave or what it could not be handed.
func TestAnActionThatFailedSaysWhy(t *testing.T) {
	var out strings.Builder
	printer := plain.Printer{Out: &out}

	printer.Event(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_ActionFinished_{ActionFinished: &buildv1.BuildEvent_ActionFinished{Result: &buildv1.ActionResult{Label: "//a:b", Outcome: buildv1.Outcome_OUTCOME_FAILED, Message: "//a:c and //a:d left different things at go/bin/e"}}}})
	printer.Event(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_ActionFinished_{ActionFinished: &buildv1.BuildEvent_ActionFinished{Result: &buildv1.ActionResult{Label: "//a:f", Outcome: buildv1.Outcome_OUTCOME_FAILED, ExitCode: 2}}}})

	if !strings.Contains(out.String(), "//a:c and //a:d left different things at go/bin/e\n") {
		t.Errorf("printed %q", out.String())
	}
	if strings.Count(out.String(), "\n") != 3 {
		t.Errorf("printed %q; want a line for each and one for what the first was told", out.String())
	}
}

// A check's findings and what it would change are printed beneath it,
// each where it is; a run asked to fix says what it wrote.
func TestACheckPrintsWhatItFoundAndWhatItWouldChange(t *testing.T) {
	var out bytes.Buffer
	p := plain.Printer{Out: &out}
	p.Event(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_ActionFinished_{ActionFinished: &buildv1.BuildEvent_ActionFinished{Result: &buildv1.ActionResult{
		Label: "//app:go.lint", Outcome: buildv1.Outcome_OUTCOME_FAILED, ExitCode: 1,
		Diagnostics: []*typev1.Diagnostic{{RuleId: "go/vet", Message: "Printf format %d has arg of wrong type", SourceLocation: &typev1.SourceLocation{Path: "app/main.go", Line: 6}}},
	}}}})
	p.Event(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_ActionFinished_{ActionFinished: &buildv1.BuildEvent_ActionFinished{Result: &buildv1.ActionResult{
		Label: "//app:catalog.depends-on", Outcome: buildv1.Outcome_OUTCOME_REWRITE, Message: "app/catalog-info.yaml says it depends on nothing; it should say it depends on component:default/api, which is what the source says: frontseat build --fix writes it",
	}}}})
	p.Event(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_ActionFinished_{ActionFinished: &buildv1.BuildEvent_ActionFinished{Result: &buildv1.ActionResult{
		Label: "//:custom.tidy", Outcome: buildv1.Outcome_OUTCOME_PASSED, Message: "wrote gone.txt",
		Rewrites: []*buildv1.Rewrite{{Location: &typev1.SourceLocation{Path: "gone.txt"}, Kind: buildv1.Rewrite_KIND_DELETE, Applied: true}},
	}}}})
	for _, want := range []string{
		"FAILED  //app:go.lint  exit 1",
		"        app/main.go:6  go/vet  Printf format %d has arg of wrong type",
		"REWRITE //app:catalog.depends-on",
		"        app/catalog-info.yaml says it depends on nothing;",
		"        deleted gone.txt",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("printed:\n%s\nwant a line holding %q", out.String(), want)
		}
	}
}

// An action that had nothing to do is said so, with why, and a run says
// how many had none: a test that was not run is seen not to have been.
func TestAnActionWithNothingToDoIsSaidAndCounted(t *testing.T) {
	var out strings.Builder
	printer := plain.Printer{Out: &out}
	printer.Event(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_ActionFinished_{ActionFinished: &buildv1.BuildEvent_ActionFinished{Result: &buildv1.ActionResult{
		Label: "//hello:go.test/tagged", Outcome: buildv1.Outcome_OUTCOME_IDLE, Message: "not run: //hello:go.test-build left no _tests/0/tagged.test, and it has nothing to do without it",
	}}}})
	printer.Event(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_Finished_{Finished: &buildv1.BuildEvent_Finished{Build: &buildv1.Build{
		ExitCode: &buildv1.ExitCode{Name: "SUCCESS"}, Counts: &buildv1.Counts{Total: 2, Passed: 1, Idle: 1},
	}}}})
	for _, said := range []string{"idle    //hello:go.test/tagged  not run: //hello:go.test-build left no _tests/0/tagged.test", "passed 1, failed 0, errored 0, skipped 0, idle 1"} {
		if !strings.Contains(out.String(), said) {
			t.Errorf("printed %q, which does not say %q", out.String(), said)
		}
	}
	// The control: a run with none says nothing of them.
	out.Reset()
	printer.Event(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_Finished_{Finished: &buildv1.BuildEvent_Finished{Build: &buildv1.Build{
		ExitCode: &buildv1.ExitCode{Name: "SUCCESS"}, Counts: &buildv1.Counts{Total: 1, Passed: 1},
	}}}})
	if strings.Contains(out.String(), "idle") {
		t.Errorf("a run with nothing idle printed %q", out.String())
	}
}

// Every line of a run is text a daemon gave as it is shown, not as a
// terminal would act on it: an action's label, why a run started over,
// where a fix wrote and the run's id are each written as the bytes they
// are, as its messages are.
func TestNothingOfARunsLinesIsActedOnByATerminal(t *testing.T) {
	var out bytes.Buffer
	p := plain.Printer{Out: &out}
	label := "//a:go.test\x1b[2J\rpassed  //b:go.test"
	for _, outcome := range []buildv1.Outcome{buildv1.Outcome_OUTCOME_PASSED, buildv1.Outcome_OUTCOME_FAILED, buildv1.Outcome_OUTCOME_IDLE, buildv1.Outcome_OUTCOME_REWRITE, buildv1.Outcome_OUTCOME_ERROR} {
		p.Event(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_ActionFinished_{ActionFinished: &buildv1.BuildEvent_ActionFinished{Result: &buildv1.ActionResult{
			Outcome: outcome, Label: label,
			Rewrites: []*buildv1.Rewrite{{Location: &typev1.SourceLocation{Path: "a.go\x1b]0;owned\x07\nwrote b.go"}}},
		}}}})
	}
	p.Event(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_StartedOver_{StartedOver: &buildv1.BuildEvent_StartedOver{Label: label, Reason: "gone\x1b[1A\nrun_9  EXIT_SUCCESS"}}})
	p.Event(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_Finished_{Finished: &buildv1.BuildEvent_Finished{Build: &buildv1.Build{Id: "run_1\x1b[2K\rrun_2", ExitCode: &buildv1.ExitCode{Name: "SUCCESS\x1b[2K\rrun_3"}}}}})
	printed := out.String()
	for _, hidden := range []string{"\x1b", "\x07", "\r"} {
		if strings.Contains(printed, hidden) {
			t.Errorf("what was printed holds %q:\n%q", hidden, printed)
		}
	}
	for _, line := range strings.Split(printed, "\n") {
		for _, forged := range []string{"passed  //b", "wrote b.go", "run_9", "run_2", "run_3"} {
			if strings.HasPrefix(strings.TrimSpace(line), forged) {
				t.Errorf("a line of the daemon's reads as the printer's: %q", line)
			}
		}
	}
	// The control: what is shown is still there to read, as the bytes it is.
	if !strings.Contains(printed, `//a:go.test\x1b[2J\x0dpassed`) {
		t.Errorf("the label is not shown as what it is:\n%s", printed)
	}
}

// A value of an enum is said in words, without its enum's name; one the
// contract does not name is said by its number.
func TestAnEnumsValueIsSaidInWords(t *testing.T) {
	for want, value := range map[string]protoreflect.Enum{
		"ready":       workspacev1.WorkspaceStatus_STATE_READY,
		"own change":  buildv1.ReasonKind_REASON_KIND_OWN_CHANGE,
		"grid cache":  buildv1.Provenance_PROVENANCE_GRID_CACHE,
		"unpublished": buildv1.Release_STATE_UNPUBLISHED,
		"99":          buildv1.Outcome(99),
	} {
		if got := plain.Word(value); got != want {
			t.Errorf("%v is said %q, want %q", value, got, want)
		}
	}
}

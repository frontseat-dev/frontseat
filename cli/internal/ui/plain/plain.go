package plain

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

// shown is text a daemon gave, as a line may hold it: what moves a
// terminal's cursor, clears its screen or begins a line of its own is
// written as the bytes it is, so that nothing a commit, an action or a
// daemon says can be taken for a line this printer wrote.
func shown(text string) string {
	if strings.IndexFunc(text, hidden) < 0 {
		return text
	}
	var out strings.Builder
	for _, r := range text {
		if hidden(r) {
			fmt.Fprintf(&out, "\\x%02x", r)
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// hidden is a character a terminal acts on and does not show.
func hidden(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == utf8.RuneError
}

// Printer writes a build's events as lines.
type Printer struct {
	// Out is where the lines go.
	Out io.Writer
}

// Event writes one event.
func (p Printer) Event(e *buildv1.BuildEvent) {
	switch event := e.GetEvent().(type) {
	case *buildv1.BuildEvent_Planned_:
		fmt.Fprintf(p.Out, "%d actions\n", len(event.Planned.GetActions()))
	case *buildv1.BuildEvent_ActionFinished_:
		p.action(event.ActionFinished.GetResult())
	case *buildv1.BuildEvent_StartedOver_:
		fmt.Fprintf(p.Out, "starting over: the grid no longer held what %s was handed (%s)\n", shown(event.StartedOver.GetLabel()), shown(event.StartedOver.GetReason()))
	case *buildv1.BuildEvent_Finished_:
		p.finished(event.Finished.GetBuild())
	}
}

func (p Printer) action(result *buildv1.ActionResult) {
	took := result.GetDuration().AsDuration().Round(time.Millisecond)
	switch result.GetOutcome() {
	case buildv1.Outcome_OUTCOME_PASSED:
		// One the grid answered from what it kept is said to be: it did
		// not run for this build.
		cached := ""
		if result.GetProvenance() == buildv1.Provenance_PROVENANCE_GRID_CACHE {
			cached = "  cached"
		}
		fmt.Fprintf(p.Out, "passed  %s  %s%s\n", shown(result.GetLabel()), took, cached)
	case buildv1.Outcome_OUTCOME_FAILED:
		fmt.Fprintf(p.Out, "FAILED  %s  exit %d  %s\n", shown(result.GetLabel()), result.GetExitCode(), took)
		// One that failed for what it did not leave or what it was handed
		// says so: its exit code does not.
		if result.GetMessage() != "" {
			fmt.Fprintf(p.Out, "        %s\n", shown(result.GetMessage()))
		}
	case buildv1.Outcome_OUTCOME_IDLE:
		// Not run, having nothing to do: said, so that a test that did
		// not run is seen not to have.
		fmt.Fprintf(p.Out, "idle    %s  %s\n", shown(result.GetLabel()), shown(result.GetMessage()))
	case buildv1.Outcome_OUTCOME_SKIPPED:
		// Not run, for what it needed did not pass: what stopped it is
		// said, and it is no error of its own.
		fmt.Fprintf(p.Out, "skipped %s  %s\n", shown(result.GetLabel()), shown(result.GetMessage()))
	case buildv1.Outcome_OUTCOME_REWRITE:
		fmt.Fprintf(p.Out, "REWRITE %s  %s\n", shown(result.GetLabel()), took)
		fmt.Fprintf(p.Out, "        %s\n", shown(result.GetMessage()))
	default:
		fmt.Fprintf(p.Out, "ERROR   %s  %s\n", shown(result.GetLabel()), shown(result.GetMessage()))
	}
	// What a check found, each where it found it; and what it changed,
	// where the run was asked to fix.
	p.found(result.GetDiagnostics())
	for _, change := range result.GetRewrites() {
		did := map[bool]string{true: "wrote", false: "would write"}[change.GetApplied()]
		switch change.GetKind() {
		case buildv1.Rewrite_KIND_DELETE:
			did = map[bool]string{true: "deleted", false: "would delete"}[change.GetApplied()]
		case buildv1.Rewrite_KIND_CREATE:
			did = map[bool]string{true: "created", false: "would create"}[change.GetApplied()]
		}
		fmt.Fprintf(p.Out, "        %s %s\n", did, shown(change.GetLocation().GetPath()))
	}
}

func (p Printer) finished(build *buildv1.Build) {
	c := build.GetCounts()
	// How many had nothing to do is said where any had.
	idle := ""
	if c.GetIdle() > 0 {
		idle = fmt.Sprintf(", idle %d", c.GetIdle())
	}
	fmt.Fprintf(p.Out, "%s  %s  passed %d, failed %d, errored %d, skipped %d%s  in %s\n",
		shown(build.GetId()), shown(build.GetExitCode().GetName()), c.GetPassed(), c.GetFailed(), c.GetErrored(), c.GetSkipped(), idle,
		build.GetCost().GetWallTime().AsDuration().Round(time.Millisecond))
	if build.GetFailure() != "" {
		fmt.Fprintln(p.Out, shown(build.GetFailure()))
	}
	p.found(build.GetDiagnostics())
}

// found writes what was found, each where it was found.
// Printed writes what an action that failed printed, beneath the line
// that says it failed: every line, as the action printed it, set in as
// what an action is told is, so that none is taken for a line of this
// printer's own.
func (p Printer) Printed(lines []*typev1.LogLine) {
	for _, line := range lines {
		fmt.Fprintf(p.Out, "        %s\n", shown(line.GetText()))
	}
}

// Unread says, in the place of what a failed action printed, that it
// could not be read, and why.
func (p Printer) Unread(err error) {
	fmt.Fprintf(p.Out, "        what it printed could not be read: %s\n", shown(err.Error()))
}

func (p Printer) found(diagnostics []*typev1.Diagnostic) {
	for _, found := range diagnostics {
		at := ""
		if location := found.GetSourceLocation(); location != nil {
			at = location.GetPath()
			if location.GetLine() > 0 {
				at = fmt.Sprintf("%s:%d", at, location.GetLine())
			}
			at += "  "
		}
		fmt.Fprintf(p.Out, "        %s%s  %s\n", shown(at), shown(found.GetRuleId()), shown(found.GetMessage()))
	}
}

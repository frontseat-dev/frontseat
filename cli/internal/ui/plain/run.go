package plain

import (
	"fmt"
	"strings"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

// Line writes a line a program printed, as it printed it, with
// what a terminal would act on written as the bytes it is.
func (p Printer) Line(line *typev1.LogLine) {
	fmt.Fprintln(p.Out, p.Indent+shown(line.GetText()))
}

// Run writes one run as a line of a list: its id, where it is,
// and what was run.
func (p Printer) Run(run *buildv1.Run) {
	what := run.GetTarget()
	if len(run.GetArguments()) > 0 {
		what += " -- " + strings.Join(run.GetArguments(), " ")
	}
	fmt.Fprintf(p.Out, "%s  %s  %s\n", shown(run.GetId()), RunState(run), shown(what))
}

// RunState says where a run is, in a few words.
func RunState(run *buildv1.Run) string {
	switch run.GetState() {
	case buildv1.Run_STATE_BUILDING, buildv1.Run_STATE_RUNNING:
		return Word(run.GetState())
	case buildv1.Run_STATE_EXITED:
		return fmt.Sprintf("exited %d", run.GetExitCode())
	case buildv1.Run_STATE_CANCELLED:
		// What could not be done of ending it is said.
		if run.GetFailure() != "" {
			return "cancelled: " + shown(run.GetFailure())
		}
		return "cancelled"
	case buildv1.Run_STATE_FAILED:
		return "failed: " + shown(run.GetFailure())
	}
	return "unknown"
}

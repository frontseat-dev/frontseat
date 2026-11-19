package schedule

import (
	"context"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

type endingRunner struct {
	cancel context.CancelFunc
	err    error
}

func (r endingRunner) Run(context.Context, plan.Action, []Handed) Ending {
	if r.cancel != nil {
		r.cancel()
	}
	return Ending{Outcome: Errored, Err: r.err}
}

func (endingRunner) Skip(plan.Action, model.Label, Outcome) {}

// An error may cancel its scheduling attempt so the run can retry. The cause
// remains an error even though the tasks it interrupted were cancelled.
func TestTheErrorThatCancelsAnAttemptKeepsItsOwnResult(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cancel bool
		err    error
		result string
		status codes.Code
	}{
		{"ordinary error", false, exec.ErrNotKept, "error", codes.Error},
		{"missing content causes retry", true, exec.ErrNotKept, "error", codes.Error},
		{"cancelled action", true, context.Canceled, "cancellation", codes.Unset},
		{"action deadline", false, context.DeadlineExceeded, "timeout", codes.Error},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel, parent, spans := executing(t)
			runner := endingRunner{err: tc.err}
			if tc.cancel {
				runner.cancel = cancel
			}
			actions := []plan.Action{action("compile")}
			All(ctx, actions, 1, runner)
			got := checkTaskLinks(t, spans.Ended(), actions, parent.SpanContext())
			span := got[actions[0].Label.String()]
			if result := taskAttribute(span, semconv.CICDPipelineTaskRunResultKey); result != tc.result || span.Status().Code != tc.status {
				t.Fatalf("error %v: result=%q status=%v; want result=%q status=%v", tc.err, result, span.Status(), tc.result, tc.status)
			}
			if tc.status == codes.Error && span.Status().Description != tc.err.Error() {
				t.Errorf("error detail = %q, want %q", span.Status().Description, tc.err.Error())
			}
		})
	}
}

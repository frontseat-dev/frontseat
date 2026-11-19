package run

import (
	"fmt"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// inStep ends the host's action over an entity's catalog file, which the
// daemon does itself: its result says so, and no worker's time is
// counted for it. A file that does not say what the source says is out
// of step: the action ends as a rewrite, which fails the run, and says
// what the file should say.
// A run asked to fix writes it.
func (runner Runner) inStep(events Events, snapshot *source.Snapshot, action plan.Action) buildv1.Outcome {
	started := time.Now()
	step := action.InStep
	shown := path.Join(action.Repository, step.File)
	result := &buildv1.ActionResult{
		Label:      action.Label.String(),
		Entity:     events.owner(action),
		Name:       action.Name(),
		Provenance: buildv1.Provenance_PROVENANCE_DAEMON,
		StartTime:  timestamppb.New(started),
		Outcome:    buildv1.Outcome_OUTCOME_PASSED,
	}
	// What the file says and should say of each entity that is out of
	// step. A file of one entity says "it"; one of several names each.
	var says, wrote []string
	for _, each := range step.Entities {
		if slices.Equal(each.Says, each.Should) {
			continue
		}
		subject := "it"
		if len(step.Entities) > 1 {
			subject = each.Entity.String()
		}
		relation := "depends on"
		if each.Inverse {
			relation = "is a dependency of"
		}
		says = append(says, fmt.Sprintf("%s says %s %s %s; it should say it %s %s", shown, subject, relation, listed(each.Says), relation, listed(each.Should)))
		wrote = append(wrote, fmt.Sprintf("%s %s %s", subject, relation, listed(each.Should)))
	}
	switch {
	case step.Fixed == nil:
	case !runner.Fix:
		result.Outcome = buildv1.Outcome_OUTCOME_REWRITE
		result.Message = strings.Join(says, "; ") + ", which is what the source says: frontseat build --fix writes it"
	default:
		// What is written was worked out from what the run read of the
		// file, and is written over that and nothing else.
		_, _, executable, err := snapshot.File(filepath.Join(snapshot.Root, filepath.FromSlash(snapshot.Paths[action.Repository]), filepath.FromSlash(step.File)))
		if err == nil {
			err = snapshot.Apply(action.Repository, source.Change{Path: step.File, Kind: source.Replace, Content: step.Fixed, From: step.From, Executable: executable})
		}
		if err != nil {
			result.Outcome, result.Message = buildv1.Outcome_OUTCOME_ERROR, err.Error()
			break
		}
		result.Message = fmt.Sprintf("wrote %s: %s", shown, strings.Join(wrote, "; "))
		events.Patched(action.Repository, []string{step.File})
	}
	result.Duration = durationpb.New(time.Since(started))
	events.Finished(result)
	return result.GetOutcome()
}

// listed is components as a sentence names them.
func listed(components []entity.Ref) string {
	if len(components) == 0 {
		return "no component"
	}
	written := make([]string, 0, len(components))
	for _, component := range components {
		written = append(written, component.String())
	}
	return strings.Join(written, ", ")
}

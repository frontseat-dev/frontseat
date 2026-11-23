package run

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/schedule"
	"github.com/frontseat-dev/frontseat/build/engine/model"
)

// generated is what an action is handed of what the actions it needs
// left: what it reads of them (selected), and each artifact binding's
// declared files, placed alongside the consumer's snapshot inputs. A
// producer that is needed for its generated source alone hands nothing
// else. A binding is found in all its producer left, whatever of that
// the action also named: naming one output of a producer hides none of
// its generated source.
func (runner Runner) generated(ctx context.Context, tree source.Tree, action plan.Action, handed []schedule.Handed) ([]schedule.Handed, error) {
	read := selected(tree, action, handed)
	provided := 0
	for _, h := range action.Handed {
		provided += len(h.Artifacts)
	}
	if len(action.Artifacts) == 0 && provided == 0 {
		return read, nil
	}
	byProducer := make(map[model.Label]map[string]exec.Held, len(handed))
	for _, each := range handed {
		byProducer[each.From] = each.Left
	}
	out := make([]schedule.Handed, 0, len(read)+len(action.Artifacts)+provided)
	for _, each := range read {
		if !slices.Contains(action.ArtifactOnly, each.From) {
			out = append(out, each)
		}
	}
	for _, h := range action.Handed {
		for _, artifact := range h.Artifacts {
			from := path.Join(tree.Paths[artifact.Repository], artifact.ProjectRoot, strings.TrimSuffix(artifact.Output, "/"))
			held, found := byProducer[artifact.From][from]
			if !found {
				if artifact.Optional {
					continue
				}
				return nil, fmt.Errorf("%s left no provided output %s", artifact.From, artifact.Output)
			}
			into := path.Join(tree.Paths[action.Repository], action.ProjectRoot, h.Into, strings.TrimSuffix(artifact.Output, "/"))
			// Keep bindings separate: together and the REAPI input tree check
			// collisions; no map assignment may silently pick one producer.
			out = append(out, schedule.Handed{From: artifact.From, Left: map[string]exec.Held{into: held}})
		}
	}
	for _, binding := range action.Artifacts {
		claim := binding.Source
		from := path.Join(tree.Paths[claim.Repository], claim.ProjectRoot, strings.TrimSuffix(claim.Output, "/"))
		held, found := byProducer[claim.Producer][from]
		if !found {
			return nil, fmt.Errorf("%s left no declared generated output %s", claim.Producer, claim.Output)
		}
		files, err := runner.Answers.List(ctx, held)
		if err != nil {
			return nil, fmt.Errorf("generated output of %s: %w", claim.Producer, err)
		}
		placed := make(map[string]exec.Held, len(files))
		for file, content := range files {
			if !claim.AllowsOutput(file) {
				return nil, fmt.Errorf("%s produced %s outside its generated source declaration", claim.Producer, file)
			}
			// Where the claim is beneath the root it is bound at, its
			// files are there in what the consumer reads.
			at := file
			if binding.Within != "" {
				at = path.Join(binding.Within, file)
			}
			if binding.Accepts(at) {
				placed[path.Join(tree.Paths[action.Repository], action.ProjectRoot, binding.Into, at)] = content
			}
		}
		out = append(out, schedule.Handed{From: claim.Producer, Left: placed})
	}
	return out, nil
}

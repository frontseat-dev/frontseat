package run

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/exec/fix"
)

// A check is an action that checks the source and writes nothing: on
// the grid it fails on what it finds, by its exit code or by its
// findings. A run asked to fix runs the check's fix first, where the
// checkout is (fix), and applies each change over the content the run
// read, through the snapshot, which refuses a file that is no longer
// that; the check then runs on the grid over what was written. Two
// actions of a run whose fixes may change one file are refused together
// before either runs, so that neither writes what the other was not
// worked out from.

// proposal is a change a check proposed: as the host applies it, and as
// the run records it.
type proposal struct {
	change   source.Change
	recorded *buildv1.Rewrite
}

// kinds are the changes as the run records them.
var kinds = map[source.Kind]buildv1.Rewrite_Kind{
	source.Replace: buildv1.Rewrite_KIND_REPLACE,
	source.Create:  buildv1.Rewrite_KIND_CREATE,
	source.Delete:  buildv1.Rewrite_KIND_DELETE,
}

// record records what a fix changed, as the run records it: the content
// of each replacement or creation kept with the run, by digest.
func record(events Events, action plan.Action, changes []source.Change) []proposal {
	rule := action.Label.Plugin + "/" + action.Name()
	proposals := make([]proposal, 0, len(changes))
	for _, change := range changes {
		recorded := &buildv1.Rewrite{
			Location:   &typev1.SourceLocation{Repository: action.Repository, Path: change.Path},
			Kind:       kinds[change.Kind],
			Executable: change.Executable,
			RuleId:     rule,
		}
		if change.Kind != source.Delete {
			recorded.Content = events.Print(change.Content)
		}
		proposals = append(proposals, proposal{change: change, recorded: recorded})
	}
	return proposals
}

// conflicting returns, for a run that fixes, what is wrong with each
// action whose tool may change a file another action's may: both are
// refused, so that neither writes over what the other was worked out
// from. The map is by the action's label.
func conflicting(ctx context.Context, snapshot *source.Snapshot, needed []plan.Action) (map[string]string, error) {
	changers := map[string][]string{}
	for _, action := range needed {
		changeable, err := fix.Changeable(ctx, snapshot, action)
		if err != nil {
			return nil, err
		}
		for _, file := range changeable {
			changers[file] = append(changers[file], action.Label.String())
		}
	}
	refused := map[string][]string{}
	for at, labels := range changers {
		if len(labels) < 2 {
			continue
		}
		sort.Strings(labels)
		for _, label := range labels {
			others := make([]string, 0, len(labels)-1)
			for _, other := range labels {
				if other != label {
					others = append(others, other)
				}
			}
			refused[label] = append(refused[label], fmt.Sprintf("%s, which %s may change too", at, strings.Join(others, " and ")))
		}
	}
	out := make(map[string]string, len(refused))
	for label, reasons := range refused {
		sort.Strings(reasons)
		out[label] = "not run: it may change " + strings.Join(reasons, "; ") + ": one action changes a file in a run that fixes"
	}
	return out, nil
}

// apply writes what a check proposed, each change over what the run
// read, and says which files it wrote. It stops at the first change the
// snapshot refuses, and says why: what was written before it stays
// written, and is said to be.
func apply(snapshot *source.Snapshot, action plan.Action, proposals []proposal) (wrote []string, err error) {
	for _, each := range proposals {
		if err := snapshot.Apply(action.Repository, each.change); err != nil {
			return wrote, err
		}
		each.recorded.Applied = true
		wrote = append(wrote, each.change.Path)
	}
	return wrote, nil
}

// changed lists what a check would change, as its result says it.
func changed(action plan.Action, proposals []proposal) string {
	names := make([]string, 0, len(proposals))
	for _, each := range proposals {
		names = append(names, path.Join(action.Repository, each.change.Path))
	}
	return strings.Join(names, ", ")
}

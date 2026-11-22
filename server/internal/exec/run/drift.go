package run

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
)

// drift compares what a producer generated with the copies the checkout
// holds of it: each file the worker produced within the declared
// patterns against the file at its place in the checkout, and each file
// the checkout holds within them against what was produced. A copy that
// differs, one that is missing and one the generator no longer produces
// are each a change that puts the checkout in step. A checkout that
// holds no copy at all keeps none: its generated source lives on the
// workers alone, and nothing is written into it. The worker's bytes are
// brought only where a copy is to be written; a copy that is the same
// is known so by its digest. Nothing is asked of what keeps the
// checkout's history.
func (runner Runner) drift(ctx context.Context, snapshot *source.Snapshot, action plan.Action, left map[string]exec.Held) ([]source.Change, error) {
	repository := snapshot.Paths[action.Repository]
	var changes []source.Change
	// What is brought to be compared is its own to delete: a directory
	// made here, for one claim of this action, which nothing else names.
	// It is gone however this ends.
	var made []string
	defer func() {
		for _, dir := range made {
			_ = os.RemoveAll(dir)
		}
	}()
	for _, claim := range action.GeneratedSources {
		if claim.Producer != action.Label || claim.Repository != action.Repository {
			continue
		}
		committed, err := snapshot.Within(ctx, action.Repository, claim.Directory, claim.Patterns)
		if err != nil {
			return nil, err
		}
		if len(committed) == 0 {
			continue
		}
		at := path.Join(repository, action.ProjectRoot, strings.TrimSuffix(claim.Output, "/"))
		held, there := left[at]
		if !there || !held.Directory {
			return nil, fmt.Errorf("%s left no directory at %s, which it says it generates", action.Label, claim.Output)
		}
		produced, err := runner.Answers.List(ctx, held)
		if err != nil {
			return nil, err
		}
		var brought string
		bring := func(file string) ([]byte, error) {
			if brought == "" {
				if err := os.MkdirAll(runner.Kept, 0o755); err != nil {
					return nil, err
				}
				dir, err := os.MkdirTemp(runner.Kept, "drift-")
				if err != nil {
					return nil, err
				}
				made = append(made, dir)
				brought = filepath.Join(dir, "produced")
				if err := runner.Answers.Materialize(ctx, held, brought); err != nil {
					return nil, err
				}
			}
			return os.ReadFile(filepath.Join(brought, filepath.FromSlash(file)))
		}
		seen := map[string]bool{}
		for file, each := range produced {
			if each.Directory || !claim.AllowsOutput(file) {
				continue
			}
			target := path.Join(claim.Directory, file)
			seen[target] = true
			was, _, executable, err := snapshot.File(filepath.Join(snapshot.Root, filepath.FromSlash(repository), filepath.FromSlash(target)))
			switch {
			case err == nil && was == each.Digest.Hash && executable == each.Executable:
				continue
			case err == nil:
				content, err := bring(file)
				if err != nil {
					return nil, err
				}
				changes = append(changes, source.Change{Path: target, Kind: source.Replace, Content: content, Executable: each.Executable, From: was})
			default:
				content, err := bring(file)
				if err != nil {
					return nil, err
				}
				changes = append(changes, source.Change{Path: target, Kind: source.Create, Content: content, Executable: each.Executable})
			}
		}
		// What the checkout holds within the patterns and the generator
		// did not produce is stale, and goes. It is the producer's to
		// delete because the claim is its own: beneath its project, or,
		// outside it, where the Component there says in its catalog file
		// that the producer's Component generates it (evaluation refuses
		// any other claim before anything runs).
		for _, file := range committed {
			if seen[file] {
				continue
			}
			was, _, _, err := snapshot.File(filepath.Join(snapshot.Root, filepath.FromSlash(repository), filepath.FromSlash(file)))
			if err != nil {
				return nil, err
			}
			changes = append(changes, source.Change{Path: file, Kind: source.Delete, From: was})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

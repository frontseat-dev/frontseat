// Package fix runs a check's fix where the checkout is. A check runs on
// the grid and only reports; its fix, gofmt -w where the check is
// gofmt -l, never runs there. A run asked to fix runs it here, in the
// daemon's own sandbox, which is what confines it: over a copy of the
// check's inputs, with the same tool the check runs with on the grid,
// brought from the grid by its digest, and with the environment the
// check ran with, as mise said it on the grid. What the fix changed of the
// files it may change is then a change over what the run read, for the
// snapshot to apply or refuse; the copy is thrown away. Nothing of the
// fix is an action of the grid, and nothing it does is kept.
package fix

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	execport "github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/here"
	"github.com/frontseat-dev/frontseat/server/internal/exec/tools"
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
	"github.com/frontseat-dev/frontseat/server/telemetry"
)

// Runner runs fixes for one run.
type Runner struct {
	// Grid brings a tool's tree here.
	Grid execport.Materializing
	// Tools is the run's provisioner: what it was answered for each tool
	// is what the fix runs with.
	Tools *tools.Provisioner
	// Kept is the directory tool trees are kept in here, by digest, for
	// as long as the daemon runs.
	Kept string
}

// Run runs an action's fix over a copy of its inputs and returns what it
// changed of the files it may change: each a change over the file as
// the run read it. A fix that ends with a non-zero exit code is an
// error, with what it printed.
func (r Runner) Run(ctx context.Context, snapshot *source.Snapshot, action plan.Action) (changes []source.Change, err error) {
	ctx, span := telemetry.Start(ctx, "fix")
	defer func() { telemetry.End(span, err) }()
	command := action.Declared.GetCheck().GetFix()
	if len(command.GetArguments()) == 0 {
		return nil, fmt.Errorf("%s has no fix", action.Label)
	}
	repository := snapshot.Paths[action.Repository]
	given, err := snapshot.Select(ctx, action.Repository, action.Sources)
	if err != nil {
		return nil, err
	}
	// The copy is kept where the daemon keeps a fix's work, which the
	// daemon after it clears: nothing is left in the system's temporary
	// directory by a daemon that died here.
	if err := os.MkdirAll(r.Kept, 0o755); err != nil {
		return nil, err
	}
	copied, err := os.MkdirTemp(r.Kept, "fix-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(copied)
	root := filepath.Join(copied, "work")
	// The inputs, as the run read them. What a generated source claims
	// is not the check's to see, here as on the grid.
	for _, file := range given {
		if !action.AcceptsSnapshot(action.Repository, file) {
			continue
		}
		if err := r.copy(ctx, snapshot, action.Repository, repository, file, root); err != nil {
			return nil, err
		}
	}
	// The files above the project its tool reads for the workspace, and
	// the files arranged for the check, as the check had them.
	for _, file := range action.WorkspaceFiles {
		if err := r.copy(ctx, snapshot, action.Repository, repository, file, root); err != nil {
			return nil, err
		}
	}
	for _, file := range action.Arranged {
		at := filepath.Join(root, filepath.FromSlash(repository), filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
			return nil, err
		}
		perm := os.FileMode(0o644)
		if file.Executable {
			perm = 0o755
		}
		if err := os.WriteFile(at, file.Content, perm); err != nil {
			return nil, err
		}
	}
	// The fix runs with what its check ran with, and with the tools
	// the check ran with, brought here. What the action is given as
	// written (plan.Action.Environment) its fix is given too, held to
	// what its check is held to: never under a name mise, the check's
	// command or the fix's own already gives.
	env, program, err := here.Runner{Grid: r.Grid, Tools: r.Tools, Kept: r.Kept}.Ready(ctx, snapshot, here.Asked{
		What: "the fix of " + action.Label.String(), Label: action.Label.String(), Repository: repository, Tools: action.Declared.GetTools(),
		Variables: action.Declared.GetCommand().GetEnvironment(), Command: command, Given: action.Environment,
		Root: root, Scratch: copied,
	})
	if err != nil {
		return nil, err
	}
	run := exec.CommandContext(ctx, program, command.GetArguments()[1:]...)
	run.Dir = filepath.Join(root, filepath.FromSlash(repository), filepath.FromSlash(action.ProjectRoot))
	// The project is there for the fix even where the check reads
	// nothing of it: what the fix makes there is judged like the rest.
	if err := os.MkdirAll(run.Dir, 0o755); err != nil {
		return nil, err
	}
	run.Env = env
	if out, err := run.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("the fix of %s: %w\n%s", action.Label, err, strings.TrimSpace(string(out)))
	}
	return r.changed(ctx, snapshot, action, root, given)
}

// copy writes a file of the snapshot into the copy, as the run read it.
func (r Runner) copy(ctx context.Context, snapshot *source.Snapshot, name, repository, file, root string) error {
	content, found, err := snapshot.Read(ctx, name, file)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	_, _, executable, err := snapshot.File(filepath.Join(snapshot.Root, filepath.FromSlash(repository), filepath.FromSlash(file)))
	if err != nil {
		return err
	}
	at := filepath.Join(root, filepath.FromSlash(repository), filepath.FromSlash(file))
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		return err
	}
	perm := fs.FileMode(0o644)
	if executable {
		perm = 0o755
	}
	return os.WriteFile(at, content, perm)
}

// changed compares the files the fix may change, in the copy, with what
// the run read: a difference in content or in whether a file may be run
// is a replacement, a file gone a deletion, a file new a creation.
func (r Runner) changed(ctx context.Context, snapshot *source.Snapshot, action plan.Action, root string, given []string) ([]source.Change, error) {
	repository := snapshot.Paths[action.Repository]
	project := filepath.Join(root, filepath.FromSlash(repository), filepath.FromSlash(action.ProjectRoot))
	may := func(file string) bool {
		return pattern.Within(action.ProjectRoot, file, action.Rewrites()) && action.AcceptsSnapshot(action.Repository, file)
	}
	// A file that is new is a creation where the check's inputs would
	// have selected it: a fix creates within what its check reads.
	given_ := func(file string) bool {
		return slices.ContainsFunc(action.Sources, func(group pattern.Group) bool { return group.Accepts(file) })
	}
	var changes []source.Change
	// What was arranged for the check is the host's, and no change of
	// the fix's.
	arranged := map[string]bool{}
	for _, file := range action.Arranged {
		arranged[path.Clean(file.Path)] = true
	}
	wasThere := map[string]bool{}
	for _, file := range given {
		if !may(file) {
			continue
		}
		wasThere[file] = true
		was, _, executable, err := snapshot.File(filepath.Join(snapshot.Root, filepath.FromSlash(repository), filepath.FromSlash(file)))
		if err != nil {
			return nil, err
		}
		at := filepath.Join(root, filepath.FromSlash(repository), filepath.FromSlash(file))
		info, err := os.Lstat(at)
		switch {
		case os.IsNotExist(err):
			changes = append(changes, source.Change{Path: file, Kind: source.Delete, From: was})
			continue
		case err != nil:
			return nil, err
		case !info.Mode().IsRegular():
			return nil, fmt.Errorf("the fix of %s left %s as something other than a file", action.Label, file)
		}
		content, err := os.ReadFile(at)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(content)
		now := info.Mode()&0o100 != 0
		if hex.EncodeToString(sum[:]) != was || now != executable {
			changes = append(changes, source.Change{Path: file, Kind: source.Replace, Content: content, Executable: now, From: was})
		}
	}
	// What is new beneath the project and may be changed is created.
	err := filepath.WalkDir(project, func(at string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if at != project && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(project, at)
		if err != nil {
			return err
		}
		file := path.Join(action.ProjectRoot, filepath.ToSlash(rel))
		if wasThere[file] || arranged[file] || !may(file) || !given_(file) {
			return nil
		}
		content, err := os.ReadFile(at)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		changes = append(changes, source.Change{Path: file, Kind: source.Create, Content: content, Executable: info.Mode()&0o100 != 0})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(changes, func(a, b source.Change) int { return strings.Compare(a.Path, b.Path) })
	return changes, nil
}

// Changeable returns the files an action's fix may change: its inputs
// that its rewrite patterns select, sorted. It is what two actions of a
// run must not share.
func Changeable(ctx context.Context, snapshot *source.Snapshot, action plan.Action) ([]string, error) {
	if len(action.Rewrites()) == 0 || action.Declared.GetCheck().GetFix() == nil || !action.IsCheck() {
		return nil, nil
	}
	given, err := snapshot.Select(ctx, action.Repository, action.Sources)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, file := range given {
		if pattern.Within(action.ProjectRoot, file, action.Rewrites()) && action.AcceptsSnapshot(action.Repository, file) {
			out = append(out, path.Join(action.Repository, file))
		}
	}
	return out, nil
}

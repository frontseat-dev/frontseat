package populate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/frontseat-dev/frontseat/server/internal/source/record"
	"github.com/frontseat-dev/frontseat/server/internal/source/vcs"
)

// unfinished is where a clone under way is said, beneath the directory
// the workspace keeps its own record in, which no repository may be
// named: one empty file for each, by the repository's name, made once
// its directory is and removed once the repository is recorded or the
// clone has failed.
const unfinished = ".frontseat/cloning"

// Clone clones a repository into a workspace's directory and returns it
// as the manifest records it. Roots make it a partial checkout: only
// those directories are there. A clone that fails leaves nothing where
// it was to be, and one asked into a place that is taken touches
// nothing. One that is never let fail or finish, in a daemon that ends,
// leaves its directory said to be unfinished, for Settle.
func Clone(ctx context.Context, system vcs.System, workspace string, c record.Checkout) (cloned record.Repository, err error) {
	if err := c.Within(); err != nil {
		return record.Repository{}, err
	}
	dir := filepath.Join(workspace, c.Name)
	// The directory is made here, by a call that makes none where
	// anything is: of two clones asked into one place, one is refused it.
	if err := os.Mkdir(dir, 0o755); errors.Is(err, fs.ErrExist) {
		return record.Repository{}, fmt.Errorf("populate: %s is in the workspace already", c.Name)
	} else if err != nil {
		return record.Repository{}, fmt.Errorf("populate: %w", err)
	}
	said := filepath.Join(workspace, filepath.FromSlash(unfinished), c.Name)
	defer func() {
		if err != nil {
			// Its own to delete: the directory this call made, which no
			// other clone was given, and what says it is unfinished.
			_ = os.RemoveAll(dir)
			_ = os.Remove(said)
			// And the directories that hold what says it, where they are
			// empty: removing one loses nothing.
			_ = os.Remove(filepath.Dir(said))
			_ = os.Remove(filepath.Dir(filepath.Dir(said)))
		}
	}()
	if err := os.MkdirAll(filepath.Dir(said), 0o755); err != nil {
		return record.Repository{}, fmt.Errorf("populate: %w", err)
	}
	if err := os.WriteFile(said, nil, 0o644); err != nil {
		return record.Repository{}, fmt.Errorf("populate: %w", err)
	}
	commit, err := system.Clone(ctx, dir, vcs.Source{Remote: c.Remote, Ref: c.Ref, Roots: c.Roots})
	if err != nil {
		return record.Repository{}, fmt.Errorf("populate: %w", stopped(ctx, err))
	}
	return record.Repository{Name: c.Name, Remote: c.Remote, Ref: c.Ref, Commit: commit, Roots: c.Roots}, nil
}

// Settle clears what clones left unfinished in a workspace, given the
// repositories its manifest records: a clone whose repository is
// recorded is finished, and one whose repository is not was stopped
// midway, by a daemon that ended, and its directory is removed, so that
// the repository is cloned anew when it is asked for. It is called
// where nothing is being cloned in the workspace: when it is opened, and
// once a pass has recorded what it brought. Whatever runs in the
// workspace can write beneath unfinished: a name there is held to be a
// repository's directory before the workspace's directory is joined to
// it, and one that is none names nothing to remove; one it wrote names a
// directory of the workspace it could remove itself.
func Settle(workspace string, recorded []record.Repository) error {
	at := filepath.Join(workspace, filepath.FromSlash(unfinished))
	entries, err := os.ReadDir(at)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("populate: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		finished := slices.ContainsFunc(recorded, func(r record.Repository) bool { return r.Name == name })
		if record.Directory(name) == nil && !finished {
			// Its own to delete: Clone made this directory, by a call that
			// makes none where anything is, before it said it unfinished,
			// and the manifest never recorded it whole.
			if err := os.RemoveAll(filepath.Join(workspace, name)); err != nil {
				return fmt.Errorf("populate: %w", err)
			}
		}
		// Its own to delete: beneath the directory the workspace keeps
		// its own record in, where Clone says a clone is unfinished.
		if err := os.RemoveAll(filepath.Join(at, name)); err != nil {
			return fmt.Errorf("populate: %w", err)
		}
	}
	return nil
}

// stopped is what ended a call its version control system failed: the
// context's own error where the context had ended, which stops what the
// system runs, else the system's failure.
func stopped(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// Widen adds directories to a partial checkout a workspace already holds,
// and returns the repository as the manifest records it now. No
// directories asks for all of it, and the checkout is then partial no
// longer. A repository that was cloned whole already has them. What they
// hold is fetched from the remote the manifest records, which a workspace
// holds to the one it is asked for from before it widens.
func Widen(ctx context.Context, system vcs.System, workspace string, held record.Repository, roots []string) (record.Repository, error) {
	if len(held.Roots) == 0 {
		return held, nil
	}
	if len(roots) == 0 {
		if err := system.Widen(ctx, filepath.Join(workspace, held.Name), held.Remote, nil); err != nil {
			return record.Repository{}, fmt.Errorf("populate: %w", stopped(ctx, err))
		}
		held.Roots = nil
		return held, nil
	}
	var more []string
	for _, root := range roots {
		if err := record.Beneath(held.Name, "the directory", root); err != nil {
			return record.Repository{}, err
		}
		if !slices.Contains(held.Roots, root) {
			more = append(more, root)
		}
	}
	if len(more) == 0 {
		return held, nil
	}
	if err := system.Widen(ctx, filepath.Join(workspace, held.Name), held.Remote, more); err != nil {
		return record.Repository{}, fmt.Errorf("populate: %w", stopped(ctx, err))
	}
	held.Roots = append(slices.Clone(held.Roots), more...)
	sort.Strings(held.Roots)
	return held, nil
}

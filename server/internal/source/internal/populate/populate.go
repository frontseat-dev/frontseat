package populate

import "github.com/frontseat-dev/frontseat/server/internal/source/record"

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/frontseat-dev/frontseat/server/internal/source/vcs"
)

// Clone clones a repository into a workspace's directory and returns it
// as the manifest records it. Roots make it a partial checkout: only
// those directories are there. A clone that fails leaves nothing where
// it was to be, and one asked into a place that is taken touches
// nothing.
func Clone(ctx context.Context, system vcs.System, workspace string, c record.Checkout) (cloned record.Repository, err error) {
	if err := c.Within(); err != nil {
		return record.Repository{}, err
	}
	dir := filepath.Join(workspace, c.Name)
	if _, taken := os.Lstat(dir); taken == nil {
		return record.Repository{}, fmt.Errorf("populate: %s is in the workspace already", c.Name)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	commit, err := system.Clone(ctx, dir, vcs.Source{Remote: c.Remote, Ref: c.Ref, Roots: c.Roots})
	if err != nil {
		return record.Repository{}, fmt.Errorf("populate: %w", err)
	}
	return record.Repository{Name: c.Name, Remote: c.Remote, Ref: c.Ref, Commit: commit, Roots: c.Roots}, nil
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
			return record.Repository{}, fmt.Errorf("populate: %w", err)
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
		return record.Repository{}, fmt.Errorf("populate: %w", err)
	}
	held.Roots = append(slices.Clone(held.Roots), more...)
	sort.Strings(held.Roots)
	return held, nil
}

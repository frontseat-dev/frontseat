package reapi

import (
	"context"
	"fmt"
	"path"
	"path/filepath"

	"github.com/bazelbuild/remote-apis-sdks/go/pkg/digest"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/access"
)

var _ exec.Listing = (*Grid)(nil)

// List implements exec.Listing without downloading any file bytes. Only
// validated CAS directory metadata becomes paths in a consumer input root.
func (g *Grid) List(ctx context.Context, directory exec.Held) (map[string]exec.Held, error) {
	if !directory.Directory {
		return nil, fmt.Errorf("generated output is not a directory")
	}
	ctx = access.Watch(ctx)
	files, err := list(ctx, g.directoryStore(), digest.Digest{Hash: directory.Digest.Hash, Size: directory.Digest.Size})
	return files, g.said(ctx, executionError(err))
}

// A bounded traversal prevents an untrusted directory DAG from expanding to
// unbounded paths. Repeated content at different paths remains distinct.
// What is generated holds no link: one is refused.
func list(ctx context.Context, store directoriesOf, root digest.Digest) (map[string]exec.Held, error) {
	walked, err := walk(ctx, store, root, 100000, "generated output", false)
	if err != nil {
		return nil, err
	}
	return walked.files, nil
}

// tree walks a tree the grid holds as list does, and returns its files,
// its links, each by its path to what it leads to, and its directories.
// A link is one within the tree: relative, and leading nowhere above
// the tree's root. A tool's tree has such links; what else a grid holds
// is listed without them.
func tree(ctx context.Context, store directoriesOf, root digest.Digest) (files map[string]exec.Held, links map[string]string, dirs []string, err error) {
	walked, err := walk(ctx, store, root, 200000, "the tree", true)
	if err != nil {
		return nil, nil, nil, err
	}
	return walked.files, walked.links, walked.dirs, nil
}

// walked is what a walk found: files and links by their paths, and the
// directories below the root.
type walked struct {
	files map[string]exec.Held
	links map[string]string
	dirs  []string
}

// walk reads a tree the grid holds, depth first, to at most 128
// directories deep and limit directories, files and links in all. A
// link is taken where links says so, and only one that stays within the
// tree; elsewhere a link is refused. What names the tree in an error.
func walk(ctx context.Context, store directoriesOf, root digest.Digest, limit int, what string, links bool) (walked, error) {
	out := walked{files: map[string]exec.Held{}, links: map[string]string{}}
	nodes := 0
	exceeded := func() error { return fmt.Errorf("%s exceeds the directory traversal limit", what) }
	var visit func(digest.Digest, string, int) error
	visit = func(at digest.Digest, prefix string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		nodes++
		if depth > 128 || nodes+len(out.files)+len(out.links) > limit {
			return exceeded()
		}
		directory, err := store.read(ctx, at)
		if err != nil {
			return err
		}
		if err := named(directory); err != nil {
			return err
		}
		if prefix != "" {
			out.dirs = append(out.dirs, prefix)
		}
		if !links && len(directory.GetSymlinks()) != 0 {
			return fmt.Errorf("%s contains a symbolic link at %s", what, prefix)
		}
		for _, file := range directory.GetFiles() {
			if nodes+len(out.files)+len(out.links) >= limit {
				return exceeded()
			}
			blob, err := digest.NewFromProto(file.GetDigest())
			if err != nil {
				return err
			}
			out.files[path.Join(prefix, file.GetName())] = exec.Held{Digest: exec.Digest{Hash: blob.Hash, Size: blob.Size}, Executable: file.GetIsExecutable()}
		}
		for _, link := range directory.GetSymlinks() {
			to := link.GetTarget()
			within := path.Join(prefix, path.Dir(link.GetName()), to)
			if to == "" || path.IsAbs(to) || !filepath.IsLocal(filepath.FromSlash(within)) {
				return fmt.Errorf("%s holds a link at %s that leads out of it, to %s", what, path.Join(prefix, link.GetName()), to)
			}
			out.links[path.Join(prefix, link.GetName())] = to
		}
		for _, child := range directory.GetDirectories() {
			below, err := digest.NewFromProto(child.GetDigest())
			if err != nil {
				return err
			}
			if err := visit(below, path.Join(prefix, child.GetName()), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(root, "", 0); err != nil {
		return walked{}, err
	}
	return out, nil
}

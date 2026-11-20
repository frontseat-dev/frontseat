package reapi

import (
	"context"
	"fmt"
	"path"
	"sort"

	"github.com/bazelbuild/remote-apis-sdks/go/pkg/digest"
	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/access"
)

var _ exec.Merging = (*Grid)(nil)

// Merge implements exec.Merging: directories the grid holds as one, as
// Pants's MergeDigests does. What is in more than one must be the same.
func (g *Grid) Merge(ctx context.Context, all []exec.Held) (_ exec.Held, err error) {
	ctx = access.Watch(ctx)
	defer func() { err = g.said(ctx, err) }()
	sources := make([]source, 0, len(all))
	for i, each := range all {
		if !each.Directory {
			other := 0
			if i == 0 {
				other = 1
			}
			return exec.Held{}, &exec.Clash{First: min(i, other), Second: max(i, other)}
		}
		sources = append(sources, source{at: digest.Digest{Hash: each.Digest.Hash, Size: each.Digest.Size}, index: i})
	}
	merged, err := merge(ctx, g.directoryStore(), sources, "")
	if err != nil {
		return exec.Held{}, executionError(err)
	}
	return exec.Held{Digest: exec.Digest{Hash: merged.Hash, Size: merged.Size}, Directory: true}, nil
}

// directoryStore is the shared validated-content metadata access for input
// composition; missing content retains the retry signal used by execution.
func (g *Grid) directoryStore() directoriesOf {
	return directoriesOf{
		read: func(ctx context.Context, at digest.Digest) (*repb.Directory, error) {
			held := &repb.Directory{}
			if _, err := g.client.ReadProto(ctx, at, held); err != nil {
				if status.Code(err) == codes.NotFound {
					return nil, fmt.Errorf("%s: %w", at, exec.ErrNotKept)
				}
				return nil, err
			}
			return held, nil
		},
		write: func(ctx context.Context, directory *repb.Directory) (digest.Digest, error) {
			return g.client.WriteProto(ctx, directory)
		},
	}
}

// directoriesOf is where merge reads and writes directories.
type directoriesOf struct {
	read  func(context.Context, digest.Digest) (*repb.Directory, error)
	write func(context.Context, *repb.Directory) (digest.Digest, error)
}

// source is a directory to merge, and which of those asked for it came
// from.
type source struct {
	at    digest.Digest
	index int
}

// entry is what a name is in a merged directory, and which source said
// so first.
type entry struct {
	file     *repb.FileNode
	link     *repb.SymlinkNode
	children []source
	from     int
}

// merge returns the directory that holds what all the sources do,
// reading each once and writing one directory for each that differs.
// What it writes holds names, digests and whether a file may be run and
// nothing else, sorted, so that it is the same directory in whatever
// order the sources come. A name that is a different thing in two of
// them is a clash, at where it is.
func merge(ctx context.Context, store directoriesOf, sources []source, at string) (digest.Digest, error) {
	seen := map[digest.Digest]bool{}
	distinct := sources[:0:0]
	for _, each := range sources {
		if !seen[each.at] {
			seen[each.at] = true
			distinct = append(distinct, each)
		}
	}
	if len(distinct) == 1 {
		return distinct[0].at, nil
	}
	entries := map[string]*entry{}
	clash := func(name string, first, second int) error {
		return &exec.Clash{Path: path.Join(at, name), First: min(first, second), Second: max(first, second)}
	}
	for _, each := range distinct {
		directory, err := store.read(ctx, each.at)
		if err != nil {
			return digest.Digest{}, err
		}
		// A directory is what a worker said, and is held to being one
		// before anything of it is passed on.
		if err := named(directory); err != nil {
			return digest.Digest{}, fmt.Errorf("the directory %s is not one to merge: %w", each.at, err)
		}
		for _, file := range directory.GetFiles() {
			there, taken := entries[file.GetName()]
			switch {
			case !taken:
				entries[file.GetName()] = &entry{file: &repb.FileNode{Name: file.GetName(), Digest: file.GetDigest(), IsExecutable: file.GetIsExecutable()}, from: each.index}
			case there.file == nil || !proto.Equal(there.file.GetDigest(), file.GetDigest()) || there.file.GetIsExecutable() != file.GetIsExecutable():
				return digest.Digest{}, clash(file.GetName(), there.from, each.index)
			}
		}
		for _, link := range directory.GetSymlinks() {
			there, taken := entries[link.GetName()]
			switch {
			case !taken:
				entries[link.GetName()] = &entry{link: &repb.SymlinkNode{Name: link.GetName(), Target: link.GetTarget()}, from: each.index}
			case there.link == nil || there.link.GetTarget() != link.GetTarget():
				return digest.Digest{}, clash(link.GetName(), there.from, each.index)
			}
		}
		for _, child := range directory.GetDirectories() {
			beneath, err := digest.NewFromProto(child.GetDigest())
			if err != nil {
				return digest.Digest{}, err
			}
			there, taken := entries[child.GetName()]
			switch {
			case !taken:
				entries[child.GetName()] = &entry{children: []source{{at: beneath, index: each.index}}, from: each.index}
			case there.children == nil:
				return digest.Digest{}, clash(child.GetName(), there.from, each.index)
			default:
				there.children = append(there.children, source{at: beneath, index: each.index})
			}
		}
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	out := &repb.Directory{}
	for _, name := range names {
		there := entries[name]
		switch {
		case there.file != nil:
			out.Files = append(out.Files, there.file)
		case there.link != nil:
			out.Symlinks = append(out.Symlinks, there.link)
		default:
			merged, err := merge(ctx, store, there.children, path.Join(at, name))
			if err != nil {
				return digest.Digest{}, err
			}
			out.Directories = append(out.Directories, &repb.DirectoryNode{Name: name, Digest: merged.ToProto()})
		}
	}
	return store.write(ctx, out)
}

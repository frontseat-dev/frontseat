// Package directory is a directory as a build reads it: the repositories
// it holds and the entities found in them, as the workspace's manifest in
// it says (.frontseat/workspace.yaml, which whoever fills the directory
// writes), or the one repository the directory itself is, where the
// build was given a checkout. It reads, and brings nothing: a build needs
// a directory, and not whoever filled it.
//
// The manifest is read as it is now, each time it is asked of: a
// repository is recorded there once it is whole, so what a build is given
// is what is there to build. Whatever runs in the directory can write the
// manifest; it is read as strictly as its writer reads it, and a place
// outside the directory is refused.
package directory

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/frontseat-dev/frontseat/common/record"
	"github.com/frontseat-dev/frontseat/common/record/manifest"
	"github.com/frontseat-dev/frontseat/common/vcs"
)

// System is what a directory's repositories are read with: their history,
// and what a revision written in full is.
type System interface {
	vcs.History
	Revision(name string) error
}

// Directory is a directory a build reads.
type Directory struct {
	root string
	// alone is whether the directory is itself one repository, of no
	// name, which the build was given: a job's checkout.
	alone  bool
	system System

	mu sync.Mutex
	// read is the manifest as it was last read, and looked how it looked
	// then: it is read again only where it looks otherwise.
	read   manifest.Manifest
	looked os.FileInfo
}

// Open returns the directory at root, read with a version control system.
// Alone, it is one repository the build was given.
func Open(root string, alone bool, system System) (*Directory, error) {
	if system == nil {
		return nil, errors.New("the directory was given no version control system to read its repositories with")
	}
	return &Directory{root: root, alone: alone, system: system}, nil
}

// Root is the directory.
func (d *Directory) Root() string { return d.root }

// Alone is whether the directory is itself one repository the build was
// given, which holds no other.
func (d *Directory) Alone() bool { return d.alone }

// holds returns the manifest as it is now; none where the directory has
// none, or is one repository alone.
func (d *Directory) holds() (manifest.Manifest, error) {
	if d.alone {
		return manifest.Manifest{}, nil
	}
	info, err := os.Stat(filepath.Join(d.root, filepath.FromSlash(manifest.File)))
	if errors.Is(err, fs.ErrNotExist) {
		return manifest.Manifest{}, nil
	}
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("the workspace's manifest: %w", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	// It is written whole and put in place by a rename, so one that looks
	// as it did, the same file of the disk, is the one that was read.
	if d.looked != nil && os.SameFile(d.looked, info) && d.looked.ModTime().Equal(info.ModTime()) && d.looked.Size() == info.Size() {
		return d.read, nil
	}
	read, _, err := manifest.Read(d.root, d.system.Revision)
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("the workspace's manifest: %w", err)
	}
	d.read, d.looked = read, info
	return read, nil
}

// Repositories returns the repositories the directory holds, each whole
// as its manifest records it; none for one repository alone, which has
// no name.
func (d *Directory) Repositories() ([]record.Repository, error) {
	held, err := d.holds()
	return held.Repositories, err
}

// Entities returns the entities the directory knows, each with where it
// lives.
func (d *Directory) Entities() []record.Located {
	held, err := d.holds()
	if err != nil {
		return nil
	}
	out := make([]record.Located, 0, len(held.Known))
	for _, known := range held.Known {
		out = append(out, known.Located())
	}
	return out
}

// repository returns the directory one of its repositories is in, and
// where it was brought from as the manifest records it. A repository is
// named as the directory holds it, and no other directory is asked of.
func (d *Directory) repository(name string) (directory, remote string, err error) {
	// A checkout the build was given is one repository, of no name, in
	// the directory itself.
	if d.alone {
		if name != "" {
			return "", "", fmt.Errorf("the workspace is a checkout the daemon was given, and holds no repository %q", name)
		}
		return d.root, "", nil
	}
	held, err := d.holds()
	if err != nil {
		return "", "", err
	}
	for _, each := range held.Repositories {
		if each.Name == name {
			return filepath.Join(d.root, each.Name), each.Remote, nil
		}
	}
	return "", "", fmt.Errorf("the workspace holds no repository %q", name)
}

// Standing says where one of the directory's repositories stands.
func (d *Directory) Standing(ctx context.Context, repository string) (vcs.Standing, error) {
	directory, _, err := d.repository(repository)
	if err != nil {
		return vcs.Standing{}, err
	}
	standing, err := d.system.Standing(ctx, directory)
	// A checkout the build was given says itself where its tags are
	// pushed: one that says nowhere a tag can be pushed is not planned
	// from, so that nothing is built and tagged that cannot be sent.
	if err == nil && d.alone {
		_, err = d.system.Origin(ctx, directory)
	}
	return standing, err
}

// Tags returns the tags of one of the directory's repositories.
func (d *Directory) Tags(ctx context.Context, repository string) ([]vcs.Tag, error) {
	directory, _, err := d.repository(repository)
	if err != nil {
		return nil, err
	}
	return d.system.Tags(ctx, directory)
}

// Changes returns what changed directories of one of the directory's
// repositories since a revision.
func (d *Directory) Changes(ctx context.Context, repository, since string, directories []string) ([]vcs.Change, error) {
	directory, _, err := d.repository(repository)
	if err != nil {
		return nil, err
	}
	return d.system.Changes(ctx, directory, since, directories)
}

// Uncommitted returns the files of one of the directory's repositories
// that are not as its revision has them.
func (d *Directory) Uncommitted(ctx context.Context, repository string) ([]string, error) {
	directory, _, err := d.repository(repository)
	if err != nil {
		return nil, err
	}
	return d.system.Uncommitted(ctx, directory)
}

// Ignored returns what is in one of the directory's repositories that its
// version control system is told to ignore, and the directory the
// repository is in.
func (d *Directory) Ignored(ctx context.Context, repository string) (paths []string, directory string, err error) {
	directory, _, err = d.repository(repository)
	if err != nil {
		return nil, "", err
	}
	paths, err = d.system.Ignored(ctx, directory)
	return paths, directory, err
}

// Tag names a revision of one of the directory's repositories by a tag.
func (d *Directory) Tag(ctx context.Context, repository, name, revision string) error {
	directory, _, err := d.repository(repository)
	if err != nil {
		return err
	}
	return d.system.Tag(ctx, directory, name, revision)
}

// PushTag sends a tag of one of the directory's repositories to where
// that repository was brought from, and the tag alone.
func (d *Directory) PushTag(ctx context.Context, repository, name string) error {
	directory, _, err := d.repository(repository)
	if err != nil {
		return err
	}
	remote, err := d.Remote(ctx, repository)
	if err != nil {
		return err
	}
	return d.system.PushTag(ctx, directory, remote, name)
}

// Remote is where one of the directory's repositories was brought from,
// as its version control system writes one.
func (d *Directory) Remote(ctx context.Context, repository string) (string, error) {
	directory, remote, err := d.repository(repository)
	if err != nil {
		return "", err
	}
	// A checkout the build was given was brought by another, and says
	// from where itself.
	if d.alone {
		return d.system.Origin(ctx, directory)
	}
	return remote, nil
}

// TagName holds a name to one the directory's version control system
// takes for a tag.
func (d *Directory) TagName(ctx context.Context, name string) error {
	return d.system.TagName(ctx, name)
}

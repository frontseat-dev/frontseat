// Package directory is a directory as a build reads it: the repositories
// it holds and the entities found in them, as the workspace's manifest in
// it says (.frontseat/workspace.yaml, which whoever fills the directory
// writes), or the one repository the directory itself is, where the
// build was given a checkout. It reads, and brings nothing: a build needs
// a directory, and not whoever filled it.
//
// A repository something else mounted in the directory is held as any
// is, by its record: where a commit is asked, it says the changeset it
// stands at. It has no history, and its version control system is asked
// nothing of it (ErrMounted).
//
// The manifest is read as it is now, each time it is asked of. It says
// where what it records is not whole, being filled or left by a fill
// that failed, and a directory then holds nothing to build: a repository
// is widened in place, and is recorded before and after. Whatever runs in the directory can write the
// manifest; it is read as strictly as its writer reads it, and a place
// outside the directory is refused.
package directory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

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

// read returns the manifest as it is now; none where the directory has
// none, or is one repository alone. It is read each time: whatever runs
// in the directory can write it, so nothing of how the file looks says
// that it is the one read before.
func (d *Directory) read() (manifest.Manifest, error) {
	if d.alone {
		return manifest.Manifest{}, nil
	}
	read, _, err := manifest.Read(d.root, d.system.Revision)
	if err != nil {
		return manifest.Manifest{}, unread{err}
	}
	return read, nil
}

// unread is a manifest that was not read. It says that and no more:
// whoever asked for a build is told it, and why holds what the manifest
// said, which whatever runs in the directory wrote. Why is kept for
// whoever asks the error what it is (errors.Is, errors.As).
type unread struct{ why error }

func (unread) Error() string {
	return "the workspace's manifest (" + manifest.File + ") is not one a build reads: whatever fills the workspace writes it, and it holds something else"
}

func (u unread) Unwrap() error { return u.why }

// ErrNotWhole is a directory whose manifest says that what it records is
// not whole: it is being filled, or its last fill failed.
var ErrNotWhole = errors.New("the workspace is not whole")

// Held is what a directory holds at one moment: its repositories, each
// whole as its manifest records it, and the entities it knows, each with
// where it lives. Both are of one reading of the manifest, so that no
// entity is said to live in a repository the same answer does not hold.
type Held struct {
	Repositories []record.Repository
	Entities     []record.Located
}

// Holds returns what the directory holds now; nothing for one repository
// alone, which has no name. A manifest that cannot be read is an error,
// and never a directory that holds nothing; the error repeats nothing
// the manifest said.
func (d *Directory) Holds() (Held, error) {
	read, err := d.read()
	if err != nil {
		return Held{}, err
	}
	switch read.State {
	case manifest.Filling:
		return Held{}, fmt.Errorf("%w: it is being filled", ErrNotWhole)
	case manifest.Failed:
		return Held{}, fmt.Errorf("%w: the last fill of it failed, and whoever asked for the workspace is told why", ErrNotWhole)
	}
	held := Held{Repositories: read.Repositories, Entities: make([]record.Located, 0, len(read.Known))}
	for _, known := range read.Known {
		held.Entities = append(held.Entities, known.Located())
	}
	return held, nil
}

// repository returns the directory one of its repositories is in, and
// where it was brought from as the manifest records it. A repository is
// named as the directory holds it, and no other directory is asked of.
// It is what every question to the version control system goes
// through, and one that is mounted is refused here (ErrMounted).
func (d *Directory) repository(name string) (directory, remote string, err error) {
	// A checkout the build was given is one repository, of no name, in
	// the directory itself.
	if d.alone {
		if name != "" {
			return "", "", fmt.Errorf("the workspace is a checkout the daemon was given, and holds no repository %q", name)
		}
		return d.root, "", nil
	}
	held, err := d.read()
	if err != nil {
		return "", "", err
	}
	for _, each := range held.Repositories {
		switch {
		case each.Name != name:
		case each.Mounted:
			// Its directory is no checkout: a version control system
			// asked of it would read whatever is above it, or what the
			// workspace wrote there.
			return "", "", fmt.Errorf("%s: %w", name, ErrMounted)
		default:
			return filepath.Join(d.root, each.Name), each.Remote, nil
		}
	}
	return "", "", fmt.Errorf("the workspace holds no repository %q", name)
}

// ErrMounted is a repository something else mounted in the directory,
// asked what only a clone says: where it stands in its history, its
// tags, what changed since a revision, what is uncommitted or ignored,
// a tag to make or push, where it was brought from. A mounted
// repository has no history a version control system reads, and none is
// asked of it; where it stands is the changeset its record says (Holds).
var ErrMounted = errors.New("the repository is mounted, and not cloned: it has no history, no tags and no remote")

// Mounted names a repository the directory holds that something else
// mounted there, the first as its manifest records them; none where it
// holds none, or is one repository alone.
func (d *Directory) Mounted() (string, error) {
	held, err := d.read()
	if err != nil {
		return "", err
	}
	for _, each := range held.Repositories {
		if each.Mounted {
			return each.Name, nil
		}
	}
	return "", nil
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

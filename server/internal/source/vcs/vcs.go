// Package vcs is what a workspace asks of a version control system: to
// bring a repository, and to bring more of one. Git is one (server/internal/source/internal/vcs/git), and a
// program chooses which a daemon has.
package vcs

import "context"

// Source is a repository to bring, and what of it.
type Source struct {
	// Remote is where it is brought from, as the system writes one.
	Remote string
	// Ref is the branch or tag to stand at; empty for the remote's
	// default.
	Ref string
	// Roots are the directories to take of it; none for all of it.
	Roots []string
}

// System is a version control system.
type System interface {
	// NameOf returns the name a repository is brought under when it is
	// given none, as the system names the directory it brings one into;
	// empty where the remote names no directory.
	NameOf(remote string) string
	// Revision holds a name to being a revision written in full: the
	// system is asked for one by that name, and reads nothing else into
	// it.
	Revision(name string) error
	// Clone brings a repository into a directory that is not there yet,
	// and returns the revision it stands at. Roots make it partial: only
	// those directories are there.
	Clone(ctx context.Context, directory string, from Source) (revision string, err error)
	// Widen brings more directories of a repository that was brought in
	// part, from the remote it was brought from, whatever the repository
	// now says that is; none asks for all of it, and it is partial no
	// longer.
	Widen(ctx context.Context, directory, remote string, roots []string) error
}

// History is what a workspace asks of a repository's history, to say
// what a release of it would be. A system that keeps none is no History.
type History interface {
	// Standing says where a repository stands.
	Standing(ctx context.Context, directory string) (Standing, error)
	// Tags returns every tag of a repository, in the order of their
	// names, each with the revision it names and whether that is in the
	// history of where the repository stands.
	Tags(ctx context.Context, directory string) ([]Tag, error)
	// Changes returns the revisions in the history of where a repository
	// stands, and not in that of since, that changed a file in any of the
	// directories named, oldest first. An empty since is the whole
	// history. A directory is a name, relative to the repository, never a
	// pattern; none named is refused.
	Changes(ctx context.Context, directory, since string, directories []string) ([]Change, error)
	// TagName holds a name to one the system takes for a tag: it is the
	// system's own rule, asked of the system, and nothing is made.
	TagName(ctx context.Context, name string) error
	// Uncommitted returns the files of a repository that are not as its
	// revision has them, or that the system cannot say are: changed,
	// added, removed, there and not kept, or kept and told not to be
	// looked at; each by its path. What the system is told to ignore is
	// not among them: Ignored returns that.
	Uncommitted(ctx context.Context, directory string) ([]string, error)
	// Ignored returns what is in a repository that the system is told to
	// ignore, each by its path, a directory with a slash after it. It is
	// not of the revision, and whoever reads the repository's files reads
	// it all the same.
	Ignored(ctx context.Context, directory string) ([]string, error)
	// Tag names a revision by a tag, in the repository alone: nothing
	// leaves it. A tag that names the revision already is left; one that
	// names another is refused, and nothing is changed.
	Tag(ctx context.Context, directory, name, revision string) error
	// PushTag sends a tag of a repository to the remote it was brought
	// from, and the tag alone: no branch and no other tag goes with it.
	// One the remote has at the same revision already is left; one it
	// has at another is refused, and nothing there is changed.
	PushTag(ctx context.Context, directory, remote, name string) error
	// Origin is where a repository says it was brought from: what a
	// daemon given a checkout, and told of no remote, pushes its tags
	// to. It is what the repository says, and is held to as any address
	// a caller names is.
	Origin(ctx context.Context, directory string) (string, error)
}

// Standing is where a repository stands.
type Standing struct {
	// Branch is the branch it is on; empty where it is on none.
	Branch string
	// Revision is the revision it stands at.
	Revision string
	// DefaultBranch is the default branch of where it was brought from;
	// empty where that is not known.
	DefaultBranch string
	// Time is when the revision it stands at was committed, in seconds
	// since the epoch: a fact of the commit, the same wherever it is
	// read.
	Time int64
}

// Tag is a tag of a repository.
type Tag struct {
	// Name is its name.
	Name string
	// Revision is the revision it names.
	Revision string
	// Reachable is whether that is in the history of where the repository
	// stands.
	Reachable bool
}

// Change is a revision that changed something.
type Change struct {
	// Revision is the revision.
	Revision string
	// Message is its whole message.
	Message string
	// Time is when it was made, in seconds since the epoch.
	Time int64
}

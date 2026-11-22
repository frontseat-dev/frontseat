package evaluate

import "context"

// Source is what an evaluation reads: the repositories of a workspace, as
// they stood at one moment.
type Source interface {
	// Repositories names the repositories the source holds.
	Repositories() []string
	// Read returns a file of a repository, and whether it is there.
	Read(ctx context.Context, repository, path string) ([]byte, bool, error)
	// Digest returns the SHA-256 of a file of a repository, and whether
	// the file is there, without reading the file where the source knows.
	Digest(ctx context.Context, repository, path string) (string, bool, error)
	// Within selects files beneath a literal directory using relative patterns.
	Within(ctx context.Context, repository, directory string, patterns []string) ([]string, error)
}

// Counting is a Source that counts what it read: an evaluation's span
// says how many files one hashed. A Source that does not count is none.
type Counting interface {
	// Hashed is how many files the source has hashed so far.
	Hashed() int
}

// Hinting is a Source that knows which files may have changed since it
// was last taken: what a kept evaluation checks first. A hint orders
// the checks and decides none of them.
type Hinting interface {
	// MayHaveChanged says whether a file of a repository may differ
	// from what it held.
	MayHaveChanged(repository, path string) bool
}

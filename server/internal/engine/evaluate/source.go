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

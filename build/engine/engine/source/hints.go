package source

import (
	"path"
	"path/filepath"
)

// MayHaveChanged is a hint for ordering work, never evidence that a file
// is as it was: a session checks first what it says may have changed. It
// compares the metadata the snapshot already holds with what the Keeper
// knows of the file, and asks the filesystem nothing. A file that is not
// known may have changed too. Whoever uses it still holds what it read to
// the source through Read and Digest; another request may change this
// answer at any moment, so nothing correct can rest on it.
func (s *Snapshot) MayHaveChanged(repository, name string) bool {
	t := s.repositories[repository]
	if t == nil {
		return true
	}
	key := path.Clean(filepath.ToSlash(name))
	when := s.knows(t, key)
	if !when.listed {
		return true
	}
	_, known := s.keeper.kept(filepath.Join(t.directory, filepath.FromSlash(key)), when.was)
	return !known
}

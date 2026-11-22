package exec

import (
	"errors"
	"path/filepath"
)

// ErrOutside is a path that, once every link on the way is followed,
// is not beneath the directory it was held to.
var ErrOutside = errors.New("it leads outside the directory")

// Within returns a path as it is on the disk, every link on the way
// followed, where that is a directory or beneath it, the directory
// taken the same way: what is brought where the daemon is holds links,
// and what one leads to is what is read or run. A path that is not
// there is the system's error, and one that leads elsewhere ErrOutside.
func Within(directory, at string) (string, error) {
	led, err := filepath.EvalSymlinks(at)
	if err != nil {
		return "", err
	}
	base, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(base, led); err != nil || rel != "." && !filepath.IsLocal(rel) {
		return "", ErrOutside
	}
	return led, nil
}

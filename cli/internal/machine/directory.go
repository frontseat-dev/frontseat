package machine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/frontseat-dev/frontseat/common/record/manifest"
)

// repositoryFile is the file at a repository's root that says which
// plugins build it.
const repositoryFile = "frontseat.yaml"

// Directory returns the directory a command run here builds: the
// nearest, from the working directory upwards, that is a workspace,
// holding its manifest, or else a repository, holding frontseat.yaml. A
// command run beneath one builds it, as one run at its root does, so a
// directory has one build daemon wherever in it a command is run.
func Directory() (string, error) {
	here, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(here); err == nil {
		here = real
	}
	for _, marks := range []string{filepath.FromSlash(manifest.File), repositoryFile} {
		for at := here; ; at = filepath.Dir(at) {
			if _, err := os.Stat(filepath.Join(at, marks)); err == nil {
				return at, nil
			} else if !errors.Is(err, fs.ErrNotExist) {
				return "", err
			}
			if at == filepath.Dir(at) {
				break
			}
		}
	}
	return "", fmt.Errorf("there is nothing to build here: neither %s nor a directory above it holds a %s", here, repositoryFile)
}

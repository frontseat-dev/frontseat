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
// Inside a workspace's sandbox it is the workspace's directory and no
// other: a repository there is part of the workspace, never a checkout
// to build alone, and a workspace that was told nothing to hold yet has
// no manifest, and is not built.
func Directory(inside bool) (string, error) {
	here, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(here); err == nil {
		here = real
	}
	marked := []string{filepath.FromSlash(manifest.File), repositoryFile}
	if inside {
		marked = marked[:1]
	}
	for _, marks := range marked {
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
	if inside {
		return "", fmt.Errorf("there is nothing to build here: neither %s nor a directory above it is this workspace's, which holds %s once the workspace was told what to hold, from outside; a command that builds is run in the workspace's directory", here, manifest.File)
	}
	return "", fmt.Errorf("there is nothing to build here: neither %s nor a directory above it holds a %s", here, repositoryFile)
}

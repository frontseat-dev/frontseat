package machine

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/frontseat-dev/frontseat/common/record/manifest"
)

// repositoryFile is the file at a repository's root that says which
// plugins build it.
const repositoryFile = "frontseat.yaml"

// Directory returns the directory a command run here builds: the
// nearest, from the working directory upwards, that is a workspace,
// holding its manifest, or a repository, holding frontseat.yaml,
// whichever is nearer. A command run beneath one builds it, as one run
// at its root does, so a directory has one build daemon wherever in it a
// command is run. Inside a workspace's sandbox it is the workspace's
// directory and no other: a repository there is part of the workspace,
// never a checkout to build alone, and a workspace that was told nothing
// to hold yet has no manifest, and is not built.
//
// A directory is taken only where it is the user's the command runs as
// (mine): a marker says what is built and by which plugins, and in a
// directory others write, as /tmp, anyone plants one above where a
// command is run. It is git's rule for a repository it did not make
// (safe.directory, after CVE-2022-24765): the owner, and nothing of the
// directory's mode. One that is another's is passed over, and said where
// nothing else is found. What cannot be asked of a directory above, one
// that may not be entered or a file named as the manifest's directory
// is, is no marker there, and not a failure.
func Directory(inside bool) (string, error) {
	here, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(here); err == nil {
		here = real
	}
	passed := ""
	for at := here; ; at = filepath.Dir(at) {
		if marked(at, inside) {
			if info, err := os.Stat(at); err == nil && mine(info) {
				return at, nil
			}
			if passed == "" {
				passed = at
			}
		}
		if at == filepath.Dir(at) {
			break
		}
	}
	another := ""
	if passed != "" {
		another = fmt.Sprintf("; %s is marked as one and is not taken, since the directory is another user's", passed)
	}
	if inside {
		return "", fmt.Errorf("there is nothing to build here: neither %s nor a directory above it is this workspace's, which holds %s once the workspace was told what to hold, from outside; a command that builds is run in the workspace's directory%s", here, manifest.File, another)
	}
	return "", fmt.Errorf("there is nothing to build here: neither %s nor a directory above it holds a %s%s", here, repositoryFile, another)
}

// marked is whether a directory says it is built: a workspace, by its
// manifest, or, outside a sandbox, a repository, by its frontseat.yaml.
func marked(at string, inside bool) bool {
	if _, err := os.Stat(filepath.Join(at, filepath.FromSlash(manifest.File))); err == nil {
		return true
	}
	if inside {
		return false
	}
	_, err := os.Stat(filepath.Join(at, repositoryFile))
	return err == nil
}

// mine is whether a directory is the user's the command runs as. A test
// stands in for it.
var mine = func(directory fs.FileInfo) bool { return ownedBy(directory, os.Getuid()) }

// ownedBy is whether a file is a user's, by number. Where a system
// gives a file no owner, or a process no user, every file is.
func ownedBy(file fs.FileInfo, uid int) bool {
	owner, has := file.Sys().(*syscall.Stat_t)
	return !has || uid < 0 || int(owner.Uid) == uid
}

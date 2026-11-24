package source

import (
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// readOnly is whether a directory is on a file system mounted
// read-only, as Darwin says.
func readOnly(directory string) bool {
	var stat unix.Statfs_t
	return unix.Statfs(directory, &stat) == nil && stat.Flags&unix.MNT_RDONLY != 0
}

// looksStill is whether an open file looks now as it did when it was
// looked at. Darwin has no way to ask past what its kernel kept of an
// earlier answer; a repository is mounted for a box, which is Linux,
// and here this is asked of a test's files.
func looksStill(file *os.File, as fs.FileInfo) bool {
	now, err := file.Stat()
	return err == nil && look(now) == look(as)
}

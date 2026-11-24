package source

import (
	"io/fs"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// readOnly is whether a directory is on a file system mounted
// read-only, as Linux says of the mount it is reached through.
func readOnly(directory string) bool {
	var stat unix.Statfs_t
	return unix.Statfs(directory, &stat) == nil && stat.Flags&unix.ST_RDONLY != 0
}

// looksStill is whether an open file looks now as it did when it was
// looked at, asked of the file system itself and not of what the
// kernel kept of an earlier answer (AT_STATX_FORCE_SYNC, which a
// mount served by a program is asked again for): its size, its mode,
// when it was written and changed, and which file it is.
func looksStill(file *os.File, as fs.FileInfo) bool {
	was, ok := as.Sys().(*syscall.Stat_t)
	conn, err := file.SyscallConn()
	if !ok || err != nil {
		return false
	}
	var now unix.Statx_t
	var failed error
	if conn.Control(func(fd uintptr) {
		failed = unix.Statx(int(fd), "", unix.AT_EMPTY_PATH|unix.AT_STATX_FORCE_SYNC, unix.STATX_BASIC_STATS, &now)
	}) != nil || failed != nil || now.Mask&unix.STATX_BASIC_STATS != unix.STATX_BASIC_STATS {
		return false
	}
	return now.Ino == was.Ino && int64(now.Size) == was.Size && uint32(now.Mode) == was.Mode &&
		now.Mtime.Sec == int64(was.Mtim.Sec) && int64(now.Mtime.Nsec) == int64(was.Mtim.Nsec) &&
		now.Ctime.Sec == int64(was.Ctim.Sec) && int64(now.Ctime.Nsec) == int64(was.Ctim.Nsec)
}

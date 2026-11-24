package source

import (
	"io/fs"
	"syscall"
)

// identity returns which file of the disk a file is, and when anything
// of it last changed.
func identity(info fs.FileInfo) (file uint64, changed int64) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0
	}
	return stat.Ino, changedAt(stat)
}

// disk returns which directory of which disk a directory is, and
// whether the disk says.
func disk(info fs.FileInfo) (device, file uint64, known bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return deviceOf(stat), stat.Ino, true
}

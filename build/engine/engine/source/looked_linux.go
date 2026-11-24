package source

import "syscall"

// changedAt is when anything of a file last changed, as Linux says it.
func changedAt(stat *syscall.Stat_t) int64 { return stat.Ctim.Nano() }

// deviceOf is which disk a file is on, as Linux says it.
func deviceOf(stat *syscall.Stat_t) uint64 { return stat.Dev }

// fileSystemOf returns the file system a directory is on, as Linux
// numbers it, by its name where it has one here, and whether it is
// known to date a file's changes itself (linuxDates); no name where
// Linux does not say.
func fileSystemOf(directory string) (string, bool) {
	var stat syscall.Statfs_t
	if syscall.Statfs(directory, &stat) != nil {
		return "", false
	}
	return linuxFileSystem(uint32(stat.Type))
}

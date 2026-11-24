package source

import "syscall"

// changedAt is when anything of a file last changed, as Darwin says it.
func changedAt(stat *syscall.Stat_t) int64 { return stat.Ctimespec.Nano() }

// deviceOf is which disk a file is on, as Darwin says it.
func deviceOf(stat *syscall.Stat_t) uint64 { return uint64(stat.Dev) }

// fileSystemOf returns the file system a directory is on, as Darwin
// names it, and whether it is known to date a file's changes itself
// (darwinDates); no name where Darwin does not say.
func fileSystemOf(directory string) (string, bool) {
	var stat syscall.Statfs_t
	if syscall.Statfs(directory, &stat) != nil {
		return "", false
	}
	name := make([]byte, 0, len(stat.Fstypename))
	for _, c := range stat.Fstypename {
		if c == 0 {
			break
		}
		name = append(name, byte(c))
	}
	return string(name), darwinDates[string(name)]
}

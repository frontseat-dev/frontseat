package source

import "syscall"

// changedAt is when anything of a file last changed, as Darwin says it.
func changedAt(stat *syscall.Stat_t) int64 { return stat.Ctimespec.Nano() }

package source

import "syscall"

// changedAt is when anything of a file last changed, as Linux says it.
func changedAt(stat *syscall.Stat_t) int64 { return stat.Ctim.Nano() }

// deviceOf is which disk a file is on, as Linux says it.
func deviceOf(stat *syscall.Stat_t) uint64 { return stat.Dev }

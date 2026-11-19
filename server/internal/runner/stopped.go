package runner

import (
	"path/filepath"
	"strconv"
	"syscall"
)

// runnerName is the runner's name as the kernel keeps a process's, in
// fifteen bytes: frontseat-runner's.
const runnerName = "frontseat-runne"

// continueStopped continues each runner the table of processes at proc
// says is stopped: an action may stop the runner of its sandbox, which
// would hang every action sent to its slot after it, and Landlock's scope
// of signals does not hold inside an OpenShell sandbox.
func continueStopped(proc string) {
	for _, pid := range stoppedRunners(proc) {
		_ = syscall.Kill(pid, syscall.SIGCONT)
	}
}

// stoppedRunners is the runners the table of processes at proc says are
// stopped.
func stoppedRunners(proc string) []int {
	stats, _ := filepath.Glob(filepath.Join(proc, "[0-9]*", "stat"))
	var out []int
	for _, at := range stats {
		pid, err := strconv.Atoi(filepath.Base(filepath.Dir(at)))
		if err != nil {
			continue
		}
		if name, state, _, err := stat(proc, pid); err == nil && name == runnerName && (state == 'T' || state == 't') {
			out = append(out, pid)
		}
	}
	return out
}

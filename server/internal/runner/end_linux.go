package runner

import (
	"errors"
	"log"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// subreaper keeps whatever this process starts beneath it, whatever its
// parent does: an orphan of an action's comes to the runner, never to the
// sandbox's init.
func subreaper() error {
	return unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0)
}

// end ends everything this process started, and all they started: each
// is killed, again and again until a pass finds no thread of any of them
// alive, and reaped as it ends. A process a fatal signal is pending for
// starts nothing, so what forks as fast as it can ends with all it forked;
// and none is waited on to stop, so what cannot stop (a vfork parent, in
// uninterruptible sleep) is killed as well. Where they have not all ended
// by the deadline, the runner ends: its sandbox ends with it, and the pool
// makes the slot's anew, with nothing of what was left.
func end() {
	self := os.Getpid()
	deadline := time.Now().Add(endWithin)
	for {
		alive := 0
		for _, pid := range descendants("/proc", self) {
			_ = unix.Kill(pid, unix.SIGKILL)
			if living("/proc", pid) {
				alive++
			}
		}
		reapEnded()
		if alive == 0 {
			return
		}
		if time.Now().After(deadline) {
			log.Printf("frontseat-runner: what an action started did not end within %s: the runner ends, and its sandbox with it", endWithin)
			os.Exit(1)
		}
		time.Sleep(time.Millisecond)
	}
}

// endWithin is how long end waits for what an action started to end.
const endWithin = 30 * time.Second

// reapEnded reaps every child of this process that has ended, waiting for
// none.
func reapEnded() {
	for {
		pid, err := unix.Wait4(-1, nil, unix.WNOHANG, nil)
		if pid <= 0 || errors.Is(err, syscall.ECHILD) || err != nil {
			return
		}
	}
}

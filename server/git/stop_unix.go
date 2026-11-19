//go:build !windows

package git

import (
	osexec "os/exec"
	"syscall"

	"github.com/frontseat-dev/frontseat/server/internal/confine"
)

// stopTogether starts git in a process group of its own and, where what
// asked is cancelled, stops the group: git and every program it started
// that has not left it.
func stopTogether(cmd *osexec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// Git is not yet waited for, so its group is still its own.
		return confine.SignalGroup(cmd.Process.Pid, syscall.SIGKILL)
	}
}

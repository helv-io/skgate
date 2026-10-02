//go:build unix

package managed

import (
	"os/exec"
	"syscall"
)

// setGroup puts the child in its own process group, so signals reach grandchildren too.
func setGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func signalGroup(pid int, kill bool) {
	sig := syscall.SIGTERM
	if kill {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-pid, sig)
}

// groupAlive reports whether any process of the group still exists.
func groupAlive(pid int) bool { return syscall.Kill(-pid, 0) == nil }

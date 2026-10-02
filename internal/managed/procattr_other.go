//go:build !unix

package managed

import (
	"os"
	"os/exec"
)

func setGroup(cmd *exec.Cmd) {}

func signalGroup(pid int, kill bool) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

func groupAlive(pid int) bool { return false }

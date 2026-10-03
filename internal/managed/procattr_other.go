//go:build !unix

package managed

import (
	"io/fs"
	"os"
	"os/exec"
)

func setGroup(cmd *exec.Cmd, _ *RunAs) {}

func signalGroup(pid int, kill bool) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

func groupAlive(pid int) bool { return false }

func ownerOf(fs.FileInfo) (int, bool) { return 0, false }

func lchown(string, int, int) error { return nil }

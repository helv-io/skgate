//go:build unix

package managed

import (
	"io/fs"
	"os"
	"os/exec"
	"syscall"
)

// setGroup puts the child in its own process group, so signals reach grandchildren too. With as
// set, the child also runs as that user and group, without supplementary groups.
func setGroup(cmd *exec.Cmd, as *RunAs) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if as != nil {
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(as.UID), Gid: uint32(as.GID), Groups: []uint32{}}
	}
}

func signalGroup(pid int, kill bool) {
	sig := syscall.SIGTERM
	if kill {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-pid, sig)
}

// groupAlive reports whether any process of the group still exists.
func groupAlive(pid int) bool { return syscall.Kill(-pid, 0) == nil }

// ownerOf returns the owning user of fi.
func ownerOf(fi fs.FileInfo) (uid int, ok bool) {
	st, is := fi.Sys().(*syscall.Stat_t)
	if !is {
		return 0, false
	}
	return int(st.Uid), true
}

func lchown(path string, uid, gid int) error { return os.Lchown(path, uid, gid) }

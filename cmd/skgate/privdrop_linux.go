//go:build linux

package main

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

func fileOwner(fi fs.FileInfo) (uid, gid int, ok bool) {
	st, isStat := fi.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, 0, false
	}
	return int(st.Uid), int(st.Gid), true
}

// dropPrivileges clears supplementary groups, then sets gid and uid (Go 1.16+ applies
// these to every thread), and verifies that root cannot be regained.
func dropPrivileges(uid, gid int) error {
	if err := syscall.Setgroups([]int{}); err != nil {
		return fmt.Errorf("setgroups: %w", err)
	}
	if err := syscall.Setgid(gid); err != nil {
		return fmt.Errorf("setgid(%d): %w", gid, err)
	}
	if err := syscall.Setuid(uid); err != nil {
		return fmt.Errorf("setuid(%d): %w", uid, err)
	}
	if os.Getuid() != uid || os.Geteuid() != uid || os.Getgid() != gid || os.Getegid() != gid {
		return fmt.Errorf("verify failed: uid=%d euid=%d gid=%d egid=%d, want %d:%d",
			os.Getuid(), os.Geteuid(), os.Getgid(), os.Getegid(), uid, gid)
	}
	if err := syscall.Setuid(0); err == nil {
		return fmt.Errorf("verify failed: setuid(0) succeeded after dropping privileges")
	}
	if err := syscall.Setgid(0); err == nil {
		return fmt.Errorf("verify failed: setgid(0) succeeded after dropping privileges")
	}
	return nil
}

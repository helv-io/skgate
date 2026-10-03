//go:build linux

package main

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
	"unsafe"
)

func fileOwner(fi fs.FileInfo) (uid, gid int, ok bool) {
	st, isStat := fi.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, 0, false
	}
	return int(st.Uid), int(st.Gid), true
}

// Linux capability numbers kept when managed runners are available: enough to start them as
// another user, hand them their directories and stop them, nothing else.
const (
	capChown  = 0
	capKill   = 5
	capSetGid = 6
	capSetUID = 7
	keepCaps  = 1<<capChown | 1<<capKill | 1<<capSetGid | 1<<capSetUID

	prSetKeepCaps = 8
	capVersion3   = 0x20080522
)

type capHeader struct {
	version uint32
	pid     int32
}

type capData struct{ effective, permitted, inheritable uint32 }

// dropPrivileges clears supplementary groups, then sets gid and uid (Go 1.16+ applies
// these to every thread), and verifies that root cannot be regained. With runners set, the
// process keeps CAP_CHOWN, CAP_KILL, CAP_SETUID and CAP_SETGID (and nothing else) after the switch, so it
// can start managed servers as nobody; then the uid is the only thing that cannot go back to 0
// by itself, and that is what is verified.
func dropPrivileges(uid, gid int, runners bool) error {
	if runners {
		if _, _, e := syscall.AllThreadsSyscall(syscall.SYS_PRCTL, prSetKeepCaps, 1, 0); e != 0 {
			return fmt.Errorf("prctl(KEEPCAPS): %w", e)
		}
	}
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
	if runners {
		return keepOnlyRunnerCaps()
	}
	if err := syscall.Setuid(0); err == nil {
		return fmt.Errorf("verify failed: setuid(0) succeeded after dropping privileges")
	}
	if err := syscall.Setgid(0); err == nil {
		return fmt.Errorf("verify failed: setgid(0) succeeded after dropping privileges")
	}
	return nil
}

// keepOnlyRunnerCaps narrows the capability sets of every thread to keepCaps and turns KEEPCAPS off.
func keepOnlyRunnerCaps() error {
	hdr := capHeader{version: capVersion3}
	data := [2]capData{{effective: keepCaps, permitted: keepCaps}}
	if _, _, e := syscall.AllThreadsSyscall(syscall.SYS_CAPSET, uintptr(unsafe.Pointer(&hdr)), uintptr(unsafe.Pointer(&data[0])), 0); e != 0 {
		return fmt.Errorf("capset: %w", e)
	}
	if _, _, e := syscall.AllThreadsSyscall(syscall.SYS_PRCTL, prSetKeepCaps, 0, 0); e != 0 {
		return fmt.Errorf("prctl(KEEPCAPS off): %w", e)
	}
	var got [2]capData
	hdr = capHeader{version: capVersion3}
	if _, _, e := syscall.RawSyscall(syscall.SYS_CAPGET, uintptr(unsafe.Pointer(&hdr)), uintptr(unsafe.Pointer(&got[0])), 0); e != 0 {
		return fmt.Errorf("capget: %w", e)
	}
	if got[0].effective != keepCaps || got[0].permitted != keepCaps || got[1].effective != 0 || got[1].permitted != 0 {
		return fmt.Errorf("verify failed: capabilities are %x/%x %x/%x, want only chown, kill, setuid and setgid", got[0].effective, got[0].permitted, got[1].effective, got[1].permitted)
	}
	return nil
}

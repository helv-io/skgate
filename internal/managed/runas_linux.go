//go:build linux

package managed

import (
	"os"
	"syscall"
	"unsafe"
)

// Capabilities a process needs to start children as another user, hand them directories and stop them.
const (
	capChown  = 0
	capKill   = 5
	capSetGid = 6
	capSetUID = 7
)

// effectiveCaps reads the effective capability set of the calling thread.
func effectiveCaps() (uint64, bool) {
	hdr := struct {
		version uint32
		pid     int32
	}{version: 0x20080522}
	var data [2]struct{ effective, permitted, inheritable uint32 }
	if _, _, e := syscall.RawSyscall(syscall.SYS_CAPGET, uintptr(unsafe.Pointer(&hdr)), uintptr(unsafe.Pointer(&data[0])), 0); e != 0 {
		return 0, false
	}
	return uint64(data[1].effective)<<32 | uint64(data[0].effective), true
}

// canRunAs reports whether the capability set allows switching children to another user.
func canRunAs(eff uint64) bool {
	for _, c := range []uint{capChown, capKill, capSetGid, capSetUID} {
		if eff&(1<<c) == 0 {
			return false
		}
	}
	return true
}

// DetectRunAs returns the unprivileged identity children run as (nobody), or nil when this
// process cannot switch users (it is not root and holds no CAP_SETUID).
func DetectRunAs() *RunAs {
	if os.Geteuid() == NobodyID {
		return nil
	}
	if eff, ok := effectiveCaps(); ok && canRunAs(eff) {
		return &RunAs{UID: NobodyID, GID: NobodyID}
	}
	return nil
}

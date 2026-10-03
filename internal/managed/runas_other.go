//go:build !linux

package managed

// DetectRunAs: only Linux can switch users per child.
func DetectRunAs() *RunAs { return nil }

//go:build !linux

package main

import (
	"errors"
	"io/fs"
)

func fileOwner(fs.FileInfo) (int, int, bool) { return 0, 0, false }

func dropPrivileges(int, int, bool) error {
	return errors.New("privilege drop is only supported on linux")
}

package managed

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

// NobodyID is the uid and gid of the unprivileged account managed children run as.
const NobodyID = 65534

// RunAs is the identity children (servers, install and git steps) run as. Nil runs them as skgate
// itself, which is what happens when skgate cannot switch users.
type RunAs struct{ UID, GID int }

// confine puts cmd in its own process group and, when configured, under the runner identity.
func (m *Manager) confine(cmd *exec.Cmd) { setGroup(cmd, m.o.RunAs) }

// handOver creates dir and gives it and everything below it to the runner, so a child can write
// there. The directory gets mode 0755: skgate can still read it. It is a no-op beyond MkdirAll
// without a runner identity, and cheap once done: the top directory is re-owned last, so an
// interrupted run is picked up again.
func (m *Manager) handOver(dir string) error {
	_, statErr := os.Lstat(dir)
	created := statErr != nil
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	as := m.o.RunAs
	if as == nil {
		return nil
	}
	if !created {
		if fi, err := os.Lstat(dir); err == nil {
			if uid, ok := ownerOf(fi); ok && uid == as.UID {
				return nil
			}
		}
		var first error
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || p == dir {
				return nil
			}
			if e := lchown(p, as.UID, as.GID); e != nil && first == nil {
				first = e
			}
			return nil
		})
		if first != nil {
			return first
		}
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return err
	}
	return lchown(dir, as.UID, as.GID)
}

// traversable lets the runner walk down to path: every directory skgate owns from path upwards
// gets the "others may enter" bit. Nothing becomes listable or readable by that, and skgate's own
// files are private (0600), so the database and the key file stay out of reach.
func (m *Manager) traversable(path string) {
	if m.o.RunAs == nil {
		return
	}
	for p := path; ; p = filepath.Dir(p) {
		fi, err := os.Stat(p)
		if err != nil || !fi.IsDir() {
			return
		}
		if uid, ok := ownerOf(fi); !ok || uid != os.Geteuid() {
			return
		}
		if fi.Mode().Perm()&0o001 == 0 {
			_ = os.Chmod(p, fi.Mode().Perm()|0o001)
		}
		if filepath.Dir(p) == p {
			return
		}
	}
}

// removeAll deletes a tree the runner may have written. What it owns is removed by a helper
// running as the runner, since skgate itself has no write access there.
func (m *Manager) removeAll(path string) error {
	err := os.RemoveAll(path)
	if err == nil || m.o.RunAs == nil {
		return err
	}
	if _, serr := os.Lstat(path); errors.Is(serr, fs.ErrNotExist) {
		return nil
	}
	rm := exec.Command("rm", "-rf", "--", path)
	rm.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin"}
	m.confine(rm)
	if out, rerr := rm.CombinedOutput(); rerr != nil {
		return errors.New("removing " + filepath.Base(path) + ": " + clipLine(string(out)))
	}
	return os.RemoveAll(path)
}

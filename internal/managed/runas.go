package managed

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

// NobodyID is the uid and gid of the unprivileged account skgate refuses to be (see DetectRunAs).
const NobodyID = 65534

// Runner ids: each managed upstream runs as its own uid and gid from this range, so one runner
// cannot read another's environment, files or caches. The range stays below 65536 so it maps in
// rootless containers and user namespaces, and clear of nobody (65534).
const (
	RunnerIDBase  = 61000
	RunnerIDCount = 4000
)

// RunAs is the identity children (servers, install and git steps) run as. Nil runs them as skgate
// itself, which is what happens when skgate cannot switch users. Options.RunAs only says that
// switching is possible; each upstream gets its own ids (see runner).
type RunAs struct{ UID, GID int }

// runnerIDsFile keeps the alias to id assignment, so an upstream keeps its id (and the ownership
// of its directories) across restarts.
const runnerIDsFile = ".runner-ids.json"

// runner returns the identity of alias's children: its own uid and gid from the runner range,
// assigned on first use and kept in <Dir>/.runner-ids.json. Nil without the ability to switch.
func (m *Manager) runner(alias string) *RunAs {
	if m.o.RunAs == nil {
		return nil
	}
	m.idMu.Lock()
	defer m.idMu.Unlock()
	if m.ids == nil {
		m.ids = map[string]int{}
		if m.o.Dir != "" {
			if b, err := os.ReadFile(filepath.Join(m.o.Dir, runnerIDsFile)); err == nil {
				_ = json.Unmarshal(b, &m.ids)
			}
		}
	}
	if id, ok := m.ids[alias]; ok && id >= RunnerIDBase && id < RunnerIDBase+RunnerIDCount {
		return &RunAs{UID: id, GID: id}
	}
	used := map[int]bool{}
	for _, id := range m.ids {
		used[id] = true
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(alias))
	start := int(h.Sum32() % RunnerIDCount)
	id := -1
	for i := 0; i < RunnerIDCount; i++ {
		if c := RunnerIDBase + (start+i)%RunnerIDCount; !used[c] {
			id = c
			break
		}
	}
	if id < 0 { // every id is taken: share the hashed one rather than fail
		id = RunnerIDBase + start
	}
	m.ids[alias] = id
	if m.o.Dir != "" {
		if b, err := json.Marshal(m.ids); err == nil {
			if err := os.MkdirAll(m.o.Dir, 0o700); err == nil {
				tmp := filepath.Join(m.o.Dir, runnerIDsFile+".tmp")
				if os.WriteFile(tmp, b, 0o600) == nil {
					_ = os.Rename(tmp, filepath.Join(m.o.Dir, runnerIDsFile))
				}
			}
		}
	}
	return &RunAs{UID: id, GID: id}
}

// RunnerOf reports the uid alias's children run as, for the admin and logs ("" when skgate cannot switch users).
func (m *Manager) RunnerOf(alias string) string {
	if as := m.runner(alias); as != nil {
		return fmt.Sprintf("uid %d", as.UID)
	}
	return ""
}

// confine puts cmd in its own process group and, when skgate can switch users, under alias's runner identity.
func (m *Manager) confine(cmd *exec.Cmd, alias string) { setGroup(cmd, m.runner(alias)) }

// handOver creates dir and gives it and everything below it to alias's runner, so its children can
// write there. The directory gets mode 0750 with skgate's own group: skgate can still read it, and
// no other runner can enter it. It is a no-op beyond MkdirAll without a runner identity, and cheap
// once done: the top directory is re-owned last, so an interrupted run is picked up again, and a
// directory left by an older release (owned by nobody, mode 0755) is taken over on the next start.
func (m *Manager) handOver(dir, alias string) error {
	_, statErr := os.Lstat(dir)
	created := statErr != nil
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	as := m.runner(alias)
	if as == nil {
		return nil
	}
	if !created {
		if fi, err := os.Lstat(dir); err == nil {
			if uid, ok := ownerOf(fi); ok && uid == as.UID && fi.Mode().Perm() == 0o750 {
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
	if err := os.Chmod(dir, 0o750); err != nil {
		return err
	}
	return lchown(dir, as.UID, os.Getegid())
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

// removeAll deletes a tree alias's runner may have written. What it owns is removed by a helper
// running as that runner, since skgate itself has no write access there.
func (m *Manager) removeAll(path, alias string) error {
	err := os.RemoveAll(path)
	if err == nil || m.o.RunAs == nil {
		return err
	}
	if _, serr := os.Lstat(path); errors.Is(serr, fs.ErrNotExist) {
		return nil
	}
	rm := exec.Command("rm", "-rf", "--", path)
	rm.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin"}
	m.confine(rm, alias)
	if out, rerr := rm.CombinedOutput(); rerr != nil {
		return errors.New("removing " + filepath.Base(path) + ": " + clipLine(string(out)))
	}
	return os.RemoveAll(path)
}

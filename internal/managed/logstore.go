package managed

import (
	"os"
	"path/filepath"
)

// The log of a process is kept in memory while it runs and written to <Dir>/.logs/<alias>.json when a run ends
// (stop, crash, shutdown), so it survives a restart of skgate. The file holds the same two runs as the memory
// and, like the memory, only lines whose secrets are already masked. It is private to skgate (0600).

func (m *Manager) logFile(alias string) string {
	if m.o.Dir == "" {
		return ""
	}
	if _, err := m.AliasDir(alias); err != nil {
		return ""
	}
	return filepath.Join(m.o.Dir, ".logs", alias+".json")
}

func (p *Proc) loadLogs() {
	if f := p.m.logFile(p.alias); f != "" {
		if b, err := os.ReadFile(f); err == nil {
			if p.ring.Import(b) != nil {
				p.ring.Clear()
			}
		}
	}
}

// saveLogs writes the log file; a failure is logged, never fatal.
func (p *Proc) saveLogs() {
	f := p.m.logFile(p.alias)
	if f == "" {
		return
	}
	if err := writeFileAtomic(f, p.ring.Export()); err != nil {
		p.logf("log file not saved: %v", err)
	}
}

func writeFileAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Log returns the log of alias: the live one of its process, or the one kept on disk when it has no process
// (a disabled upstream). The second result is false when there is none.
func (m *Manager) Log(alias string) (*Ring, bool) {
	if p := m.Lookup(alias); p != nil {
		return p.ring, true
	}
	f := m.logFile(alias)
	if f == "" {
		return nil, false
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return nil, false
	}
	r := NewRing(m.o.LogLines)
	if r.Import(b) != nil {
		return nil, false
	}
	return r, true
}

// DropLog deletes the kept log of alias (the upstream was deleted).
func (m *Manager) DropLog(alias string) {
	if f := m.logFile(alias); f != "" {
		_ = os.Remove(f)
	}
}

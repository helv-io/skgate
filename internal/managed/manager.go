package managed

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

// Process states.
const (
	StateStopped  = "stopped"
	StateStarting = "starting"
	StateRunning  = "running"
	StateFailed   = "failed"
)

// ErrDisabled is returned when the build cannot run managed upstreams (slim image).
var ErrDisabled = errors.New("managed upstreams are not available in the slim image")

// Options configure a Manager. Zero values pick production defaults.
type Options struct {
	Enabled    bool
	Dir        string        // root of the per-alias directories
	CacheDir   string        // root of the per-alias package caches (default <Dir>/.cache)
	MaxProcs   int           // concurrent processes (starting or running); 0 = unlimited
	StopGrace  time.Duration // SIGTERM to SIGKILL
	LogLines   int           // ring buffer size per process
	InstallMax time.Duration // limit for one install/clone/pull step
	Logf       func(format string, args ...any)
	Environ    func() []string // parent environment, default os.Environ
	Version    string          // skgate version, sent as clientInfo
	RunAs      *RunAs          // identity of children; nil = same as skgate (see DetectRunAs)

	// Supervision tuning (tests shrink these).
	BackoffBase  time.Duration // first restart delay, doubles per consecutive crash
	BackoffMax   time.Duration
	MaxCrashes   int           // consecutive crashes before the process is marked failed
	StableAfter  time.Duration // a run this long resets the crash counter
	FailCooldown time.Duration // a failed process is not restarted by requests for this long
	Tick         time.Duration // idle/session janitor interval
	CheckEvery   time.Duration // how often git upstreams look for a newer commit; 0 = default (30m), negative = never
}

func (o *Options) defaults() {
	if o.CacheDir == "" {
		o.CacheDir = filepath.Join(o.Dir, ".cache")
	}
	if o.MaxProcs < 0 {
		o.MaxProcs = 0
	}
	if o.StopGrace <= 0 {
		o.StopGrace = 5 * time.Second
	}
	if o.LogLines < 1 {
		o.LogLines = 2000
	}
	if o.InstallMax <= 0 {
		o.InstallMax = 15 * time.Minute
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.Environ == nil {
		o.Environ = osEnviron
	}
	if o.BackoffBase <= 0 {
		o.BackoffBase = time.Second
	}
	if o.BackoffMax <= 0 {
		o.BackoffMax = 30 * time.Second
	}
	if o.MaxCrashes < 1 {
		o.MaxCrashes = 5
	}
	if o.StableAfter <= 0 {
		o.StableAfter = 30 * time.Second
	}
	if o.FailCooldown <= 0 {
		o.FailCooldown = 30 * time.Second
	}
	if o.CheckEvery == 0 {
		o.CheckEvery = 30 * time.Minute
	}
	if o.Tick <= 0 {
		o.Tick = 5 * time.Second
	}
}

// Manager owns all managed processes.
type Manager struct {
	o      Options
	mu     sync.Mutex
	procs  map[string]*Proc
	active int // processes holding a slot
	closed bool
	quit   chan struct{}
	wg     sync.WaitGroup
	ctx    context.Context // ends at Shutdown; background updates and checks use it
	cancel context.CancelFunc
}

// NewManager returns a manager. It starts a small janitor goroutine; call Shutdown to end it.
func NewManager(o Options) *Manager {
	o.defaults()
	m := &Manager{o: o, procs: map[string]*Proc{}, quit: make(chan struct{})}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	if o.Enabled {
		if o.RunAs != nil {
			o.Logf("managed: servers run as uid=%d gid=%d, each with its own writable directories", o.RunAs.UID, o.RunAs.GID)
		} else {
			o.Logf("managed: WARNING cannot switch users (not root, no CAP_SETUID): servers run as skgate itself (uid=%d) and can read its database and key file; start the container as root so they run as nobody", os.Geteuid())
		}
	}
	m.wg.Add(1)
	go m.janitor()
	return m
}

// Enabled reports whether processes may be spawned.
func (m *Manager) Enabled() bool { return m.o.Enabled }

// Dir is the root directory of the per-alias data.
func (m *Manager) Dir() string { return m.o.Dir }

var aliasDirRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// AliasDir returns <Dir>/<alias>, refusing anything that is not a plain name.
func (m *Manager) AliasDir(alias string) (string, error) {
	if !aliasDirRE.MatchString(alias) || alias == "." || alias == ".." {
		return "", fmt.Errorf("alias %q cannot be used as a directory name", alias)
	}
	return filepath.Join(m.o.Dir, alias), nil
}

// AliasCacheDir returns <CacheDir>/<alias>, the package caches of one upstream (npm, uv, pip and
// XDG). Each upstream has its own, so clearing one never touches another.
func (m *Manager) AliasCacheDir(alias string) (string, error) {
	if _, err := m.AliasDir(alias); err != nil {
		return "", err
	}
	return filepath.Join(m.o.CacheDir, alias), nil
}

// Proc returns the process for spec.Alias, creating it when needed. When the spec changed in a
// way that affects the running process, the old process is stopped gracefully first and the new
// one starts on demand (or right away for always-on upstreams, via StartAlways).
func (m *Manager) Proc(spec Spec) (*Proc, error) {
	if !m.o.Enabled {
		return nil, ErrDisabled
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if _, err := m.AliasDir(spec.Alias); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("shutting down")
	}
	p := m.procs[spec.Alias]
	if p == nil {
		p = newProc(m, spec)
		m.procs[spec.Alias] = p
		m.mu.Unlock()
		return p, nil
	}
	m.mu.Unlock()
	p.specMu.Lock()
	defer p.specMu.Unlock()
	if p.setSpec(spec) { // run-relevant change: replace the process
		p.Stop()
		p.applySpec(spec)
	}
	return p, nil
}

// Lookup returns the process for alias if one exists.
func (m *Manager) Lookup(alias string) *Proc {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.procs[alias]
}

// Forget stops and removes the process of alias (upstream deleted or disabled).
func (m *Manager) Forget(alias string) {
	m.mu.Lock()
	p := m.procs[alias]
	delete(m.procs, alias)
	m.mu.Unlock()
	if p != nil {
		p.Stop()
		p.closeSessions()
	}
}

// bg runs fn in the background, tracked so Shutdown waits for it. fn stops when ctx ends.
func (m *Manager) bg(fn func(ctx context.Context)) bool {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return false
	}
	m.wg.Add(1)
	m.mu.Unlock()
	go func() { defer m.wg.Done(); fn(m.ctx) }()
	return true
}

// Register creates the processes of specs without starting them, so their update state (installed
// commit, remote check) is known before the first request.
func (m *Manager) Register(specs []Spec) {
	for _, s := range specs {
		if _, err := m.Proc(s); err != nil {
			m.o.Logf("managed[%s]: not registered: %v", s.Alias, err)
		}
	}
}

// StartAlways starts every always-on spec (boot). Errors are logged per alias.
func (m *Manager) StartAlways(specs []Spec) {
	for _, s := range specs {
		if s.Lifecycle != Always {
			continue
		}
		p, err := m.Proc(s)
		if err != nil {
			m.o.Logf("managed[%s]: not started: %v", s.Alias, err)
			continue
		}
		if err := p.Start(); err != nil {
			m.o.Logf("managed[%s]: not started: %v", s.Alias, err)
		}
	}
}

// Statuses returns the status of every known process, sorted by alias.
func (m *Manager) Statuses() map[string]Status {
	m.mu.Lock()
	ps := make([]*Proc, 0, len(m.procs))
	for _, p := range m.procs {
		ps = append(ps, p)
	}
	m.mu.Unlock()
	out := map[string]Status{}
	for _, p := range ps {
		out[p.alias] = p.Status()
	}
	return out
}

// Aliases lists known process aliases, sorted.
func (m *Manager) Aliases() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var a []string
	for k := range m.procs {
		a = append(a, k)
	}
	sort.Strings(a)
	return a
}

// Shutdown stops every process (SIGTERM, then SIGKILL after the grace period) and ends the
// janitor. It is safe to call twice.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	close(m.quit)
	m.cancel()
	ps := make([]*Proc, 0, len(m.procs))
	for _, p := range m.procs {
		ps = append(ps, p)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, p := range ps {
		wg.Add(1)
		go func(p *Proc) { defer wg.Done(); p.Stop(); p.closeSessions() }(p)
	}
	wg.Wait()
	m.wg.Wait()
}

func (m *Manager) acquire() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.o.MaxProcs > 0 && m.active >= m.o.MaxProcs {
		return fmt.Errorf("managed process limit reached (%d, MANAGED_MAX_PROCS)", m.o.MaxProcs)
	}
	m.active++
	return nil
}

func (m *Manager) release() {
	m.mu.Lock()
	if m.active > 0 {
		m.active--
	}
	m.mu.Unlock()
}

// janitor stops idle on-demand processes and expires idle client sessions.
func (m *Manager) janitor() {
	defer m.wg.Done()
	t := time.NewTicker(m.o.Tick)
	defer t.Stop()
	for {
		select {
		case <-m.quit:
			return
		case <-t.C:
			m.mu.Lock()
			ps := make([]*Proc, 0, len(m.procs))
			for _, p := range m.procs {
				ps = append(ps, p)
			}
			m.mu.Unlock()
			for _, p := range ps {
				p.housekeeping()
			}
		}
	}
}

func ensureDir(path string) error { return os.MkdirAll(path, 0o700) }

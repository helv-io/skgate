package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Status is a snapshot of one process for the admin UI.
type Status struct {
	State    string
	Phase    string // "installing", "updating", "initializing", "restarting in 4s" while starting
	PID      int
	Since    time.Time // when the current run started (zero when not running)
	Restarts int
	LastErr  string
	LastExit time.Time
	Sessions int
	InFlight int
	Held     bool // stopped by an administrator; requests do not start it
}

// Uptime is the time since Since, or zero.
func (s Status) Uptime() time.Duration {
	if s.State != StateRunning || s.Since.IsZero() {
		return 0
	}
	return time.Since(s.Since).Truncate(time.Second)
}

type child struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	wmu     sync.Mutex
	exited  chan struct{}
	pid     int
	waitErr error
}

func (c *child) send(b []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.stdin.Write(append(b, '\n'))
	return err
}

type supervisor struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Proc is one managed upstream process plus its client sessions.
type Proc struct {
	m     *Manager
	alias string
	ring  *Ring

	lifeMu sync.Mutex // serializes Start/Stop
	specMu sync.Mutex // serializes spec replacement

	mu         sync.Mutex
	spec       Spec
	fp         string
	state      string
	phase      string
	pid        int
	startedAt  time.Time
	restarts   int
	crashes    int
	lastErr    string
	lastExit   time.Time
	failedAt   time.Time
	held       bool
	changed    chan struct{}
	sup        *supervisor
	cur        *child
	initResult json.RawMessage
	advCaps    map[string]bool
	forceSync  bool // next start refreshes the git checkout and reruns install
	lastActive time.Time
	redact     *redactor

	// update tracking (see update.go)
	rev, revFull string      // installed commit of a git upstream (short, full)
	remote       *RemoteInfo // last ls-remote result
	checking     bool
	updErr       string // last failed update (the previous version keeps running where possible)
	updErrAt     time.Time
	lastUpdate   string // one line about the last applied update
	lastUpdateAt time.Time
	autoNext     time.Time
	serverVer    string // serverInfo name and version from the last initialize
	updMu        sync.Mutex

	sessions map[string]*Session
	pending  map[int64]*pendingCall
	tokens   map[string]*pendingCall
	srvReqs  map[string]*serverReq
	nextID   int64
	internal *Session
}

func newProc(m *Manager, spec Spec) *Proc {
	p := &Proc{m: m, alias: spec.Alias, ring: NewRing(m.o.LogLines), state: StateStopped,
		changed: make(chan struct{}), sessions: map[string]*Session{}, pending: map[int64]*pendingCall{},
		tokens: map[string]*pendingCall{}, srvReqs: map[string]*serverReq{}, lastActive: time.Now()}
	p.applySpec(spec)
	return p
}

func (p *Proc) applySpec(s Spec) {
	p.mu.Lock()
	p.spec, p.fp = s, s.runFingerprint()
	p.redact = newRedactor(secretValues(s)...)
	p.mu.Unlock()
}

// setSpec stores the non-process settings (lifecycle, timeouts) and reports whether anything that
// affects the running process changed.
func (p *Proc) setSpec(s Spec) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s.runFingerprint() != p.fp {
		return true
	}
	p.spec.Lifecycle, p.spec.IdleTimeout, p.spec.StartupTimeout = s.Lifecycle, s.IdleTimeout, s.StartupTimeout
	if p.spec.AutoUpdate != s.AutoUpdate {
		p.spec.AutoUpdate, p.autoNext = s.AutoUpdate, time.Time{}
	}
	return false
}

func secretValues(s Spec) []string {
	var v []string
	for _, kv := range s.Env {
		v = append(v, kv.Value)
	}
	if s.Git != nil && s.Git.Token != "" {
		v = append(v, s.Git.Token, gitAuthValue(s.Git.Token), strings.TrimPrefix(gitAuthValue(s.Git.Token), "Authorization: Basic "))
	}
	return v
}

func (p *Proc) notifyLocked() {
	close(p.changed)
	p.changed = make(chan struct{})
}

func (p *Proc) setState(state, phase string) {
	p.mu.Lock()
	p.state, p.phase = state, phase
	if state != StateRunning {
		p.startedAt = time.Time{}
	}
	if state == StateStopped || state == StateFailed {
		p.pid = 0
	}
	p.notifyLocked()
	p.mu.Unlock()
}

func (p *Proc) logf(format string, args ...any) {
	p.m.o.Logf("managed[%s]: "+format, append([]any{p.alias}, args...)...)
}

// note records skgate's own message about the process in the ring and the log.
func (p *Proc) note(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	p.mu.Lock()
	r := p.redact
	p.mu.Unlock()
	msg = r.apply(msg)
	p.ring.Add("sys", msg)
	p.m.o.Logf("managed[%s]: %s", p.alias, msg)
}

// Logs returns the last n captured lines (all when n <= 0).
func (p *Proc) Logs(n int) []Line { return p.ring.Last(n) }

// ClearLogs empties the log buffer.
func (p *Proc) ClearLogs() { p.ring.Clear() }

// Status returns a snapshot.
func (p *Proc) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := Status{State: p.state, Phase: p.phase, PID: p.pid, Since: p.startedAt, Restarts: p.restarts,
		LastErr: p.lastErr, LastExit: p.lastExit, Sessions: len(p.sessions), Held: p.held}
	for _, pc := range p.pending {
		if !pc.internalCall {
			st.InFlight++
		}
	}
	return st
}

// Spec returns the current spec.
func (p *Proc) Spec() Spec { p.mu.Lock(); defer p.mu.Unlock(); return p.spec }

// ---- lifecycle ----

type startHint struct {
	caps  json.RawMessage
	proto string
}

// Start begins running the process (non-blocking) and clears an administrative hold. It resets the
// crash counter, so a failed process gets a fresh attempt.
func (p *Proc) Start() error { return p.start(startHint{}, true) }

func (p *Proc) start(h startHint, explicit bool) error {
	p.lifeMu.Lock()
	defer p.lifeMu.Unlock()
	p.mu.Lock()
	if p.sup != nil {
		select {
		case <-p.sup.done:
		default:
			if explicit {
				p.held = false
			}
			p.mu.Unlock()
			return nil // already starting or running
		}
	}
	if explicit {
		p.held = false
	} else if p.held {
		p.mu.Unlock()
		return errHeld
	}
	p.mu.Unlock()
	if err := p.m.acquire(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	sup := &supervisor{cancel: cancel, done: make(chan struct{})}
	p.mu.Lock()
	p.sup = sup
	p.crashes = 0
	p.state, p.phase = StateStarting, ""
	p.notifyLocked()
	p.mu.Unlock()
	go func() {
		defer p.m.release()
		p.supervise(ctx, sup.done, h)
	}()
	return nil
}

var errHeld = errors.New("stopped by an administrator; start it from the admin page")

// Stop ends the process gracefully (SIGTERM, then SIGKILL after the grace period) and waits for it.
// hold keeps requests from starting it again until Start or Restart.
func (p *Proc) Stop(hold ...bool) {
	p.lifeMu.Lock()
	defer p.lifeMu.Unlock()
	p.stopLocked(len(hold) > 0 && hold[0])
}

func (p *Proc) stopLocked(hold bool) {
	p.mu.Lock()
	sup := p.sup
	if hold {
		p.held = true
	}
	p.mu.Unlock()
	if sup != nil {
		sup.cancel()
		<-sup.done
	}
	p.mu.Lock()
	p.sup = nil
	if p.state != StateFailed || sup != nil {
		p.state, p.phase = StateStopped, ""
		p.pid, p.startedAt = 0, time.Time{}
	}
	p.notifyLocked()
	p.mu.Unlock()
}

// Restart stops and starts again, clearing failure and hold.
func (p *Proc) Restart() error {
	p.lifeMu.Lock()
	p.stopLocked(false)
	p.lifeMu.Unlock()
	return p.Start()
}

// Sync marks the next start to refresh the git checkout and rerun the install step, then restarts.
func (p *Proc) Sync() error {
	p.mu.Lock()
	p.forceSync = true
	p.mu.Unlock()
	return p.Restart()
}

// Ensure makes sure the process is running, starting it when needed, and waits until it is
// initialized (or has failed). hint carries the first client's initialize parameters.
func (p *Proc) Ensure(ctx context.Context, h startHint) error {
	for {
		p.mu.Lock()
		st, held, ch, phase := p.state, p.held, p.changed, p.phase
		lastErr, failedAt := p.lastErr, p.failedAt
		p.lastActive = time.Now()
		p.mu.Unlock()
		switch {
		case st == StateRunning:
			return nil
		case held:
			return errHeld
		case st == StateStarting && strings.HasPrefix(phase, "restarting"):
			return fmt.Errorf("process crashed and is %s: %s", phase, lastErr) // do not make callers wait out the backoff
		case st == StateFailed && time.Since(failedAt) < p.m.o.FailCooldown:
			return fmt.Errorf("process failed: %s", lastErr)
		case st == StateStopped || st == StateFailed:
			if err := p.start(h, false); err != nil {
				return err
			}
			continue
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (p *Proc) idleFor() (time.Duration, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != StateRunning || p.spec.Lifecycle == Always || len(p.pending) > 0 {
		return 0, false
	}
	idle := p.spec.IdleTimeout
	if idle <= 0 {
		idle = DefaultIdleTimeout
	}
	return time.Since(p.lastActive), time.Since(p.lastActive) >= idle
}

func (p *Proc) housekeeping() {
	if _, idle := p.idleFor(); idle {
		p.lifeMu.Lock()
		if _, still := p.idleFor(); still {
			p.note("stopping after idle timeout")
			p.stopLocked(false)
		}
		p.lifeMu.Unlock()
	}
	p.expireSessions()
	p.updateTick()
}

// ---- supervision ----

func (p *Proc) backoff(n int) time.Duration {
	d := p.m.o.BackoffBase
	for i := 1; i < n && d < p.m.o.BackoffMax; i++ {
		d *= 2
	}
	if d > p.m.o.BackoffMax {
		d = p.m.o.BackoffMax
	}
	return d
}

type fatalError struct{ error }

func (p *Proc) supervise(ctx context.Context, done chan struct{}, h startHint) {
	defer close(done)
	for {
		err := p.runOnce(ctx, h)
		if ctx.Err() != nil {
			p.setState(StateStopped, "")
			return
		}
		p.mu.Lock()
		if !p.startedAt.IsZero() && time.Since(p.startedAt) >= p.m.o.StableAfter {
			p.crashes = 0
		}
		p.crashes++
		p.lastErr, p.lastExit = err.Error(), time.Now()
		crashes := p.crashes
		var fe fatalError
		giveUp := errors.As(err, &fe) || crashes >= p.m.o.MaxCrashes
		if giveUp {
			p.state, p.phase, p.pid, p.startedAt, p.failedAt = StateFailed, "", 0, time.Time{}, time.Now()
			p.notifyLocked()
			p.mu.Unlock()
			p.note("failed: %v", err)
			return
		}
		delay := p.backoff(crashes)
		p.restarts++
		p.state, p.phase, p.pid, p.startedAt = StateStarting, "restarting in "+delay.Round(100*time.Millisecond).String(), 0, time.Time{}
		p.notifyLocked()
		p.mu.Unlock()
		p.note("exited: %v; restarting in %s (%d/%d)", err, delay.Round(10*time.Millisecond), crashes, p.m.o.MaxCrashes)
		select {
		case <-ctx.Done():
			p.setState(StateStopped, "")
			return
		case <-time.After(delay):
		}
	}
}

// dirs returns the per-alias directories, creating them.
func (p *Proc) dirs(spec Spec) (work, home, tmp string, err error) {
	base, err := p.m.AliasDir(spec.Alias)
	if err != nil {
		return
	}
	work = spec.WorkDir
	if work == "" {
		work = filepath.Join(base, "work")
		if spec.Git != nil {
			work = filepath.Join(base, "repo")
		}
	}
	home, tmp = filepath.Join(base, "home"), filepath.Join(base, "tmp")
	cache, _ := p.m.AliasCacheDir(spec.Alias)
	for _, d := range []string{work, home, tmp, cache} {
		if err = ensureDir(d); err != nil {
			return "", "", "", fmt.Errorf("creating %s: %w", filepath.Base(d), err)
		}
	}
	return
}

// childEnv is the environment of the child and of its install and git steps: the allow-listed
// parent variables, the per-alias HOME, TMPDIR and package caches, then the upstream's own env.
func (p *Proc) childEnv(spec Spec, home, tmp string) []string {
	user := spec.Env
	if cache, err := p.m.AliasCacheDir(spec.Alias); err == nil {
		user = append(CacheEnv(cache), spec.Env...)
	}
	return BuildEnv(p.m.o.Environ(), home, tmp, user)
}

// lookPath resolves a bare command name with the child's PATH, not skgate's.
func lookPath(name string, env []string, dir string) (string, error) {
	if strings.ContainsRune(name, '/') {
		if !filepath.IsAbs(name) {
			name = filepath.Join(dir, name)
		}
		return name, nil
	}
	path := ""
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			path = e[5:]
		}
	}
	for _, d := range filepath.SplitList(path) {
		if d == "" {
			continue
		}
		f := filepath.Join(d, name)
		if st, err := os.Stat(f); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return f, nil
		}
	}
	return "", fmt.Errorf("command %q not found in PATH", name)
}

func (p *Proc) runOnce(ctx context.Context, h startHint) error {
	p.mu.Lock()
	spec := p.spec
	force := p.forceSync
	p.forceSync = false
	p.mu.Unlock()
	work, home, tmp, err := p.dirs(spec)
	if err != nil {
		return fatalError{err}
	}
	env := p.childEnv(spec, home, tmp)
	if err := p.prepare(ctx, spec, work, env, force); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fatalError{err}
	}
	p.setState(StateStarting, "initializing")
	var cmd *exec.Cmd
	if spec.Shell {
		cmd = exec.Command("/bin/sh", append([]string{"-c", spec.Command, spec.Alias}, spec.Args...)...)
	} else {
		bin, err := lookPath(spec.Command, env, work)
		if err != nil {
			return fatalError{err}
		}
		cmd = exec.Command(bin, spec.Args...)
	}
	cmd.Dir, cmd.Env = work, env
	setGroup(cmd)
	cmd.WaitDelay = 2 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	c := &child{cmd: cmd, stdin: stdin, exited: make(chan struct{})}
	errW := &lineWriter{fn: func(s string) {
		s = p.redactLine(s)
		p.ring.Add("err", s)
		p.m.o.Logf("managed[%s] stderr: %s", p.alias, clipLine(s))
	}}
	cmd.Stderr = errW
	cmd.Stdout = &ndjsonWriter{p: p, c: c}
	if err := cmd.Start(); err != nil {
		return fatalError{fmt.Errorf("cannot start %q: %s", spec.Command, cleanExecErr(err))}
	}
	c.pid = cmd.Process.Pid
	p.mu.Lock()
	p.cur, p.pid, p.startedAt = c, c.pid, time.Now()
	p.mu.Unlock()
	p.logf("started pid %d", c.pid)
	go func() {
		werr := cmd.Wait()
		errW.Flush()
		c.waitErr = werr
		close(c.exited)
	}()

	initErr := p.handshake(ctx, c, spec, h)
	if initErr == nil {
		p.setState(StateRunning, "")
		p.logf("running pid %d", c.pid)
		select {
		case <-ctx.Done():
		case <-c.exited:
		}
	}
	// Make sure nothing survives: the child, then its process group.
	p.terminate(c)
	p.mu.Lock()
	p.cur = nil
	p.initResult = nil
	p.mu.Unlock()
	p.failPending("the managed process exited")
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if initErr != nil {
		return initErr
	}
	return fmt.Errorf("%s%s", exitText(c.waitErr), p.lastStderr())
}

func (p *Proc) redactLine(s string) string {
	p.mu.Lock()
	r := p.redact
	p.mu.Unlock()
	return r.apply(s)
}

func cleanExecErr(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

func exitText(err error) string {
	if err == nil {
		return "exited with status 0"
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return "killed by signal " + ws.Signal().String()
		}
		return "exited with status " + strconv.Itoa(ee.ExitCode())
	}
	return "exited: " + err.Error()
}

func (p *Proc) lastStderr() string {
	for _, l := range lastOf(p.ring.Last(20), "err") {
		return "; last stderr: " + clipText(l.Text, 200)
	}
	return ""
}

func lastOf(ls []Line, src string) []Line {
	for i := len(ls) - 1; i >= 0; i-- {
		if ls[i].Src == src {
			return ls[i : i+1]
		}
	}
	return nil
}

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// terminate stops the child: close stdin, SIGTERM the group, SIGKILL after the grace period, and
// finally make sure no group member is left.
func (p *Proc) terminate(c *child) {
	grace := p.m.o.StopGrace
	_ = c.stdin.Close()
	select {
	case <-c.exited:
	default:
		signalGroup(c.pid, false)
		select {
		case <-c.exited:
		case <-time.After(grace):
			p.note("did not stop after %s, killing", grace)
			signalGroup(c.pid, true)
			select {
			case <-c.exited:
			case <-time.After(5 * time.Second):
			}
		}
	}
	// grandchildren: give them the rest of the grace period, then kill the group
	deadline := time.Now().Add(grace)
	for groupAlive(c.pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if groupAlive(c.pid) {
		signalGroup(c.pid, true)
	}
}

// handshake performs the MCP initialize exchange with the child.
func (p *Proc) handshake(ctx context.Context, c *child, spec Spec, h startHint) error {
	proto := h.proto
	if proto == "" {
		proto = latestProtocol
	}
	caps := map[string]json.RawMessage{}
	adv := map[string]bool{}
	var clientCaps map[string]json.RawMessage
	if len(h.caps) > 0 && json.Unmarshal(h.caps, &clientCaps) == nil {
		for _, k := range []string{"roots", "sampling", "elicitation"} {
			if v, ok := clientCaps[k]; ok {
				caps[k], adv[k] = v, true
			}
		}
	}
	params, _ := json.Marshal(map[string]any{"protocolVersion": proto, "capabilities": caps,
		"clientInfo": map[string]any{"name": "skgate", "version": p.m.o.Version}})
	pc := p.newPending(nil, nil, false, true)
	msg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": pc.childID, "method": "initialize", "params": json.RawMessage(params)})
	if err := c.send(msg); err != nil {
		p.dropPending(pc)
		return fmt.Errorf("writing to the process: %s", cleanExecErr(err))
	}
	timeout := spec.startupTimeout()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case raw := <-pc.done:
		var r struct {
			Result json.RawMessage `json:"result"`
			Error  *RPCError       `json:"error"`
		}
		if json.Unmarshal(raw, &r) != nil || r.Error != nil || len(r.Result) == 0 {
			if r.Error != nil {
				if r.Error.Message == exitedMessage {
					return fmt.Errorf("%s%s", exitText(waitFor(c)), p.lastStderr())
				}
				return fmt.Errorf("initialize failed: %s", clipText(r.Error.Message, 200))
			}
			return errors.New("initialize returned no result")
		}
		note, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
		if err := c.send(note); err != nil {
			return fmt.Errorf("writing to the process: %s", cleanExecErr(err))
		}
		var si struct {
			ServerInfo struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
		}
		_ = json.Unmarshal(r.Result, &si)
		p.mu.Lock()
		p.initResult, p.advCaps = r.Result, adv
		p.serverVer = strings.TrimSpace(clipText(si.ServerInfo.Name+" "+si.ServerInfo.Version, 80))
		p.mu.Unlock()
		return nil
	case <-c.exited:
		p.dropPending(pc)
		return fmt.Errorf("%s before initialize%s", exitText(c.waitErr), p.lastStderr())
	case <-timer.C:
		p.dropPending(pc)
		return fmt.Errorf("no initialize response within %s%s", timeout.Round(time.Second), p.lastStderr())
	case <-ctx.Done():
		p.dropPending(pc)
		return ctx.Err()
	}
}

func waitFor(c *child) error {
	select {
	case <-c.exited:
		return c.waitErr
	case <-time.After(3 * time.Second):
		return nil
	}
}

// ---- stdout ----

const maxStdoutLine = 32 << 20

// ndjsonWriter splits the child's stdout into lines and dispatches them.
type ndjsonWriter struct {
	p   *Proc
	c   *child
	buf []byte
	mu  sync.Mutex
}

func (w *ndjsonWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, b...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimSpace(w.buf[:i])
		if len(line) > 0 {
			w.p.onLine(w.c, append([]byte(nil), line...))
		}
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) > maxStdoutLine {
		w.p.note("stdout line over %d MB dropped", maxStdoutLine>>20)
		w.buf = nil
	}
	return len(b), nil
}

package managed

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Kinds of git ref, as far as the remote tells.
const (
	RefBranch  = "branch"
	RefDefault = "default" // no ref configured: the remote's HEAD
	RefTag     = "tag"
	RefCommit  = "commit"
)

// RemoteInfo is the result of the last ls-remote for a git upstream.
type RemoteInfo struct {
	Checked time.Time
	Kind    string // RefBranch, RefDefault, RefTag or RefCommit
	Rev     string // commit the ref points to on the remote (full)
	Err     string // why the check failed
}

// Pinned reports whether the ref cannot move: a tag or a commit is never "ahead".
func (r RemoteInfo) Pinned() bool { return r.Kind == RefTag || r.Kind == RefCommit }

var commitRefRE = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// classifyRemote reads `git ls-remote` output for ref. A tag wins over a branch of the same name,
// as it does for `git fetch <ref>`. An annotated tag is compared by the commit it points to.
func classifyRemote(out, ref string) (kind, rev string, err error) {
	var head, branch, tag, peeled string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		sha, name := f[0], f[1]
		switch {
		case name == "HEAD":
			head = sha
		case name == "refs/heads/"+ref:
			branch = sha
		case name == "refs/tags/"+ref:
			tag = sha
		case name == "refs/tags/"+ref+"^{}":
			peeled = sha
		}
	}
	switch {
	case ref == "" && head != "":
		return RefDefault, head, nil
	case ref == "":
		return "", "", errors.New("the remote has no HEAD")
	case peeled != "":
		return RefTag, peeled, nil
	case tag != "":
		return RefTag, tag, nil
	case branch != "":
		return RefBranch, branch, nil
	case commitRefRE.MatchString(ref):
		return RefCommit, ref, nil
	}
	return "", "", fmt.Errorf("ref %q not found on the remote", ref)
}

// gitOutput runs one git command and returns its stdout. The environment is the git step
// environment (token as http.extraHeader, never in argv); errors are redacted.
func (p *Proc) gitOutput(ctx context.Context, spec Spec, timeout time.Duration, args ...string) (string, error) {
	_, home, tmp, err := p.dirs(spec)
	if err != nil {
		return "", err
	}
	base, _ := p.m.AliasDir(spec.Alias)
	env := gitEnv(p.childEnv(spec, home, tmp), spec.Git)
	bin, err := lookPath("git", env, base)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir, cmd.Env = base, env
	p.m.confine(cmd)
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &limitedBuffer{b: &stderr, max: 4096}
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = exitText(err)
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			msg = "timed out after " + timeout.Round(time.Second).String()
		}
		return "", errors.New(clipText(p.redactLine(lastLineOf(msg)), 300))
	}
	return stdout.String(), nil
}

func lastLineOf(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// limitedBuffer keeps at most max bytes.
type limitedBuffer struct {
	b   *bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := l.max - l.b.Len(); room > 0 {
		if len(p) > room {
			l.b.Write(p[:room])
		} else {
			l.b.Write(p)
		}
	}
	return len(p), nil
}

// CheckRemote asks the remote where the configured ref points (git ls-remote: one cheap request, no
// clone) and stores the result. It works before the first checkout too.
func (p *Proc) CheckRemote(ctx context.Context) (RemoteInfo, error) {
	spec := p.Spec()
	if spec.Git == nil {
		return RemoteInfo{}, errNoGit
	}
	p.mu.Lock()
	if p.checking {
		r := p.remote
		p.mu.Unlock()
		if r != nil {
			return *r, nil
		}
		return RemoteInfo{}, errors.New("a check is already running")
	}
	p.checking = true
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.checking = false; p.mu.Unlock() }()

	ref := spec.Git.Ref
	args := []string{"ls-remote", spec.Git.URL}
	if ref == "" {
		args = append(args, "HEAD")
	} else {
		args = append(args, "refs/heads/"+ref, "refs/tags/"+ref, "refs/tags/"+ref+"^{}")
	}
	info := RemoteInfo{Checked: time.Now()}
	out, err := p.gitOutput(ctx, spec, 30*time.Second, args...)
	if err == nil {
		info.Kind, info.Rev, err = classifyRemote(out, ref)
	}
	if err != nil {
		info.Err = err.Error()
	}
	p.mu.Lock()
	p.remote = &info
	p.mu.Unlock()
	return info, err
}

// UpdateInfo is what the admin UI shows about updates of one upstream.
type UpdateInfo struct {
	Git       bool
	Rev       string // installed commit, short
	RevFull   string
	Ref       string // configured ref ("" = the remote's default branch)
	RefKind   string // RefBranch, RefDefault, RefTag, RefCommit ("" until checked)
	RemoteRev string
	Available bool // the remote ref is ahead of the installed commit
	Checked   time.Time
	CheckErr  string

	Package Package // command upstreams run through a package runner
	HasPkg  bool
	Pinned  bool   // a tag, commit or exact version: never auto-updated
	PinNote string // what is pinned ("tag", "commit", "version 1.2.3")

	Err        string // last failed update
	ErrAt      time.Time
	Updating   bool // an update is running now
	LastUpdate string
	LastAt     time.Time
	AutoEvery  time.Duration
	AutoNext   time.Time
}

// UpdateInfo returns the current update state without touching the network.
func (p *Proc) UpdateInfo() UpdateInfo {
	spec := p.Spec()
	u := UpdateInfo{Git: spec.Git != nil, AutoEvery: spec.AutoUpdate}
	if spec.Git != nil {
		u.Ref = spec.Git.Ref
		u.Rev = p.Rev()
	}
	p.mu.Lock()
	u.RevFull, u.Err, u.ErrAt, u.LastUpdate, u.LastAt = p.revFull, p.updErr, p.updErrAt, p.lastUpdate, p.lastUpdateAt
	u.AutoNext = p.autoNext
	u.Updating = p.updating
	r := p.remote
	p.mu.Unlock()
	if spec.AutoUpdate == 0 {
		u.AutoNext = time.Time{}
	}
	if spec.Git != nil && r != nil {
		u.RefKind, u.RemoteRev, u.Checked, u.CheckErr = r.Kind, r.Rev, r.Checked, r.Err
		if r.Err == "" {
			u.Pinned = r.Pinned()
			if u.Pinned {
				u.PinNote = r.Kind
			}
			u.Available = !r.Pinned() && u.RevFull != "" && r.Rev != u.RevFull
		}
	}
	if pkg, ok := PackageOf(spec); ok {
		u.Package, u.HasPkg = pkg, true
		if pkg.Pinned {
			u.Pinned, u.PinNote = true, "version "+pkg.Version
		}
	}
	return u
}

var errUpdating = errors.New("an update is already running")

// updateTick runs the periodic remote check and the opt-in auto-update. It never blocks the janitor.
func (p *Proc) updateTick() {
	spec := p.Spec()
	now := time.Now()
	p.mu.Lock()
	held, busy := p.held, p.checking
	r := p.remote
	pending := len(p.pending)
	due := false
	if spec.AutoUpdate > 0 {
		if p.autoNext.IsZero() {
			p.autoNext = now.Add(spec.AutoUpdate)
		}
		due = !now.Before(p.autoNext)
	}
	p.mu.Unlock()

	if every := p.m.o.CheckEvery; spec.Git != nil && every > 0 && !busy && (r == nil || now.Sub(r.Checked) >= every) {
		p.m.bg(func(ctx context.Context) { _, _ = p.CheckRemote(ctx) })
	}
	if !due || held || pending > 0 {
		return
	}
	p.m.bg(func(ctx context.Context) {
		if !p.updMu.TryLock() {
			return
		}
		p.updMu.Unlock()
		p.autoUpdate(ctx)
	})
}

// autoUpdate applies an update when the upstream follows a moving target and has one available.
func (p *Proc) autoUpdate(ctx context.Context) {
	spec := p.Spec()
	if spec.AutoUpdate <= 0 {
		return
	}
	p.mu.Lock()
	p.autoNext = time.Now().Add(spec.AutoUpdate)
	p.mu.Unlock()
	if spec.Git != nil {
		r, err := p.CheckRemote(ctx)
		if err != nil || r.Pinned() {
			return
		}
		u := p.UpdateInfo()
		if !u.Available {
			return
		}
	} else if pkg, ok := PackageOf(spec); !ok || pkg.Pinned {
		return
	}
	if err := p.update(ctx, true); err != nil && !errors.Is(err, errUpdating) {
		p.logf("auto-update failed, keeping the previous version: %v", err)
	}
}

// Update applies the newest version and restarts a running process: a git upstream fetches its
// ref, a command upstream clears its package cache so the runner resolves the package again. When
// the new version cannot be installed or does not start, the previous one is restored and the
// error is kept for the admin UI (UpdateInfo.Err).
func (p *Proc) Update() error { return p.update(p.m.ctx, false) }

// UpdateAsync runs Update in the background; the outcome shows in UpdateInfo. Updating is true
// before this returns, so the list can show progress on the next read.
func (p *Proc) UpdateAsync() error {
	if !p.beginUpdate() {
		return errUpdating
	}
	if !p.m.bg(func(ctx context.Context) {
		defer p.endUpdate()
		_ = p.updateBody(ctx, false)
	}) {
		p.endUpdate()
		return errors.New("shutting down")
	}
	return nil
}

// beginUpdate marks an update as running and holds updMu until endUpdate.
func (p *Proc) beginUpdate() bool {
	if !p.updMu.TryLock() {
		return false
	}
	p.mu.Lock()
	p.updating = true
	p.mu.Unlock()
	return true
}

func (p *Proc) endUpdate() {
	p.mu.Lock()
	p.updating = false
	p.mu.Unlock()
	p.updMu.Unlock()
}

func (p *Proc) update(ctx context.Context, auto bool) error {
	if !p.beginUpdate() {
		return errUpdating
	}
	defer p.endUpdate()
	return p.updateBody(ctx, auto)
}

func (p *Proc) updateBody(ctx context.Context, auto bool) (err error) {
	spec := p.Spec()
	tag := "update"
	if auto {
		tag = "update auto"
	}
	p.mu.Lock()
	oldFull, oldVer := p.revFull, p.serverVer
	p.mu.Unlock()
	if spec.Git != nil {
		oldFull = p.Revision()
	}
	newFull, newVer := "", ""
	defer func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if err != nil {
			p.updErr, p.updErrAt = err.Error(), time.Now()
			return
		}
		p.updErr = ""
		p.lastUpdateAt = time.Now()
		if spec.Git != nil {
			p.lastUpdate = shortRev(oldFull) + " → " + shortRev(newFull)
		} else {
			p.lastUpdate = "cache cleared"
		}
	}()
	fail := func(e error) error {
		e = errors.New(p.redactLine(e.Error()))
		p.logf("update failed: %v", e)
		return e
	}
	if spec.Git != nil {
		newFull, err = p.updateGit(ctx, spec, oldFull)
		if err != nil {
			return fail(err)
		}
		p.logf("%s old=%s new=%s", tag, dash(shortRev(oldFull)), dash(shortRev(newFull)))
		return nil
	}
	newVer, err = p.updateCommand(ctx, spec, oldVer)
	if err != nil {
		return fail(err)
	}
	p.logf("%s old=%s new=%s", tag, dash(oldVer), dash(newVer))
	return nil
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return strings.ReplaceAll(s, " ", "_")
}

// Revision returns the installed commit of a git upstream in full ("" when not cloned).
func (p *Proc) Revision() string {
	p.Rev()
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.revFull
}

// runState reports whether the process should be running again after an update: it is up, or is
// starting, or failed (an update is the natural repair). An administrator's stop stays a stop.
func (p *Proc) runState() (wasUp bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.held {
		return false
	}
	return p.sup != nil || p.state == StateFailed
}

// startAndWait starts the process and waits until it is initialized. It reports why it did not.
func (p *Proc) startAndWait(ctx context.Context, spec Spec) error {
	if err := p.Start(); err != nil {
		return err
	}
	limit := time.NewTimer(spec.startupTimeout() + p.m.o.InstallMax + 10*time.Second)
	defer limit.Stop()
	for {
		p.mu.Lock()
		st, phase, ch, le := p.state, p.phase, p.changed, p.lastErr
		p.mu.Unlock()
		switch {
		case st == StateRunning:
			return nil
		case st == StateFailed:
			return errors.New(le)
		case st == StateStarting && strings.HasPrefix(phase, "restarting"):
			return fmt.Errorf("process crashed: %s", le)
		case st == StateStopped:
			return errors.New("the process stopped")
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		case <-limit.C:
			return errors.New("timed out waiting for the process to start")
		}
	}
}

// updateGit fetches the ref while the old version keeps running, then swaps the checkout.
func (p *Proc) updateGit(ctx context.Context, spec Spec, old string) (string, error) {
	work, home, tmp, err := p.dirs(spec)
	if err != nil {
		return "", err
	}
	base, _ := p.m.AliasDir(spec.Alias)
	up := p.runState()
	if _, e := os.Stat(filepath.Join(work, ".git")); e != nil || old == "" {
		// nothing installed yet: a regular first sync
		if !up {
			return "", errors.New("not installed yet; start the upstream first")
		}
		p.mu.Lock()
		p.forceSync = true
		p.mu.Unlock()
		if err := p.Restart(); err != nil {
			return "", err
		}
		if e := p.startAndWait(ctx, spec); e != nil {
			return "", e
		}
		return p.Revision(), nil
	}
	env := gitEnv(p.childEnv(spec, home, tmp), spec.Git)
	git := func(args ...string) error { return p.runStep(ctx, "git", work, env, "git", args...) }
	p.note("git: fetching")
	if err := git("remote", "set-url", "origin", spec.Git.URL); err != nil {
		return "", fmt.Errorf("git remote set-url: %w", err)
	}
	ref := spec.Git.Ref
	if ref == "" {
		ref = "HEAD"
	}
	if err := git("fetch", "--depth", "1", "--no-tags", "--quiet", "origin", ref); err != nil {
		return "", fmt.Errorf("git fetch: %w", err)
	}
	// From here the running version is replaced; any failure puts the old commit back.
	p.lifeMu.Lock()
	p.stopLocked(false)
	p.lifeMu.Unlock()
	restore := func(cause error) (string, error) {
		p.note("update failed, restoring %s", shortRev(old))
		p.lifeMu.Lock()
		p.stopLocked(false)
		p.lifeMu.Unlock()
		var msgs []string
		if e := git("checkout", "--quiet", "--force", "--detach", old); e != nil {
			msgs = append(msgs, "restoring the previous commit failed: "+e.Error())
		} else {
			_ = git("clean", "-fdq")
			p.setRev(work)
			if e := p.install(ctx, spec, work, p.childEnv(spec, home, tmp), true); e != nil {
				msgs = append(msgs, "reinstalling the previous version failed: "+e.Error())
			}
		}
		if !up {
			p.setState(StateStopped, "")
		}
		if up {
			if e := p.startAndWait(ctx, spec); e != nil {
				msgs = append(msgs, "the previous version does not start: "+e.Error())
			}
		}
		if len(msgs) > 0 {
			return "", fmt.Errorf("%v; %s", cause, strings.Join(msgs, "; "))
		}
		return "", fmt.Errorf("%v; the previous version %s is kept", cause, shortRev(old))
	}
	if err := git("checkout", "--quiet", "--force", "--detach", "FETCH_HEAD"); err != nil {
		return restore(fmt.Errorf("git checkout: %w", err))
	}
	_ = git("clean", "-fdq")
	_ = os.WriteFile(filepath.Join(base, "source"), []byte(p.sourceStamp(spec)), 0o600)
	p.setRev(work)
	if err := p.install(ctx, spec, work, p.childEnv(spec, home, tmp), true); err != nil {
		return restore(err)
	}
	if !up {
		p.setState(StateStopped, "")
	}
	if up {
		if err := p.startAndWait(ctx, spec); err != nil {
			return restore(err)
		}
	}
	return p.Revision(), nil
}

// updateCommand clears the upstream's own package cache and restarts it. The old cache is kept
// aside until the new start works, and put back when it does not. The upstream's TMPDIR goes the
// same way, because bunx installs the packages it runs there rather than in a cache.
func (p *Proc) updateCommand(ctx context.Context, spec Spec, oldVer string) (string, error) {
	cache, err := p.m.AliasCacheDir(spec.Alias)
	if err != nil {
		return "", err
	}
	base, err := p.m.AliasDir(spec.Alias)
	if err != nil {
		return "", err
	}
	tmp := filepath.Join(base, "tmp")
	up := p.runState()
	p.lifeMu.Lock()
	p.stopLocked(false)
	p.lifeMu.Unlock()
	aside := filepath.Join(filepath.Dir(cache), ".old-"+spec.Alias)
	tmpAside := filepath.Join(base, ".old-tmp")
	_ = p.m.removeAll(aside)
	_ = p.m.removeAll(tmpAside)
	if _, e := os.Stat(cache); e == nil {
		if err := os.Rename(cache, aside); err != nil {
			return "", fmt.Errorf("clearing the package cache: %w", err)
		}
	}
	if _, e := os.Stat(tmp); e == nil {
		_ = os.Rename(tmp, tmpAside) // best effort: a stale TMPDIR only delays a bunx update
	}
	p.note("package cache cleared")
	p.mu.Lock()
	p.forceSync = true // the install step, if any, runs again
	p.mu.Unlock()
	if err := p.m.handOver(cache); err != nil {
		return "", err
	}
	if !up {
		_ = p.m.removeAll(aside)
		_ = p.m.removeAll(tmpAside)
		return "", nil
	}
	if err := p.startAndWait(ctx, spec); err != nil {
		p.lifeMu.Lock()
		p.stopLocked(false)
		p.lifeMu.Unlock()
		msg := ""
		if _, e := os.Stat(aside); e == nil {
			_ = p.m.removeAll(cache)
			if e := os.Rename(aside, cache); e != nil {
				msg = "; restoring the package cache failed: " + e.Error()
			}
		}
		if _, e := os.Stat(tmpAside); e == nil {
			_ = p.m.removeAll(tmp)
			_ = os.Rename(tmpAside, tmp)
		}
		if e := p.startAndWait(ctx, spec); e != nil {
			return "", fmt.Errorf("%v; the previous version does not start: %v%s", err, e, msg)
		}
		return "", fmt.Errorf("%v; the previous version is kept%s", err, msg)
	}
	_ = p.m.removeAll(tmpAside)
	_ = p.m.removeAll(aside)
	p.mu.Lock()
	v := p.serverVer
	p.mu.Unlock()
	return v, nil
}

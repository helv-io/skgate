package managed

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClassifyRemote(t *testing.T) {
	const a, b, c = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccccccccccccccccccccccc"
	for _, tc := range []struct {
		name, out, ref, kind, rev string
		err                       bool
	}{
		{"default", a + "\tHEAD\n", "", RefDefault, a, false},
		{"default missing", "", "", "", "", true},
		{"branch", b + "\trefs/heads/main\n", "main", RefBranch, b, false},
		{"tag", b + "\trefs/tags/v1\n", "v1", RefTag, b, false},
		{"annotated tag is peeled", b + "\trefs/tags/v1\n" + c + "\trefs/tags/v1^{}\n", "v1", RefTag, c, false},
		{"tag wins over branch", a + "\trefs/heads/x\n" + b + "\trefs/tags/x\n", "x", RefTag, b, false},
		{"commit", "", "0123456789abcdef", RefCommit, "0123456789abcdef", false},
		{"missing", "", "nope", "", "", true},
	} {
		kind, rev, err := classifyRemote(tc.out, tc.ref)
		if (err != nil) != tc.err || kind != tc.kind || rev != tc.rev {
			t.Errorf("%s: got %q %q %v", tc.name, kind, rev, err)
		}
	}
}

func upFor(t *testing.T, p *Proc) UpdateInfo { t.Helper(); return p.UpdateInfo() }

func TestCheckRemoteShowsAheadAndTagsArePinned(t *testing.T) {
	f := newRepo(t)
	f.commit("version.txt", "v1\n", "one")
	run(t, f.wc, "git", "tag", "v1.0.0")
	run(t, f.wc, "git", "push", "-q", "origin", "v1.0.0")
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(gitSpec("g", f, "main"))
	initSession(t, serve(t, p).URL, nil)
	if u := upFor(t, p); len(u.Rev) != 12 || u.Available || u.Ref != "main" {
		t.Fatalf("installed rev is shown before any check: %+v", u)
	}
	ctx := context.Background()
	if _, err := p.CheckRemote(ctx); err != nil {
		t.Fatal(err)
	}
	if u := upFor(t, p); u.Available || u.RefKind != RefBranch || u.Pinned {
		t.Fatalf("up to date: %+v", u)
	}
	f.commit("version.txt", "v2\n", "two")
	p.CheckRemote(ctx)
	u := upFor(t, p)
	if !u.Available || u.RemoteRev == u.RevFull || u.RemoteRev == "" {
		t.Fatalf("ahead: %+v", u)
	}

	// a tag never moves: pinned, never "ahead", even when the remote differs from the checkout
	pt, _ := m.Proc(gitSpec("t", f, "v1.0.0"))
	initSession(t, serve(t, pt).URL, nil)
	f.commit("version.txt", "v3\n", "three")
	pt.CheckRemote(ctx)
	if u := upFor(t, pt); u.Available || !u.Pinned || u.RefKind != RefTag || u.PinNote != RefTag {
		t.Fatalf("tag: %+v", u)
	}
	// a commit id is pinned as well
	sha := run(t, f.wc, "git", "rev-parse", "HEAD~1")
	pc, _ := m.Proc(gitSpec("c", f, sha))
	pc.CheckRemote(ctx)
	if u := upFor(t, pc); u.Available || !u.Pinned || u.RefKind != RefCommit {
		t.Fatalf("commit: %+v", u)
	}
	// an unreachable remote is an error on the info, not a panic or a stale "available"
	bad := gitSpec("bad", f, "main")
	bad.Git.URL = "file://" + filepath.Join(t.TempDir(), "gone.git")
	pb, _ := m.Proc(bad)
	if _, err := pb.CheckRemote(ctx); err == nil || upFor(t, pb).CheckErr == "" || upFor(t, pb).Available {
		t.Fatalf("unreachable: %v %+v", err, upFor(t, pb))
	}
}

func TestPeriodicCheckRunsInTheBackground(t *testing.T) {
	f := newRepo(t)
	f.commit("version.txt", "v1\n", "one")
	m, _ := testMgr(t, func(o *Options) { o.CheckEvery = 50 * time.Millisecond })
	p, _ := m.Proc(gitSpec("g", f, "main"))
	waitFor2(t, "first check", 10*time.Second, func() bool { return !upFor(t, p).Checked.IsZero() })
}

func TestUpdateGitAppliesAndLogsOldAndNew(t *testing.T) {
	f := newRepo(t)
	f.commit("version.txt", "v1\n", "one")
	m, ls := testMgr(t, nil)
	p, _ := m.Proc(gitSpec("g", f, "main"))
	initSession(t, serve(t, p).URL, nil)
	old := p.Rev()
	f.commit("version.txt", "v2\n", "two")
	if err := p.Update(); err != nil {
		t.Fatal(err)
	}
	if p.Status().State != StateRunning {
		t.Fatalf("%+v", p.Status())
	}
	u := upFor(t, p)
	if u.Rev == old || u.Err != "" || !strings.HasPrefix(u.LastUpdate, old+" → "+u.Rev) {
		t.Fatalf("%+v", u)
	}
	if !strings.Contains(ls.String(), "managed[g]: update old="+old+" new="+u.Rev) {
		t.Fatalf("log:\n%s", ls.String())
	}
}

func TestUpdateGitFailureKeepsPreviousVersionRunning(t *testing.T) {
	f := newRepo(t)
	f.commit("version.txt", "v1\n", "one")
	m, ls := testMgr(t, nil)
	s := gitSpec("g", f, "main")
	// the install step fails once version.txt says "bad"
	s.Install = `grep -q bad version.txt && { echo "install broke"; exit 3; }; cat version.txt >> ../install.log`
	p, _ := m.Proc(s)
	ts := serve(t, p)
	sid := initSession(t, ts.URL, nil)
	old := p.Rev()
	f.commit("version.txt", "bad\n", "two")
	err := p.Update()
	if err == nil || !strings.Contains(err.Error(), "install broke") || !strings.Contains(err.Error(), old) {
		t.Fatalf("update error: %v", err)
	}
	base, _ := m.AliasDir("g")
	if b, _ := os.ReadFile(filepath.Join(base, "repo", "version.txt")); string(b) != "v1\n" {
		t.Fatalf("checkout was not restored: %q", b)
	}
	if p.Status().State != StateRunning || p.Rev() != old {
		t.Fatalf("the previous version must keep running: %+v rev=%s", p.Status(), p.Rev())
	}
	if out := callTool(t, ts.URL, sid, "env", nil); out == "" {
		t.Fatal("no answer")
	}
	u := upFor(t, p)
	if !strings.Contains(u.Err, "install broke") || u.LastUpdate != "" {
		t.Fatalf("%+v", u)
	}
	if !strings.Contains(ls.String(), "managed[g]: update failed:") || strings.Contains(ls.String(), "update old=") {
		t.Fatalf("log:\n%s", ls.String())
	}
	// fixing the repository and updating again clears the error
	f.commit("version.txt", "v3\n", "three")
	if err := p.Update(); err != nil {
		t.Fatal(err)
	}
	if u := upFor(t, p); u.Err != "" || u.LastUpdate == "" {
		t.Fatalf("%+v", u)
	}
}

func TestUpdateGitFetchFailureDoesNotTouchTheRunningProcess(t *testing.T) {
	f := newRepo(t)
	f.commit("version.txt", "v1\n", "one")
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(gitSpec("g", f, "main"))
	initSession(t, serve(t, p).URL, nil)
	pid := p.Status().PID
	run(t, f.bare, "git", "config", "receive.denyDeleteCurrent", "ignore")
	run(t, f.wc, "git", "push", "-q", "origin", "--delete", "main") // remote loses the branch (bare HEAD stays)
	if err := p.Update(); err == nil || !strings.Contains(err.Error(), "git fetch") {
		t.Fatalf("%v", err)
	}
	if st := p.Status(); st.State != StateRunning || st.PID != pid {
		t.Fatalf("a failed fetch must not restart: %+v", st)
	}
}

func TestUpdateStoppedUpstreamDoesNotStartIt(t *testing.T) {
	f := newRepo(t)
	f.commit("version.txt", "v1\n", "one")
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(gitSpec("g", f, "main"))
	initSession(t, serve(t, p).URL, nil)
	old := p.Rev()
	p.Stop(true)
	f.commit("version.txt", "v2\n", "two")
	if err := p.Update(); err != nil {
		t.Fatal(err)
	}
	if p.Status().State != StateStopped || p.Rev() == old {
		t.Fatalf("%+v rev %s (was %s)", p.Status(), p.Rev(), old)
	}
}

// stubRunner puts an `npx` in PATH that runs the fake server.
func stubRunner(t *testing.T, cmd string) func(*Options) {
	dir := t.TempDir()
	script := "#!/bin/sh\n" + cmd + "\nexec \"" + os.Args[0] + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "npx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return func(o *Options) { o.Environ = func() []string { return []string{"PATH=" + dir + ":/usr/bin:/bin"} } }
}

func pkgSpec(alias string, pkg string) Spec {
	s := fakeSpec(alias)
	s.Command, s.Args = "npx", []string{"-y", pkg}
	return s
}

func TestUpdateCommandClearsOnlyItsOwnCacheAndRestarts(t *testing.T) {
	// the runner records the cache dir it was given; the second start sees an empty one
	m, _ := testMgr(t, stubRunner(t, `touch "$(dirname "$NPM_CONFIG_CACHE")/started"`))
	a, _ := m.Proc(pkgSpec("a", "pkg-a"))
	b, _ := m.Proc(pkgSpec("b", "pkg-b"))
	for _, p := range []*Proc{a, b} {
		initSession(t, serve(t, p).URL, nil)
	}
	ca, _ := m.AliasCacheDir("a")
	cb, _ := m.AliasCacheDir("b")
	os.WriteFile(filepath.Join(ca, "marker"), nil, 0o600)
	os.WriteFile(filepath.Join(cb, "marker"), nil, 0o600)
	pid := a.Status().PID
	if err := a.Update(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ca, "marker")); err == nil {
		t.Fatal("a's cache must be cleared")
	}
	if _, err := os.Stat(filepath.Join(cb, "marker")); err != nil {
		t.Fatal("b's cache must be untouched")
	}
	if st := a.Status(); st.State != StateRunning || st.PID == pid {
		t.Fatalf("a must have restarted: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(ca, "started")); err != nil {
		t.Fatal("the restarted process must use the recreated cache")
	}
	if u := upFor(t, a); u.Err != "" || u.LastUpdate != "cache cleared" || !u.HasPkg || u.Package.Name != "pkg-a" || u.Pinned {
		t.Fatalf("%+v", u)
	}
	if b.Status().State != StateRunning {
		t.Fatal("b must keep running")
	}
	// a plain command without a package runner clears its (empty) cache and restarts as well
	q, _ := m.Proc(fakeSpec("plain"))
	initSession(t, serve(t, q).URL, nil)
	if err := q.Update(); err != nil || q.Status().State != StateRunning {
		t.Fatalf("%v %+v", err, q.Status())
	}
}

func TestUpdateCommandFailureRestoresTheCache(t *testing.T) {
	// the runner refuses to start once the cache is empty
	m, _ := testMgr(t, stubRunner(t, `test -f "$(dirname "$NPM_CONFIG_CACHE")/marker" || { echo "cannot resolve pkg" >&2; exit 4; }`))
	p, _ := m.Proc(pkgSpec("a", "pkg-a"))
	ca, _ := m.AliasCacheDir("a")
	os.MkdirAll(ca, 0o700)
	os.WriteFile(filepath.Join(ca, "marker"), nil, 0o600)
	initSession(t, serve(t, p).URL, nil)
	err := p.Update()
	if err == nil || !strings.Contains(err.Error(), "cannot resolve pkg") || !strings.Contains(err.Error(), "previous version is kept") {
		t.Fatalf("%v", err)
	}
	if _, e := os.Stat(filepath.Join(ca, "marker")); e != nil {
		t.Fatal("the old cache must be back")
	}
	if p.Status().State != StateRunning || !strings.Contains(upFor(t, p).Err, "cannot resolve pkg") {
		t.Fatalf("%+v %+v", p.Status(), upFor(t, p))
	}
}

func TestAutoUpdate(t *testing.T) {
	quick := func(o *Options) { o.CheckEvery = 30 * time.Millisecond }
	t.Run("git branch follows the remote and logs old and new", func(t *testing.T) {
		f := newRepo(t)
		f.commit("version.txt", "v1\n", "one")
		m, ls := testMgr(t, quick)
		s := gitSpec("g", f, "main")
		s.AutoUpdate = MinAutoUpdate
		p, _ := m.Proc(s)
		initSession(t, serve(t, p).URL, nil)
		old := p.Rev()
		// shorten the interval without going through Validate
		p.mu.Lock()
		p.spec.AutoUpdate = 50 * time.Millisecond
		p.autoNext = time.Now()
		p.mu.Unlock()
		f.commit("version.txt", "v2\n", "two")
		waitFor2(t, "auto update", 15*time.Second, func() bool { return p.Rev() != old && p.Status().State == StateRunning })
		waitFor2(t, "log", 5*time.Second, func() bool { return strings.Contains(ls.String(), "managed[g]: update auto old="+old+" new="+p.Rev()) })
	})
	t.Run("off by default", func(t *testing.T) {
		f := newRepo(t)
		f.commit("version.txt", "v1\n", "one")
		m, _ := testMgr(t, quick)
		p, _ := m.Proc(gitSpec("g", f, "main"))
		initSession(t, serve(t, p).URL, nil)
		old := p.Rev()
		f.commit("version.txt", "v2\n", "two")
		waitFor2(t, "check", 10*time.Second, func() bool { return upFor(t, p).Available })
		time.Sleep(300 * time.Millisecond)
		if p.Rev() != old {
			t.Fatal("updated without opt-in")
		}
	})
	t.Run("tags and commits are never auto-updated", func(t *testing.T) {
		f := newRepo(t)
		f.commit("version.txt", "v1\n", "one")
		run(t, f.wc, "git", "tag", "v1.0.0")
		run(t, f.wc, "git", "push", "-q", "origin", "v1.0.0")
		m, ls := testMgr(t, quick)
		s := gitSpec("g", f, "v1.0.0")
		p, _ := m.Proc(s)
		initSession(t, serve(t, p).URL, nil)
		old := p.Rev()
		p.mu.Lock()
		p.spec.AutoUpdate = 50 * time.Millisecond
		p.autoNext = time.Now()
		p.mu.Unlock()
		// the tag is moved on the remote; it must still not be followed
		f.commit("version.txt", "v2\n", "two")
		run(t, f.wc, "git", "tag", "-f", "v1.0.0")
		run(t, f.wc, "git", "push", "-q", "-f", "origin", "v1.0.0")
		waitFor2(t, "check", 10*time.Second, func() bool { return !upFor(t, p).Checked.IsZero() })
		time.Sleep(400 * time.Millisecond)
		if p.Rev() != old || strings.Contains(ls.String(), "update") {
			t.Fatalf("a tag was auto-updated:\n%s", ls.String())
		}
	})
	t.Run("a failing auto-update keeps the old version and surfaces the error", func(t *testing.T) {
		f := newRepo(t)
		f.commit("version.txt", "v1\n", "one")
		m, ls := testMgr(t, quick)
		s := gitSpec("g", f, "main")
		s.Install = `grep -q bad version.txt && { echo "install broke"; exit 3; }; true`
		p, _ := m.Proc(s)
		initSession(t, serve(t, p).URL, nil)
		old := p.Rev()
		p.mu.Lock()
		p.spec.AutoUpdate = 50 * time.Millisecond
		p.autoNext = time.Now()
		p.mu.Unlock()
		f.commit("version.txt", "bad\n", "two")
		waitFor2(t, "error", 15*time.Second, func() bool { return strings.Contains(upFor(t, p).Err, "install broke") })
		if p.Rev() != old || p.Status().State != StateRunning {
			t.Fatalf("%s %+v", p.Rev(), p.Status())
		}
		if !strings.Contains(ls.String(), "auto-update failed, keeping the previous version") {
			t.Fatalf("log:\n%s", ls.String())
		}
	})
	t.Run("pinned and unknown commands are skipped, unpinned packages update", func(t *testing.T) {
		m, ls := testMgr(t, stubRunner(t, `true`))
		mk := func(alias, pkg string) *Proc {
			s := pkgSpec(alias, pkg)
			p, _ := m.Proc(s)
			initSession(t, serve(t, p).URL, nil)
			p.mu.Lock()
			p.spec.AutoUpdate = 50 * time.Millisecond
			p.autoNext = time.Now()
			p.mu.Unlock()
			return p
		}
		pin := mk("pin", "pkg@1.2.3")
		pid := pin.Status().PID
		free := mk("free", "pkg")
		fpid := free.Status().PID
		// the "update auto" line is logged once the restart is done, after the new pid already shows
		waitFor2(t, "unpinned updated", 15*time.Second, func() bool {
			return free.Status().PID != fpid && free.Status().State == StateRunning && strings.Contains(ls.String(), "managed[free]: update auto")
		})
		if pin.Status().PID != pid {
			t.Fatal("a pinned version was auto-updated")
		}
		if !strings.Contains(ls.String(), "managed[free]: update auto") || strings.Contains(ls.String(), "managed[pin]: update") {
			t.Fatalf("log:\n%s", ls.String())
		}
		plain, _ := m.Proc(fakeSpec("plain"))
		initSession(t, serve(t, plain).URL, nil)
		ppid := plain.Status().PID
		plain.mu.Lock()
		plain.spec.AutoUpdate = 50 * time.Millisecond
		plain.autoNext = time.Now()
		plain.mu.Unlock()
		time.Sleep(400 * time.Millisecond)
		if plain.Status().PID != ppid {
			t.Fatal("an unrecognized command has nothing to update")
		}
	})
}

func TestAutoUpdateSpecValidation(t *testing.T) {
	s := fakeSpec("a")
	for d, ok := range map[time.Duration]bool{0: true, time.Minute: false, MinAutoUpdate: true, 24 * time.Hour: true, 400 * 24 * time.Hour: false, -time.Second: false} {
		s.AutoUpdate = d
		if (s.Validate() == nil) != ok {
			t.Errorf("%v: valid=%v", d, s.Validate() == nil)
		}
	}
}

func TestAutoUpdateDoesNotRestartTheProcessWhenChanged(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	initSession(t, serve(t, p).URL, nil)
	pid := p.Status().PID
	s := fakeSpec("a")
	s.AutoUpdate = time.Hour
	p2, _ := m.Proc(s)
	if p2 != p || p.Status().PID != pid || p.Spec().AutoUpdate != time.Hour {
		t.Fatal("changing auto-update must not replace the process")
	}
}

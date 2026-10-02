package managed

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOnDemandStartsOnFirstRequestAndStopsWhenIdle(t *testing.T) {
	m, _ := testMgr(t, nil)
	s := fakeSpec("a")
	s.IdleTimeout = 300 * time.Millisecond
	p, _ := m.Proc(s)
	if st := p.Status(); st.State != StateStopped || st.PID != 0 {
		t.Fatalf("must not run before the first request: %+v", st)
	}
	ts := serve(t, p)
	sid := initSession(t, ts.URL, nil)
	pid := p.Status().PID
	if pid == 0 || p.Status().State != StateRunning {
		t.Fatalf("%+v", p.Status())
	}
	waitFor2(t, "idle stop", 4*time.Second, func() bool { return p.Status().State == StateStopped })
	if groupAlive(pid) {
		t.Fatal("process group still alive after idle stop")
	}
	// the next request starts it again; the old client session survives (state is per process
	// lifetime, MCP servers are expected to be stateless across sessions here)
	if got := callTool(t, ts.URL, sid, "echo", map[string]any{"text": "again"}); got != "again" {
		t.Fatal(got)
	}
	if p.Status().PID == pid || p.Status().PID == 0 {
		t.Fatalf("expected a new pid, got %d (old %d)", p.Status().PID, pid)
	}
}

func TestActivityKeepsOnDemandProcessAlive(t *testing.T) {
	m, _ := testMgr(t, nil)
	s := fakeSpec("a")
	s.IdleTimeout = 500 * time.Millisecond
	p, _ := m.Proc(s)
	ts := serve(t, p)
	sid := initSession(t, ts.URL, nil)
	pid := p.Status().PID
	for i := 0; i < 8; i++ {
		time.Sleep(150 * time.Millisecond)
		callTool(t, ts.URL, sid, "echo", map[string]any{"text": "x"})
	}
	if p.Status().PID != pid {
		t.Fatal("process was stopped while in use")
	}
}

func TestAlwaysOnStartsAtBootAndNeverIdles(t *testing.T) {
	m, _ := testMgr(t, nil)
	s := fakeSpec("a")
	s.Lifecycle, s.IdleTimeout = Always, 50*time.Millisecond
	m.StartAlways([]Spec{s, fakeSpec("b")})
	if m.Lookup("b") != nil {
		t.Fatal("on-demand spec must not be started at boot")
	}
	p := m.Lookup("a")
	waitFor2(t, "always-on running", 5*time.Second, func() bool { return p.Status().State == StateRunning })
	time.Sleep(400 * time.Millisecond)
	if p.Status().State != StateRunning {
		t.Fatal("always-on process idled out")
	}
}

func TestCrashRestartsWithBackoffAndRecovers(t *testing.T) {
	m, ls := testMgr(t, nil)
	s := fakeSpec("a")
	s.Lifecycle = Always
	p, _ := m.Proc(s)
	p.Start()
	waitFor2(t, "running", 5*time.Second, func() bool { return p.Status().State == StateRunning })
	pid := p.Status().PID
	ts := serve(t, p)
	sid := initSession(t, ts.URL, nil)
	// the crash tool kills the child mid call: the client gets a JSON-RPC error, not a hang
	r := post(t, ts.URL, sid, "application/json", rpc(1, "tools/call", map[string]any{"name": "crash"}))
	if r.Status != 200 || r.Events[0].Error == nil {
		t.Fatalf("in-flight call at crash: %d %s", r.Status, r.Body)
	}
	waitFor2(t, "restarted", 5*time.Second, func() bool {
		st := p.Status()
		return st.State == StateRunning && st.PID != pid
	})
	if p.Status().Restarts != 1 {
		t.Fatalf("restarts %d", p.Status().Restarts)
	}
	if got := callTool(t, ts.URL, sid, "echo", map[string]any{"text": "back"}); got != "back" {
		t.Fatal(got)
	}
	if !strings.Contains(ls.String(), "restarting in") {
		t.Fatalf("restart not logged:\n%s", ls.String())
	}
}

func TestBackoffDoublesAndIsCapped(t *testing.T) {
	m, _ := testMgr(t, func(o *Options) { o.BackoffBase, o.BackoffMax = time.Second, 5*time.Second })
	p := newProc(m, fakeSpec("a"))
	want := []time.Duration{1, 2, 4, 5, 5}
	for i, w := range want {
		if got := p.backoff(i + 1); got != w*time.Second {
			t.Errorf("backoff(%d)=%s want %s", i+1, got, w*time.Second)
		}
	}
}

func TestCrashLoopMarksFailedWithLastError(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a", "FAKE_CRASH_ON_START=1"))
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor2(t, "failed", 8*time.Second, func() bool { return p.Status().State == StateFailed })
	st := p.Status()
	if !strings.Contains(st.LastErr, "status 4") || !strings.Contains(st.LastErr, "crashing on start") {
		t.Fatalf("last error %q", st.LastErr)
	}
	if st.Restarts < 2 || st.PID != 0 {
		t.Fatalf("%+v", st)
	}
	// during the cooldown a request fails fast instead of respawning
	ts := serve(t, p)
	r := post(t, ts.URL, "", "application/json", rpc(1, "tools/list", nil))
	if r.Status != 502 || !strings.Contains(string(r.Body), "failed") {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	// an explicit Start gives it a fresh attempt
	p.Start()
	waitFor2(t, "failed again", 8*time.Second, func() bool { return p.Status().State == StateFailed })
}

func TestMissingCommandFailsImmediately(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(Spec{Alias: "a", Command: "definitely-not-installed-xyz"})
	p.Start()
	waitFor2(t, "failed", 3*time.Second, func() bool { return p.Status().State == StateFailed })
	if !strings.Contains(p.Status().LastErr, "not found") || p.Status().Restarts != 0 {
		t.Fatalf("%+v", p.Status())
	}
}

func TestStartupTimeout(t *testing.T) {
	m, _ := testMgr(t, func(o *Options) { o.MaxCrashes = 1 })
	s := fakeSpec("a", "FAKE_NO_INIT=1")
	s.StartupTimeout = 300 * time.Millisecond
	p, _ := m.Proc(s)
	p.Start()
	waitFor2(t, "failed", 5*time.Second, func() bool { return p.Status().State == StateFailed })
	if !strings.Contains(p.Status().LastErr, "no initialize response within") {
		t.Fatalf("%q", p.Status().LastErr)
	}
	if p.Status().PID != 0 {
		t.Fatal("pid must be cleared")
	}
}

func TestNonJSONStdoutGoesToLogNotToClients(t *testing.T) {
	m, ls := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a", "FAKE_JUNK=1"))
	ts := serve(t, p)
	sid := initSession(t, ts.URL, nil)
	if got := callTool(t, ts.URL, sid, "echo", map[string]any{"text": "ok"}); got != "ok" {
		t.Fatal(got)
	}
	var found bool
	for _, l := range p.Logs(0) {
		if l.Src == "out" && l.Text == "this is not json" {
			found = true
		}
	}
	if !found || !strings.Contains(ls.String(), "managed[a] stdout: this is not json") {
		t.Fatalf("junk not captured: %v", p.Logs(0))
	}
}

func TestStderrRingBufferBoundedAndMirrored(t *testing.T) {
	m, ls := testMgr(t, func(o *Options) { o.LogLines = 5 })
	p, _ := m.Proc(fakeSpec("a"))
	ts := serve(t, p)
	sid := initSession(t, ts.URL, nil)
	for i := 0; i < 20; i++ {
		callTool(t, ts.URL, sid, "log", nil)
	}
	waitFor2(t, "ring full", 3*time.Second, func() bool { return p.ring.Len() == 5 })
	if len(p.Logs(0)) != 5 || p.Logs(2)[1].Src != "err" {
		t.Fatalf("%v", p.Logs(0))
	}
	if !strings.Contains(ls.String(), "managed[a] stderr: fake log line from tool") {
		t.Fatalf("stderr not mirrored with alias prefix:\n%s", ls.String())
	}
}

func TestEnvIsolationAndUserVariables(t *testing.T) {
	t.Setenv("OIDC_CLIENT_SECRET", "must-not-leak")
	t.Setenv("SECRETS_KEY", "must-not-leak-either")
	t.Setenv("PUID", "1234")
	m, ls := testMgr(t, nil)
	s := fakeSpec("a", "API_TOKEN=user-provided-token-value")
	p, _ := m.Proc(s)
	ts := serve(t, p)
	sid := initSession(t, ts.URL, nil)
	out := callTool(t, ts.URL, sid, "env", nil)
	if strings.Contains(out, "must-not-leak") || strings.Contains(out, "OIDC_CLIENT_SECRET") || strings.Contains(out, "SECRETS_KEY") || strings.Contains(out, `"PUID"`) {
		t.Fatalf("skgate secrets reached the child: %s", out)
	}
	var env struct {
		Keys   []string
		Values map[string]string
	}
	json.Unmarshal([]byte(strings.SplitN(out, "\n", 2)[0]), &env)
	base, _ := m.AliasDir("a")
	if env.Values["API_TOKEN"] != "user-provided-token-value" || env.Values["HOME"] != filepath.Join(base, "home") ||
		env.Values["TMPDIR"] != filepath.Join(base, "tmp") || env.Values["PATH"] == "" {
		t.Fatalf("%+v", env.Values)
	}
	if !strings.Contains(out, "cwd="+filepath.Join(base, "work")) && !strings.Contains(out, "cwd=") {
		t.Fatalf("cwd: %s", out)
	}
	if strings.Contains(ls.String(), "user-provided-token-value") {
		t.Fatalf("env value logged:\n%s", ls.String())
	}
}

func TestSecretsAreRedactedInLogs(t *testing.T) {
	m, ls := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a", "SECRET_TOKEN=abcdef123456"))
	ts := serve(t, p)
	sid := initSession(t, ts.URL, nil)
	callTool(t, ts.URL, sid, "leak", nil)
	waitFor2(t, "stderr captured", 3*time.Second, func() bool { return strings.Contains(ls.String(), "stderr: token is") })
	for _, l := range p.Logs(0) {
		if strings.Contains(l.Text, "abcdef123456") {
			t.Fatalf("ring holds a secret: %+v", l)
		}
	}
	if strings.Contains(ls.String(), "abcdef123456") || !strings.Contains(ls.String(), "token is [redacted] ok") || !strings.Contains(ls.String(), "stdout: not json but has [redacted]") {
		t.Fatalf("log:\n%s", ls.String())
	}
}

func TestDefaultWorkDirAndCustomWorkDir(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	ts := serve(t, p)
	sid := initSession(t, ts.URL, nil)
	base, _ := m.AliasDir("a")
	if out := callTool(t, ts.URL, sid, "env", nil); !strings.HasSuffix(strings.TrimSpace(out), "cwd="+filepath.Join(base, "work")) {
		t.Fatalf("default work dir: %s", out)
	}
	custom := t.TempDir()
	s := fakeSpec("b")
	s.WorkDir = custom
	p2, _ := m.Proc(s)
	ts2 := serve(t, p2)
	sid2 := initSession(t, ts2.URL, nil)
	if out := callTool(t, ts2.URL, sid2, "env", nil); !strings.HasSuffix(strings.TrimSpace(out), "cwd="+custom) {
		t.Fatalf("custom work dir: %s", out)
	}
}

func TestNoLeakedProcessesOnStopAndShutdown(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	ts := serve(t, p)
	sid := initSession(t, ts.URL, nil)
	pid := p.Status().PID
	grand := pidOf(callTool(t, ts.URL, sid, "spawn", nil)) // a grandchild `sleep 300`
	if grand == 0 || syscall.Kill(grand, 0) != nil {
		t.Fatal("grandchild not running")
	}
	p.Stop()
	if p.Status().State != StateStopped || p.Status().PID != 0 {
		t.Fatalf("%+v", p.Status())
	}
	waitFor2(t, "group gone", 4*time.Second, func() bool { return !groupAlive(pid) })
	waitFor2(t, "grandchild gone", 4*time.Second, func() bool { return !pidRunning(grand) })

	// same on manager shutdown, with two processes
	p2, _ := m.Proc(fakeSpec("b"))
	p3, _ := m.Proc(fakeSpec("c"))
	var pids, grands []int
	for _, q := range []*Proc{p2, p3} {
		u := serve(t, q)
		sd := initSession(t, u.URL, nil)
		pids = append(pids, q.Status().PID)
		grands = append(grands, pidOf(callTool(t, u.URL, sd, "spawn", nil)))
	}
	m.Shutdown()
	for i := range pids {
		if groupAlive(pids[i]) || pidRunning(grands[i]) {
			t.Fatalf("process %d or its grandchild %d survived shutdown", pids[i], grands[i])
		}
	}
	if _, err := m.Proc(fakeSpec("late")); err == nil {
		t.Fatal("a shut down manager must refuse new processes")
	}
}

// pidRunning is true when the pid exists and is not a zombie.
func pidRunning(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	b, err := os.ReadFile("/proc/" + itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	return !strings.Contains(string(b), ") Z ")
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestStopEscalatesToKill(t *testing.T) {
	// a shell that ignores SIGTERM: the process must still die after the grace period
	m, ls := testMgr(t, func(o *Options) { o.StopGrace = 300 * time.Millisecond })
	s := Spec{Alias: "a", Shell: true, Command: `trap '' TERM; echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"x"}}}'; while true; do sleep 1; done`}
	p, _ := m.Proc(s)
	if err := p.Ensure(context.Background(), startHint{}); err != nil {
		// initialize is sent by skgate with id 1 (the first pending id), so the canned answer matches
		t.Fatal(err)
	}
	pid := p.Status().PID
	start := time.Now()
	p.Stop()
	if groupAlive(pid) {
		t.Fatal("process survived SIGKILL escalation")
	}
	if time.Since(start) < 250*time.Millisecond || !strings.Contains(ls.String(), "killing") {
		t.Fatalf("expected an escalation after the grace period (took %s)\n%s", time.Since(start), ls.String())
	}
}

func TestHoldAfterStopAndRestart(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	ts := serve(t, p)
	initSession(t, ts.URL, nil)
	p.Stop(true)
	if !p.Status().Held {
		t.Fatal("hold flag")
	}
	if r := post(t, ts.URL, "", "application/json", rpc(1, "tools/list", nil)); r.Status != 502 || !strings.Contains(string(r.Body), "stopped by an administrator") {
		t.Fatalf("a held process must not start on demand: %d %s", r.Status, r.Body)
	}
	if err := p.Restart(); err != nil {
		t.Fatal(err)
	}
	waitFor2(t, "running", 5*time.Second, func() bool { return p.Status().State == StateRunning })
	if p.Status().Held {
		t.Fatal("restart clears the hold")
	}
}

func TestSpecChangeReplacesProcess(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a", "FAKE_NAME=one"))
	ts := serve(t, p)
	initSession(t, ts.URL, nil)
	pid := p.Status().PID
	if !strings.Contains(string(p.InitResult()), "one") {
		t.Fatal(string(p.InitResult()))
	}
	// lifecycle-only change keeps the process
	s := fakeSpec("a", "FAKE_NAME=one")
	s.IdleTimeout = time.Hour
	m.Proc(s)
	if p.Status().PID != pid || p.Status().State != StateRunning {
		t.Fatal("a timeout change must not restart")
	}
	// env change stops the old process; the next request starts the new one
	p2, _ := m.Proc(fakeSpec("a", "FAKE_NAME=two"))
	if p2 != p || groupAlive(pid) || p.Status().State != StateStopped {
		t.Fatalf("old process must be stopped: %+v", p.Status())
	}
	initSession(t, ts.URL, nil)
	if !strings.Contains(string(p.InitResult()), "two") || p.Status().PID == pid {
		t.Fatalf("new spec not used: %s", p.InitResult())
	}
}

func TestMaxProcsCap(t *testing.T) {
	m, _ := testMgr(t, func(o *Options) { o.MaxProcs = 2 })
	var ps []*Proc
	for _, a := range []string{"a", "b", "c"} {
		p, _ := m.Proc(fakeSpec(a))
		ps = append(ps, p)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, p := range ps[:2] {
		if err := p.Ensure(ctx, startHint{}); err != nil {
			t.Fatal(err)
		}
	}
	err := ps[2].Ensure(ctx, startHint{})
	if err == nil || !strings.Contains(err.Error(), "MANAGED_MAX_PROCS") {
		t.Fatalf("expected the limit error, got %v", err)
	}
	ps[0].Stop()
	if err := ps[2].Ensure(ctx, startHint{}); err != nil {
		t.Fatalf("a freed slot must be usable: %v", err)
	}
}

func TestDisabledManagerNeverSpawns(t *testing.T) {
	m, ls := testMgr(t, func(o *Options) { o.Enabled = false })
	marker := filepath.Join(t.TempDir(), "spawned")
	s := Spec{Alias: "a", Command: "/bin/sh", Args: []string{"-c", "touch " + marker}}
	if _, err := m.Proc(s); err != ErrDisabled {
		t.Fatalf("got %v", err)
	}
	m.StartAlways([]Spec{{Alias: "a", Command: "/bin/sh", Args: []string{"-c", "touch " + marker}, Lifecycle: Always}})
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a process was spawned while disabled")
	}
	if !strings.Contains(ls.String(), "not available") {
		t.Fatalf("expected a log line:\n%s", ls.String())
	}
}

func TestAliasDirRejectsTraversal(t *testing.T) {
	m, _ := testMgr(t, nil)
	for _, a := range []string{"", "..", ".", "a/b", "../x", "a b", ".hidden", strings.Repeat("a", 65)} {
		if _, err := m.AliasDir(a); err == nil {
			t.Errorf("%q must be rejected", a)
		}
		if _, err := m.Proc(Spec{Alias: a, Command: "x"}); err == nil {
			t.Errorf("Proc(%q) must be rejected", a)
		}
	}
	if d, err := m.AliasDir("my-server_1.x"); err != nil || filepath.Base(d) != "my-server_1.x" {
		t.Fatal(d, err)
	}
}

func TestInstallStepRunsOnceAndOnSync(t *testing.T) {
	m, _ := testMgr(t, nil)
	counter := filepath.Join(t.TempDir(), "count")
	s := fakeSpec("a")
	s.Install = `echo run >> ` + counter + `; echo "installing now"`
	p, _ := m.Proc(s)
	ts := serve(t, p)
	initSession(t, ts.URL, nil)
	count := func() int { b, _ := os.ReadFile(counter); return strings.Count(string(b), "run") }
	if count() != 1 {
		t.Fatalf("install ran %d times", count())
	}
	found := false
	for _, l := range p.Logs(0) {
		if l.Src == "install" && l.Text == "installing now" {
			found = true
		}
	}
	if !found {
		t.Fatalf("install output not captured: %v", p.Logs(0))
	}
	p.Restart()
	waitFor2(t, "running", 5*time.Second, func() bool { return p.Status().State == StateRunning })
	if count() != 1 {
		t.Fatalf("a plain restart must not reinstall (ran %d)", count())
	}
	p.Sync()
	waitFor2(t, "running", 5*time.Second, func() bool { return p.Status().State == StateRunning })
	if count() != 2 {
		t.Fatalf("Sync must reinstall (ran %d)", count())
	}
}

func TestFailingInstallMarksFailedWithoutRetryLoop(t *testing.T) {
	m, _ := testMgr(t, nil)
	s := fakeSpec("a")
	s.Install = `echo "boom: no network"; exit 7`
	p, _ := m.Proc(s)
	p.Start()
	waitFor2(t, "failed", 5*time.Second, func() bool { return p.Status().State == StateFailed })
	st := p.Status()
	if !strings.Contains(st.LastErr, "install step failed") || !strings.Contains(st.LastErr, "status 7") || !strings.Contains(st.LastErr, "boom") || st.Restarts != 0 {
		t.Fatalf("%+v", st)
	}
}

func TestMaxProcsZeroIsUnlimited(t *testing.T) {
	m, _ := testMgr(t, func(o *Options) { o.MaxProcs = 0 })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, a := range []string{"a", "b", "c", "d", "e"} {
		p, _ := m.Proc(fakeSpec(a))
		if err := p.Ensure(ctx, startHint{}); err != nil {
			t.Fatalf("%s: unlimited cap refused a start: %v", a, err)
		}
	}
}

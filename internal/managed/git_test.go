package managed

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func run(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var clean []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GIT_") {
			clean = append(clean, e)
		}
	}
	cmd.Env = append(clean, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repoFixture creates a bare repository with a work clone; write() commits a file to main.
type repoFixture struct {
	t        *testing.T
	bare, wc string
}

func newRepo(t *testing.T) *repoFixture {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	d := t.TempDir()
	f := &repoFixture{t: t, bare: filepath.Join(d, "origin.git"), wc: filepath.Join(d, "wc")}
	run(t, d, "git", "init", "-q", "--bare", "-b", "main", f.bare)
	run(t, d, "git", "clone", "-q", f.bare, f.wc)
	run(t, f.wc, "git", "checkout", "-q", "-B", "main")
	return f
}

func (f *repoFixture) commit(file, content, msg string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.wc, file), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	run(f.t, f.wc, "git", "add", file)
	run(f.t, f.wc, "git", "commit", "-q", "-m", msg)
	run(f.t, f.wc, "git", "push", "-q", "origin", "main")
}

func (f *repoFixture) url() string { return "file://" + f.bare }

func gitSpec(alias string, f *repoFixture, ref string) Spec {
	s := fakeSpec(alias)
	s.Git = &GitSpec{URL: f.url(), Ref: ref}
	s.Install = `cat version.txt >> ../install.log`
	return s
}

func TestGitCloneRunUpdateAndReinstall(t *testing.T) {
	f := newRepo(t)
	f.commit("version.txt", "v1\n", "one")
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(gitSpec("g", f, "main"))
	ts := serve(t, p)
	sid := initSession(t, ts.URL, nil)
	base, _ := m.AliasDir("g")
	if b, err := os.ReadFile(filepath.Join(base, "repo", "version.txt")); err != nil || string(b) != "v1\n" {
		t.Fatalf("clone: %q %v", b, err)
	}
	if out := callTool(t, ts.URL, sid, "env", nil); !strings.HasSuffix(strings.TrimSpace(out), "cwd="+filepath.Join(base, "repo")) {
		t.Fatalf("git upstreams run inside the checkout: %s", out)
	}
	rev1 := p.Rev()
	if len(rev1) != 12 {
		t.Fatalf("rev %q", rev1)
	}
	logf := func() string { b, _ := os.ReadFile(filepath.Join(base, "install.log")); return string(b) }
	if logf() != "v1\n" {
		t.Fatalf("install log %q", logf())
	}
	// a restart alone neither pulls nor reinstalls
	f.commit("version.txt", "v2\n", "two")
	p.Restart()
	waitFor2(t, "running", 8*time.Second, func() bool { return p.Status().State == StateRunning })
	if b, _ := os.ReadFile(filepath.Join(base, "repo", "version.txt")); string(b) != "v1\n" || logf() != "v1\n" {
		t.Fatal("Restart must not update the checkout")
	}
	// Update pulls, reinstalls and restarts
	if err := p.Update(); err != nil {
		t.Fatal(err)
	}
	waitFor2(t, "running after update", 10*time.Second, func() bool { return p.Status().State == StateRunning })
	if b, _ := os.ReadFile(filepath.Join(base, "repo", "version.txt")); string(b) != "v2\n" || logf() != "v1\nv2\n" {
		t.Fatalf("update: version %q install %q", b, logf())
	}
	if p.Rev() == rev1 {
		t.Fatal("revision did not change")
	}
}

func TestGitRefsTagBranchAndRefChangeRefetches(t *testing.T) {
	f := newRepo(t)
	f.commit("version.txt", "main1\n", "one")
	run(t, f.wc, "git", "tag", "v1.0.0")
	run(t, f.wc, "git", "push", "-q", "origin", "v1.0.0")
	run(t, f.wc, "git", "checkout", "-q", "-b", "dev")
	f.commit("version.txt", "dev1\n", "dev")
	run(t, f.wc, "git", "push", "-q", "origin", "dev")

	m, _ := testMgr(t, nil)
	p, _ := m.Proc(gitSpec("g", f, "v1.0.0"))
	initSession(t, serve(t, p).URL, nil)
	base, _ := m.AliasDir("g")
	read := func() string { b, _ := os.ReadFile(filepath.Join(base, "repo", "version.txt")); return string(b) }
	if read() != "main1\n" {
		t.Fatalf("tag checkout: %q", read())
	}
	// switching to a branch replaces the process (run fingerprint includes the ref) and refetches
	p2, _ := m.Proc(gitSpec("g", f, "dev"))
	initSession(t, serve(t, p2).URL, nil)
	if read() != "dev1\n" {
		t.Fatalf("branch checkout: %q", read())
	}
	// default branch (no ref) follows the remote HEAD
	p3, _ := m.Proc(gitSpec("g", f, ""))
	initSession(t, serve(t, p3).URL, nil)
	if read() != "main1\n" {
		t.Fatalf("default ref: %q", read())
	}
}

func TestGitFailureIsReported(t *testing.T) {
	f := newRepo(t)
	f.commit("a", "1", "one")
	m, ls := testMgr(t, nil)
	s := gitSpec("g", f, "no-such-branch")
	p, _ := m.Proc(s)
	p.Start()
	waitFor2(t, "failed", 8*time.Second, func() bool { return p.Status().State == StateFailed })
	if !strings.Contains(p.Status().LastErr, "git fetch") || p.Status().Restarts != 0 {
		t.Fatalf("%+v", p.Status())
	}
	if !strings.Contains(ls.String(), "managed[g] git:") {
		t.Fatalf("git output must be mirrored to the log:\n%s", ls.String())
	}
}

func TestGitTokenNeverInArgvOrLogs(t *testing.T) {
	// A stub `git` first in PATH records its arguments and the config passed by environment.
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	stub := "#!/bin/sh\necho \"ARGV: $*\" >> " + rec + "\nenv | grep -E '^GIT_CONFIG_(KEY|VALUE)_' | sort >> " + rec + "\necho \"fatal: token abc-secret-token-123 was rejected\" >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	m, ls := testMgr(t, func(o *Options) {
		o.Environ = func() []string { return []string{"PATH=" + dir + ":/usr/bin:/bin"} }
	})
	s := fakeSpec("g")
	s.Command = os.Args[0]
	s.Git = &GitSpec{URL: "https://git.example.com/org/repo.git", Ref: "main", Token: "abc-secret-token-123"}
	p, _ := m.Proc(s)
	p.Start()
	waitFor2(t, "failed", 8*time.Second, func() bool { return p.Status().State == StateFailed })
	b, _ := os.ReadFile(rec)
	got := string(b)
	if strings.Contains(strings.Split(got, "GIT_CONFIG_KEY")[0], "abc-secret-token-123") {
		t.Fatalf("token in argv:\n%s", got)
	}
	if !strings.Contains(got, "http.extraHeader") || !strings.Contains(got, "Authorization: Basic ") {
		t.Fatalf("token must be passed as http.extraHeader through the environment:\n%s", got)
	}
	all := ls.String() + p.Status().LastErr
	for _, l := range p.Logs(0) {
		all += l.Text
	}
	if strings.Contains(all, "abc-secret-token-123") {
		t.Fatalf("token leaked into logs:\n%s", all)
	}
	if !strings.Contains(all, "[redacted]") {
		t.Fatalf("expected the rejected-token line to be redacted:\n%s", all)
	}
	// the token must not reach the MCP child either
	if strings.Contains(strings.Join(BuildEnv(nil, "/h", "/t", s.Env), " "), "abc-secret-token-123") {
		t.Fatal("token in the child env")
	}
}

func TestGitEnvIgnoresUserConfig(t *testing.T) {
	env := strings.Join(gitEnv(nil, &GitSpec{URL: "https://h/x"}), "\n")
	for _, want := range []string{"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "protocol.ext.allow", "GIT_CONFIG_COUNT=1"} {
		if !strings.Contains(env, want) {
			t.Errorf("missing %s in\n%s", want, env)
		}
	}
	if strings.Contains(env, "extraHeader") {
		t.Fatal("no header without a token")
	}
}

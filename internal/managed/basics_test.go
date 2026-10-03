package managed

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRingKeepsNewestAndClips(t *testing.T) {
	r := NewRing(3)
	for i := 1; i <= 5; i++ {
		r.Add("err", fmt.Sprintf("line %d\r\n", i))
	}
	got := r.Last(0)
	if len(got) != 3 || got[0].Text != "line 3" || got[2].Text != "line 5" || r.Len() != 3 {
		t.Fatalf("%+v", got)
	}
	if l := r.Last(2); len(l) != 2 || l[0].Text != "line 4" {
		t.Fatalf("last 2: %+v", l)
	}
	r.Add("out", strings.Repeat("é", maxLineBytes))
	if l := r.Last(1)[0]; len(l.Text) > maxLineBytes+4 || !strings.HasSuffix(l.Text, "…") {
		t.Fatalf("long line not clipped: %d", len(l.Text))
	}
	r.Clear()
	if r.Len() != 0 || len(r.Last(0)) != 0 {
		t.Fatal("clear")
	}
	if NewRing(0).Add("x", "y"); false {
		t.Fatal()
	}
}

func TestLineWriterSplitsAndFlushes(t *testing.T) {
	var got []string
	w := &lineWriter{fn: func(s string) { got = append(got, s) }}
	w.Write([]byte("a\nb"))
	w.Write([]byte("c\n\nd"))
	if strings.Join(got, "|") != "a|bc|" {
		t.Fatalf("%q", got)
	}
	w.Flush()
	if got[len(got)-1] != "d" {
		t.Fatalf("%q", got)
	}
	w.Write([]byte(strings.Repeat("x", 5*maxLineBytes)))
	if len(got) < 4 {
		t.Fatal("a huge line without newline must be cut, not buffered forever")
	}
}

func TestBuildEnvIsolation(t *testing.T) {
	parent := []string{
		"PATH=/opt/bin:/usr/bin", "HOME=/root", "OIDC_CLIENT_SECRET=topsecret", "SECRETS_KEY=k", "PUID=1000",
		"NPM_CONFIG_CACHE=/data/cache/npm", "LANG=en_US.UTF-8", "TMPDIR=/tmp", "AWS_SECRET_ACCESS_KEY=zzz",
	}
	env := BuildEnv(parent, "/data/managed/x/home", "/data/managed/x/tmp", []KV{{"API_KEY", "abc"}, {"LANG", "C"}})
	m := map[string]string{}
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		if _, dup := m[k]; dup {
			t.Fatalf("duplicate %s", k)
		}
		m[k] = v
	}
	for _, bad := range []string{"OIDC_CLIENT_SECRET", "SECRETS_KEY", "PUID", "AWS_SECRET_ACCESS_KEY"} {
		if _, ok := m[bad]; ok {
			t.Errorf("%s leaked into the child environment", bad)
		}
	}
	if m["HOME"] != "/data/managed/x/home" || m["TMPDIR"] != "/data/managed/x/tmp" || m["PATH"] != "/opt/bin:/usr/bin" ||
		m["NPM_CONFIG_CACHE"] != "/data/cache/npm" || m["API_KEY"] != "abc" || m["LANG"] != "C" {
		t.Fatalf("env: %v", m)
	}
	if e2 := BuildEnv(nil, "/h", "/t", nil); !strings.Contains(strings.Join(e2, " "), "PATH=/usr/local/bin") || !strings.Contains(strings.Join(e2, " "), "LANG=C.UTF-8") {
		t.Fatalf("defaults: %v", e2)
	}
}

func TestRedactor(t *testing.T) {
	r := newRedactor("supersecretvalue", "abc", "", "tok-123456")
	got := r.apply("using supersecretvalue and tok-123456 and abc")
	if got != "using [redacted] and [redacted] and abc" {
		t.Fatalf("%q", got)
	}
	var nilR *redactor
	if nilR.apply("x") != "x" {
		t.Fatal("nil redactor")
	}
}

func TestSpecValidate(t *testing.T) {
	ok := Spec{Alias: "a", Command: "npx", Args: []string{"-y", "@scope/pkg"}, Env: []KV{{"TOKEN", "x"}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]Spec{
		"empty command":     {Command: "  "},
		"nul in command":    {Command: "a\x00b"},
		"newline no shell":  {Command: "a\nb"},
		"bad env name":      {Command: "x", Env: []KV{{"1BAD", "v"}}},
		"env with dash":     {Command: "x", Env: []KV{{"A-B", "v"}}},
		"dup env":           {Command: "x", Env: []KV{{"A", "1"}, {"A", "2"}}},
		"nul env value":     {Command: "x", Env: []KV{{"A", "1\x002"}}},
		"relative workdir":  {Command: "x", WorkDir: "rel/dir"},
		"bad lifecycle":     {Command: "x", Lifecycle: "sometimes"},
		"neg timeout":       {Command: "x", StartupTimeout: -1},
		"huge timeout":      {Command: "x", StartupTimeout: 2 * time.Hour},
		"arg nul":           {Command: "x", Args: []string{"a\x00"}},
		"too many args":     {Command: "x", Args: make([]string, MaxArgs+1)},
		"git ssh":           {Command: "x", Git: &GitSpec{URL: "ssh://git@host/x.git"}},
		"git scp":           {Command: "x", Git: &GitSpec{URL: "git@host:x/y.git"}},
		"git creds in url":  {Command: "x", Git: &GitSpec{URL: "https://user:pw@host/x.git"}},
		"git empty":         {Command: "x", Git: &GitSpec{}},
		"git bad ref":       {Command: "x", Git: &GitSpec{URL: "https://h/x.git", Ref: "--upload-pack=evil"}},
		"git dotdot ref":    {Command: "x", Git: &GitSpec{URL: "https://h/x.git", Ref: "a/../b"}},
		"git space in url":  {Command: "x", Git: &GitSpec{URL: "https://h/x y.git"}},
		"git newline token": {Command: "x", Git: &GitSpec{URL: "https://h/x.git", Token: "a\nb"}},
	}
	for name, s := range bad {
		if err := s.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	shell := Spec{Command: "cd x && run", Shell: true}
	if err := shell.Validate(); err != nil {
		t.Fatalf("multi-word shell command: %v", err)
	}
	if err := (Spec{Command: "x", Git: &GitSpec{URL: "https://h.example/x.git", Ref: "release/1.2", Token: "tok"}}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRunFingerprintTracksWhatMatters(t *testing.T) {
	a := Spec{Command: "x", Args: []string{"1"}, Env: []KV{{"K", "v"}}}
	same := a
	same.IdleTimeout = time.Hour // lifecycle knobs do not restart the process
	same.Lifecycle = Always
	if a.runFingerprint() != same.runFingerprint() {
		t.Fatal("timeouts and lifecycle must not change the fingerprint")
	}
	for name, mod := range map[string]func(*Spec){
		"command": func(s *Spec) { s.Command = "y" }, "args": func(s *Spec) { s.Args = []string{"2"} },
		"env": func(s *Spec) { s.Env = []KV{{"K", "w"}} }, "shell": func(s *Spec) { s.Shell = true },
		"dir": func(s *Spec) { s.WorkDir = "/w" }, "install": func(s *Spec) { s.Install = "make" },
		"git": func(s *Spec) { s.Git = &GitSpec{URL: "https://h/x"} },
	} {
		b := a
		mod(&b)
		if a.runFingerprint() == b.runFingerprint() {
			t.Errorf("%s change must change the fingerprint", name)
		}
	}
}

func TestCacheEnvIsPerAlias(t *testing.T) {
	m, _ := testMgr(t, nil)
	a, err := m.AliasCacheDir("a")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := m.AliasCacheDir("b")
	if a == b || filepath.Dir(a) != filepath.Dir(b) || !strings.HasPrefix(a, m.o.CacheDir) {
		t.Fatalf("%s %s", a, b)
	}
	if _, err := m.AliasCacheDir("../x"); err == nil {
		t.Fatal("traversal must be refused")
	}
	// the child sees its own caches, overriding whatever skgate inherited; a user variable still wins
	p, _ := m.Proc(fakeSpec("a", "UV_CACHE_DIR=/custom"))
	work, home, tmp, err := p.dirs(p.Spec())
	if err != nil || work == "" {
		t.Fatal(err)
	}
	env := strings.Join(p.childEnv(p.Spec(), home, tmp), "\n")
	for _, want := range []string{"NPM_CONFIG_CACHE=" + a + "/npm", "PIP_CACHE_DIR=" + a + "/pip", "XDG_CACHE_HOME=" + a + "/xdg", "UV_CACHE_DIR=/custom"} {
		if !strings.Contains(env, want) {
			t.Errorf("missing %s in\n%s", want, env)
		}
	}
	if st, err := os.Stat(a); err != nil || !st.IsDir() {
		t.Fatal("the cache dir must exist before the first run")
	}
}

// .NET children find the SDK, skip telemetry and keep NuGet packages in their own cache.
func TestDotnetEnv(t *testing.T) {
	env := BuildEnv([]string{"DOTNET_ROOT=/usr/share/dotnet", "DOTNET_CLI_TELEMETRY_OPTOUT=1", "SECRETS_KEY=x"}, "/h", "/t", CacheEnv("/c"))
	got := strings.Join(env, "\n")
	for _, want := range []string{"DOTNET_ROOT=/usr/share/dotnet", "DOTNET_CLI_TELEMETRY_OPTOUT=1", "NUGET_PACKAGES=/c/nuget"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(got, "SECRETS_KEY") {
		t.Error("a secret leaked into the child")
	}
	found := false
	for _, c := range commonCommands {
		found = found || c.Name == "dotnet"
	}
	if !found {
		t.Error("dotnet is not offered as a command")
	}
}

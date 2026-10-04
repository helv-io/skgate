package managed

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func texts(ls []Line) string {
	var s []string
	for _, l := range ls {
		s = append(s, l.Src+":"+l.Text)
	}
	return strings.Join(s, "\n")
}

// A run writes its lifecycle into the log: starting, started with the pid, stderr, stopped with the signal. A
// second start is a new run; a third drops the first.
func TestLogHasLifecycleLinesAndKeepsTwoRuns(t *testing.T) {
	m, _ := testMgr(t, nil)
	spec := fakeSpec("a", "FAKE_STDERR=hello from stderr")
	spec.PlainEnv = []string{"FAKE_STDERR"} // not masked, so the line can be read back
	p, _ := m.Proc(spec)
	run := func() {
		t.Helper()
		if err := p.Start(); err != nil {
			t.Fatal(err)
		}
		waitFor2(t, "running", 5*time.Second, func() bool { return p.Status().State == StateRunning })
		p.Stop()
	}
	run()
	first := texts(p.Logs(0))
	for _, want := range []string{"sys:starting", "sys:started, pid ", "err:hello from stderr", "sys:stopped ("} {
		if !strings.Contains(first, want) {
			t.Fatalf("missing %q in\n%s", want, first)
		}
	}
	run()
	starts := func() int {
		n := 0
		for _, l := range p.Logs(0) {
			if l.Start {
				n++
			}
		}
		return n
	}
	if starts() != 2 {
		t.Fatalf("two runs expected:\n%s", texts(p.Logs(0)))
	}
	firstPID := ""
	for _, l := range p.Logs(0) {
		if strings.HasPrefix(l.Text, "started, pid ") {
			firstPID = l.Text
			break
		}
	}
	run()
	if starts() != 2 {
		t.Fatalf("only the current and the previous run stay:\n%s", texts(p.Logs(0)))
	}
	for _, l := range p.Logs(0) {
		if l.Text == firstPID && l.Run == 1 {
			t.Fatalf("the first run is still there")
		}
	}
}

func TestLogSurvivesARestartOfSkgateAndIsDroppedWithTheUpstream(t *testing.T) {
	dir := t.TempDir()
	mut := func(o *Options) { o.Dir = dir }
	m, _ := testMgr(t, mut)
	spec := fakeSpec("a", "FAKE_STDERR=remember me")
	spec.PlainEnv = []string{"FAKE_STDERR"}
	p, _ := m.Proc(spec)
	p.Start()
	waitFor2(t, "running", 5*time.Second, func() bool { return p.Status().State == StateRunning })
	m.Shutdown() // stops the process, which writes the file
	f := filepath.Join(dir, ".logs", "a.json")
	if st, err := os.Stat(f); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("log file: %v %v", st, err)
	}

	m2, _ := testMgr(t, mut)
	// without a process the kept log can still be read
	if r, ok := m2.Log("a"); !ok || !strings.Contains(texts(r.Last(0)), "err:remember me") {
		t.Fatalf("kept log not readable")
	}
	p2, _ := m2.Proc(spec)
	if !strings.Contains(texts(p2.Logs(0)), "err:remember me") {
		t.Fatalf("restored log:\n%s", texts(p2.Logs(0)))
	}
	before := p2.Logs(0)[len(p2.Logs(0))-1].ID
	p2.Start()
	waitFor2(t, "running", 5*time.Second, func() bool { return p2.Status().State == StateRunning })
	p2.Stop()
	var runs, startsSeen int
	for _, l := range p2.Logs(0) {
		if l.ID <= before {
			runs = l.Run
		}
		if l.Start {
			startsSeen++
		}
	}
	if runs != 1 || startsSeen != 2 || p2.Logs(0)[len(p2.Logs(0))-1].ID <= before {
		t.Fatalf("the restored run is the previous one now:\n%s", texts(p2.Logs(0)))
	}
	p2.ClearLogs()
	if r, _ := m2.Log("a"); r.Len() != 0 {
		t.Fatal("clearing empties the kept log too")
	}
	m2.DropLog("a")
	if _, err := os.Stat(f); err == nil {
		t.Fatal("DropLog must remove the file")
	}
}

// Values of env vars not marked plain are masked before a line is stored; plain ones (URLs, hosts) show.
func TestOnlySecretEnvIsMaskedInTheLog(t *testing.T) {
	m, _ := testMgr(t, nil)
	spec := fakeSpec("a", "SECRET_TOKEN=abcdef123456", "SERVICE_URL=http://example.internal:9000")
	spec.PlainEnv = []string{"SERVICE_URL"}
	p, _ := m.Proc(spec)
	p.note("secret %s and url %s", "abcdef123456", "http://example.internal:9000")
	all := texts(p.Logs(0))
	if strings.Contains(all, "abcdef123456") || !strings.Contains(all, "http://example.internal:9000") {
		t.Fatalf("masking:\n%s", all)
	}
	// flagging the URL as secret later masks it without a restart
	spec.PlainEnv = nil
	if p2, _ := m.Proc(spec); p2 != p {
		t.Fatal("same process expected")
	}
	p.note("again %s", "http://example.internal:9000")
	ls := p.Logs(1)
	if strings.Contains(ls[0].Text, "example.internal") {
		t.Fatalf("now secret: %q", ls[0].Text)
	}
}

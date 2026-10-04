package managed

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRingKeepsTheLastLinesAndNumbersThem(t *testing.T) {
	r := NewRing(5)
	for i := 1; i <= 20; i++ {
		r.Add("err", fmt.Sprintf("line %d", i))
	}
	got := r.Last(0)
	if len(got) != 5 || got[0].Text != "line 16" || got[4].Text != "line 20" || got[4].ID != 20 {
		t.Fatalf("kept %+v", got)
	}
	if r.Len() != 5 {
		t.Fatalf("Len = %d", r.Len())
	}
}

func TestRingIsCappedByBytes(t *testing.T) {
	r := NewRing(100000)
	line := strings.Repeat("x", 3000)
	for i := 0; i < 400; i++ { // 1.2 MB offered
		r.Add("out", line)
	}
	total := 0
	for _, l := range r.Last(0) {
		total += len(l.Text)
	}
	if total > MaxLogBytes || total < MaxLogBytes-3001 {
		t.Fatalf("holds %d bytes, want just under %d", total, MaxLogBytes)
	}
}

func TestRingCutsLongLinesAndSaysSo(t *testing.T) {
	r := NewRing(10)
	short := r.Add("err", "short")
	long := r.Add("err", strings.Repeat("é", 5000))
	if short.Trunc || !long.Trunc || len(long.Text) > maxLineBytes+len("…") || !strings.HasSuffix(long.Text, "…") {
		t.Fatalf("short %+v long len %d trunc %v", short, len(long.Text), long.Trunc)
	}
}

// Only the current and the previous run are kept.
func TestRingKeepsTwoRuns(t *testing.T) {
	r := NewRing(100)
	for run := 1; run <= 3; run++ {
		r.BeginRun("starting")
		r.Add("err", fmt.Sprintf("run %d a", run))
		r.Add("err", fmt.Sprintf("run %d b", run))
	}
	var texts []string
	starts := 0
	for _, l := range r.Last(0) {
		texts = append(texts, l.Text)
		if l.Start {
			starts++
		}
	}
	want := "starting|run 2 a|run 2 b|starting|run 3 a|run 3 b"
	if strings.Join(texts, "|") != want || starts != 2 {
		t.Fatalf("kept %v", texts)
	}
}

func TestRingSinceResumesAndResets(t *testing.T) {
	r := NewRing(50)
	for i := 1; i <= 5; i++ {
		r.Add("out", fmt.Sprint(i))
	}
	gen := r.Gen()
	lines, _, reset := r.Since(3, gen)
	if reset || len(lines) != 2 || lines[0].Text != "4" {
		t.Fatalf("since 3: %+v reset=%v", lines, reset)
	}
	if lines, _, reset = r.Since(5, gen); reset || len(lines) != 0 {
		t.Fatalf("since the newest: %+v reset=%v", lines, reset)
	}
	if lines, _, reset = r.Since(99, gen); !reset || len(lines) != 5 {
		t.Fatalf("a viewer ahead of the log starts over: %+v reset=%v", lines, reset)
	}
	r.Clear()
	r.Add("out", "after")
	lines, g, reset := r.Since(5, gen)
	if !reset || g == gen || len(lines) != 1 || lines[0].Text != "after" || lines[0].ID != 6 {
		t.Fatalf("after clear: %+v gen %d reset=%v", lines, g, reset)
	}
}

func TestRingWaitWakesOnAdd(t *testing.T) {
	r := NewRing(5)
	w := r.Wait()
	select {
	case <-w:
		t.Fatal("woke without a change")
	default:
	}
	go func() { time.Sleep(10 * time.Millisecond); r.Add("out", "x") }()
	select {
	case <-w:
	case <-time.After(2 * time.Second):
		t.Fatal("did not wake")
	}
}

func TestRingExportImportKeepsIDsAndRuns(t *testing.T) {
	r := NewRing(50)
	r.BeginRun("starting")
	r.Add("err", "one")
	r.BeginRun("starting")
	r.Add("err", "two")
	b := r.Export()
	r2 := NewRing(50)
	if err := r2.Import(b); err != nil {
		t.Fatal(err)
	}
	a, c := r.Last(0), r2.Last(0)
	if len(a) != len(c) || c[len(c)-1].ID != a[len(a)-1].ID || c[len(c)-1].Run != 2 || !c[2].Start {
		t.Fatalf("round trip: %+v vs %+v", a, c)
	}
	if n := r2.Add("err", "three"); n.ID != a[len(a)-1].ID+1 {
		t.Fatalf("ids continue: %d", n.ID)
	}
	if NewRing(5).Import([]byte("nonsense")) == nil {
		t.Fatal("garbage must be refused")
	}
}

func TestDetectLevel(t *testing.T) {
	for in, want := range map[string]string{
		"2026/10/03 12:00:00 ERROR something broke":     "error",
		"[warn] disk is almost full":                    "warn",
		"INFO: listening on :8080":                      "info",
		`{"level":"error","msg":"x"}`:                   "error",
		`time=2026-10-03 level=WARNING msg="slow"`:      "warn",
		"DEBUG cache miss":                              "debug",
		"Traceback (most recent call last):":            "",
		"    at Object.<anonymous> (/app/index.js:3:9)": "",
		"listening":            "",
		"FATAL: out of memory": "error",
		"a rather long sentence that goes on for a good while before it mentions info": "",
	} {
		if got := detectLevel(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

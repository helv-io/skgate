package managed

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/managed/fakemcp"
)

func TestMain(m *testing.M) {
	if fakemcp.IsChild() {
		fakemcp.Run(os.Stdin, os.Stdout)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type logSink struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logSink) Printf(f string, a ...any) {
	l.mu.Lock()
	fmt.Fprintf(&l.b, f+"\n", a...)
	l.mu.Unlock()
}
func (l *logSink) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// testMgr returns a manager with fast supervision timings.
func testMgr(t *testing.T, mut func(*Options)) (*Manager, *logSink) {
	t.Helper()
	ls := &logSink{}
	o := Options{Enabled: true, Dir: t.TempDir(), MaxProcs: 8, StopGrace: 2 * time.Second, LogLines: 200,
		InstallMax: 20 * time.Second, Logf: ls.Printf, Version: "test",
		BackoffBase: 20 * time.Millisecond, BackoffMax: 100 * time.Millisecond, MaxCrashes: 3,
		StableAfter: time.Hour, FailCooldown: 200 * time.Millisecond, Tick: 20 * time.Millisecond}
	if mut != nil {
		mut(&o)
	}
	m := NewManager(o)
	t.Cleanup(func() {
		m.Shutdown()
		// An update still running when Shutdown began can start its process again after Shutdown stopped it
		// (Shutdown waits for the update, not for what the update started). Stop every process once more, so
		// nothing is left running or writing when t.TempDir is removed.
		m.mu.Lock()
		ps := make([]*Proc, 0, len(m.procs))
		for _, p := range m.procs {
			ps = append(ps, p)
		}
		m.mu.Unlock()
		for _, p := range ps {
			p.Stop()
		}
	})
	return m, ls
}

// fakeSpec runs this test binary as the fake server. extra are additional KEY=VALUE variables.
func fakeSpec(alias string, extra ...string) Spec {
	s := Spec{Alias: alias, Command: os.Args[0], Env: []KV{{"FAKE_MCP", "1"}}, StartupTimeout: 10 * time.Second}
	for _, e := range extra {
		k, v, _ := strings.Cut(e, "=")
		s.Env = append(s.Env, KV{k, v})
	}
	return s
}

func fail(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": desc})
}

func serve(t *testing.T, p *Proc) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.ServeHTTP(w, r, fail) }))
	t.Cleanup(ts.Close)
	return ts
}

type reply struct {
	Status int
	Header http.Header
	Body   []byte
	Events []rpcMsg // parsed when the response is an event stream, else the single JSON body
}

func parseReply(resp *http.Response) reply {
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	r := reply{Status: resp.StatusCode, Header: resp.Header, Body: b}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(make([]byte, 1<<20), 8<<20)
		for sc.Scan() {
			if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var m rpcMsg
				if json.Unmarshal([]byte(d), &m) == nil {
					r.Events = append(r.Events, m)
				}
			}
		}
		return r
	}
	var m rpcMsg
	if json.Unmarshal(b, &m) == nil {
		r.Events = []rpcMsg{m}
	}
	return r
}

func post(t *testing.T, url, sid, accept string, body any) reply {
	t.Helper()
	var b []byte
	if s, ok := body.(string); ok {
		b = []byte(s)
	} else {
		b, _ = json.Marshal(body)
	}
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if accept == "" {
		accept = "application/json, text/event-stream"
	}
	req.Header.Set("Accept", accept)
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return parseReply(resp)
}

func rpc(id any, method string, params any) map[string]any {
	m := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		m["id"] = id
	}
	if params != nil {
		m["params"] = params
	}
	return m
}

// initSession runs initialize + initialized and returns the session id.
func initSession(t *testing.T, url string, caps map[string]any) string {
	t.Helper()
	if caps == nil {
		caps = map[string]any{}
	}
	r := post(t, url, "", "application/json", rpc(1, "initialize", map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": caps, "clientInfo": map[string]any{"name": "t"}}))
	if r.Status != 200 || len(r.Events) != 1 || r.Events[0].Error != nil {
		t.Fatalf("initialize: %d %s", r.Status, r.Body)
	}
	sid := r.Header.Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatal("no session id")
	}
	if n := post(t, url, sid, "", rpc(nil, "notifications/initialized", nil)); n.Status != 202 {
		t.Fatalf("initialized: %d", n.Status)
	}
	return sid
}

func toolText(t *testing.T, m rpcMsg) string {
	t.Helper()
	if m.Error != nil {
		t.Fatalf("rpc error: %v", m.Error)
	}
	var res struct {
		Content []struct{ Text string } `json:"content"`
	}
	if err := json.Unmarshal(m.Result, &res); err != nil || len(res.Content) == 0 {
		t.Fatalf("tool result %s", m.Result)
	}
	return res.Content[0].Text
}

func callTool(t *testing.T, url, sid, name string, args map[string]any) string {
	t.Helper()
	r := post(t, url, sid, "application/json", rpc(7, "tools/call", map[string]any{"name": name, "arguments": args}))
	if r.Status != 200 || len(r.Events) != 1 {
		t.Fatalf("tools/call %s: %d %s", name, r.Status, r.Body)
	}
	return toolText(t, r.Events[0])
}

func waitFor2(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func pidOf(s string) int { n, _ := strconv.Atoi(strings.TrimSpace(s)); return n }

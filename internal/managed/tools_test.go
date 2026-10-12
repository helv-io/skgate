package managed

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWithListChangedSetsTheCapability(t *testing.T) {
	for _, in := range []string{
		`{"protocolVersion":"x","capabilities":{"tools":{"listChanged":false},"logging":{}}}`,
		`{"protocolVersion":"x","capabilities":{}}`,
		`{"protocolVersion":"x"}`,
	} {
		var r struct {
			Capabilities struct {
				Tools struct {
					ListChanged bool `json:"listChanged"`
				} `json:"tools"`
			} `json:"capabilities"`
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(withListChanged(json.RawMessage(in)), &r); err != nil || !r.Capabilities.Tools.ListChanged || r.ProtocolVersion != "x" {
			t.Fatalf("%s: %+v %v", in, r, err)
		}
	}
	if got := string(withListChanged(json.RawMessage(`not json`))); got != "not json" {
		t.Fatalf("invalid input changed: %s", got)
	}
}

func TestInitializeAdvertisesListChanged(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	ts := serve(t, p)
	r := post(t, ts.URL, "", "application/json", rpc(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}}))
	if !strings.Contains(string(r.Body), `"listChanged":true`) {
		t.Fatalf("initialize: %s", r.Body)
	}
}

// toolsStream opens the session's GET stream and returns the methods it receives.
func toolsStream(t *testing.T, url, sid string) chan string {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", sid)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET stream: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	ch := make(chan string, 16)
	go func() {
		rd := newSSEReader(resp.Body)
		for {
			m, ok := rd.next()
			if !ok {
				return
			}
			ch <- m.Method
		}
	}()
	return ch
}

func waitHash(t *testing.T, p *Proc, not string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h := p.ToolsHash(); h != "" && h != not {
			return h
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("tools hash stayed %q", not)
	return ""
}

func TestRestartWithNewToolsNotifiesOnlyWhenTheListChanged(t *testing.T) {
	extra := filepath.Join(t.TempDir(), "tools")
	changed := make(chan string, 4)
	m, _ := testMgr(t, func(o *Options) { o.OnToolsChanged = func(a string) { changed <- a } })
	p, _ := m.Proc(fakeSpec("a", "FAKE_TOOLS_FILE="+extra))
	ts := serve(t, p)
	sid := initSession(t, ts.URL, nil)
	h1 := waitHash(t, p, "")
	events := toolsStream(t, ts.URL, sid)
	time.Sleep(50 * time.Millisecond)

	// Same tools after a restart: nothing is sent.
	if err := p.Restart(); err != nil {
		t.Fatal(err)
	}
	ensure(t, p)
	time.Sleep(300 * time.Millisecond)
	if p.ToolsHash() != h1 {
		t.Fatal("hash changed without a change")
	}
	select {
	case ev := <-events:
		t.Fatalf("unexpected %s", ev)
	case a := <-changed:
		t.Fatalf("unexpected change for %s", a)
	default:
	}

	// A new tool after a restart (an update): the session hears of it, and a fresh list has it.
	if err := os.WriteFile(extra, []byte("added\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.Restart(); err != nil {
		t.Fatal(err)
	}
	ensure(t, p)
	waitHash(t, p, h1)
	select {
	case ev := <-events:
		if ev != ToolsListChanged {
			t.Fatalf("got %s", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no tools/list_changed")
	}
	select {
	case a := <-changed:
		if a != "a" {
			t.Fatalf("OnToolsChanged(%q)", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnToolsChanged not called")
	}
	r := post(t, ts.URL, sid, "application/json", rpc(2, "tools/list", nil))
	if !strings.Contains(string(r.Body), `"added"`) {
		t.Fatalf("tools/list after the change: %s", r.Body)
	}
}

func ensure(t *testing.T, p *Proc) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for p.Status().State != StateRunning {
		if time.Now().After(deadline) {
			t.Fatalf("not running: %+v", p.Status())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

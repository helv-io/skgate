package app

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// logsApp has a running always-on managed upstream whose child writes an ERROR line to stderr.
func logsApp(t *testing.T) (*App, *browser, string) {
	a, br, csrf := managedApp(t)
	f := stdioForm(csrf, "tools", url.Values{"lifecycle": {"always"}, "env_name": {"FAKE_MCP", "API_TOKEN", "FAKE_STDERR", ""},
		"env_value": {"1", "super-secret-token-4321", "2026/10/03 ERROR the disk is on fire", ""}})
	br.post("/admin/upstreams/save", f)
	waitProc(t, a, "tools", "running")
	return a, br, csrf
}

func waitProc(t *testing.T, a *App, alias, want string) {
	t.Helper()
	end := time.Now().Add(10 * time.Second)
	for time.Now().Before(end) {
		if p := a.MCP.Managed.Lookup(alias); p != nil && p.Status().State == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never became %s", alias, want)
}

type sseEvent struct{ ID, Name, Data string }

// follow opens the live stream and delivers its events until the test ends.
func follow(t *testing.T, br *browser, path, lastEventID string) <-chan sseEvent {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", br.ts.URL+path, nil)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := br.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	out := make(chan sseEvent, 256)
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		var ev sseEvent
		for sc.Scan() {
			switch l := sc.Text(); {
			case l == "":
				if ev.Name != "" {
					out <- ev
				}
				ev = sseEvent{}
			case strings.HasPrefix(l, "id: "):
				ev.ID = l[4:]
			case strings.HasPrefix(l, "event: "):
				ev.Name = l[7:]
			case strings.HasPrefix(l, "data: "):
				ev.Data = l[6:]
			}
		}
	}()
	return out
}

func nextLine(t *testing.T, ch <-chan sseEvent, contains string) sseEvent {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Name == "line" && strings.Contains(ev.Data, contains) {
				return ev
			}
		case <-timeout:
			t.Fatalf("no line containing %q", contains)
		}
	}
}

func TestLogsPageShowsTheOutputAndMasksSecrets(t *testing.T) {
	_, br, _ := logsApp(t)
	_, page := br.get("/admin/upstreams/tools/logs")
	for _, w := range []string{`data-log-view`, `data-stream="/admin/upstreams/tools/logs/stream"`, `data-src="err"`, `data-lvl="error"`,
		"2026/10/03 ERROR the disk is on fire", `<span class="logmark">since last start</span>`, `data-src="sys"`, "starting", "started, pid",
		`data-log-stderr`, `data-log-time`, `data-log-jump`, `href="/admin/upstreams/tools/logs/download"`, `data-log-filter`} {
		if !strings.Contains(page, w) {
			t.Errorf("logs page misses %q", w)
		}
	}
	if regexp.MustCompile(`<select[^>]*data-log-level[^>]*hidden`).MatchString(page) {
		t.Error("a line carries a level, so the level filter is offered")
	}
	if strings.Contains(page, "super-secret-token-4321") {
		t.Error("a secret is on the logs page")
	}
	if strings.Contains(page, "<table class=\"table compact") {
		t.Error("the output is the shared viewer, not a table")
	}
	r, dl := br.get("/admin/upstreams/tools/logs/download")
	if r.StatusCode != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/plain") || !strings.Contains(r.Header.Get("Content-Disposition"), `attachment; filename="tools-output.txt"`) ||
		!strings.Contains(dl, "[err] 2026/10/03 ERROR the disk is on fire") || !strings.Contains(dl, "[sys] starting") {
		t.Errorf("download: %d %v\n%s", r.StatusCode, r.Header, dl)
	}
}

func TestLogStreamFollowsResumesAndResets(t *testing.T) {
	a, br, csrf := logsApp(t)
	ch := follow(t, br, "/admin/upstreams/tools/logs/stream?after=0", "")
	first := nextLine(t, ch, "starting")
	var j struct {
		ID    uint64 `json:"id"`
		Run   int    `json:"run"`
		Src   string `json:"src"`
		Start bool   `json:"start"`
		Abs   string `json:"abs"`
	}
	if json.Unmarshal([]byte(first.Data), &j) != nil || j.Src != "sys" || !j.Start || j.Run != 1 || j.Abs == "" || first.ID == "" {
		t.Fatalf("first event %+v", first)
	}
	nextLine(t, ch, "ERROR the disk is on fire")
	// a line produced while the page is open arrives
	br.post("/admin/upstreams/tools/process", url.Values{"csrf": {csrf}, "action": {"stop"}, "to": {"logs"}})
	stopped := nextLine(t, ch, "stopped (")
	waitProc(t, a, "tools", "stopped")

	// a browser that reconnects with Last-Event-ID gets only what it missed
	again := follow(t, br, "/admin/upstreams/tools/logs/stream?after=0", first.ID)
	got := nextLine(t, again, "ERROR the disk is on fire") // the first line it receives is the one after "starting"
	if got.ID <= first.ID {
		t.Fatalf("resumed from %s but got %s", first.ID, got.ID)
	}
	nextLine(t, again, "stopped (")
	_ = stopped

	// clearing the log tells the open page to start over
	br.post("/admin/upstreams/tools/process", url.Values{"csrf": {csrf}, "action": {"clear-logs"}, "to": {"logs"}})
	timeout := time.After(10 * time.Second)
	for done := false; !done; {
		select {
		case ev := <-ch:
			done = ev.Name == "reset"
		case <-timeout:
			t.Fatal("no reset event after clearing")
		}
	}
	br.post("/admin/upstreams/tools/process", url.Values{"csrf": {csrf}, "action": {"start"}, "to": {"logs"}})
	nextLine(t, ch, "starting")
}

func TestLogStreamNeedsSignInAndAnUpstream(t *testing.T) {
	_, br, _ := logsApp(t)
	if r, _ := br.get("/admin/upstreams/nosuch/logs/stream"); r.StatusCode != 404 {
		t.Errorf("unknown upstream: %d", r.StatusCode)
	}
	anon := newBrowser(t, br.ts)
	for _, p := range []string{"/admin/upstreams/tools/logs/stream", "/admin/upstreams/tools/logs/download"} {
		if r, _ := anon.get(p); r.StatusCode == 200 {
			t.Errorf("%s is open to anyone", p)
		}
	}
}

// The output viewer in a browser: filters, levels, relative times, following, jump to latest, run dividers.
func TestLogViewerInABrowser(t *testing.T) {
	a, br, csrf := managedApp(t)
	br.post("/admin/upstreams/save", stdioForm(csrf, "tools", url.Values{"lifecycle": {"always"},
		"env_name": {"FAKE_MCP", "FAKE_STDERR_COUNT", ""}, "env_value": {"1", "30", ""}}))
	waitProc(t, a, "tools", "running")
	for _, w := range []string{"390", "1280"} {
		// each width starts from one fresh run
		act := func(action string) {
			br.post("/admin/upstreams/tools/process", url.Values{"csrf": {csrf}, "action": {action}, "to": {"logs"}})
		}
		act("stop")
		waitProc(t, a, "tools", "stopped")
		act("clear-logs")
		act("start")
		waitProc(t, a, "tools", "running")
		end := time.Now().Add(10 * time.Second)
		for a.MCP.Managed.Lookup("tools").Status().State == "running" && len(a.MCP.Managed.Lookup("tools").Logs(0)) < 30 && time.Now().Before(end) {
			time.Sleep(20 * time.Millisecond)
		}
		runBrowserScript(t, "logview.js", br.ts.URL, br, w)
	}
}

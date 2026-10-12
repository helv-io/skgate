package mcp

import (
	"bufio"
	"net/http"
	"strings"
	"testing"
	"time"
)

// listStream opens the GET event stream of path and returns the JSON-RPC messages it carries.
func listStream(t *testing.T, e *env, key, path string) chan string {
	t.Helper()
	req, _ := http.NewRequest("GET", e.ts.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("GET %s: %v %v", path, err, resp)
	}
	t.Cleanup(func() { resp.Body.Close() })
	ch := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				ch <- d
			}
		}
	}()
	time.Sleep(50 * time.Millisecond) // subscribed
	return ch
}

func expectListChanged(t *testing.T, ch chan string, what string) {
	t.Helper()
	select {
	case d := <-ch:
		if !strings.Contains(d, `"notifications/tools/list_changed"`) {
			t.Fatalf("%s: got %s", what, d)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("%s: no tools/list_changed", what)
	}
}

func expectQuiet(t *testing.T, ch chan string, what string) {
	t.Helper()
	select {
	case d := <-ch:
		t.Fatalf("%s: unexpected %s", what, d)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestEndpointsAdvertiseListChanged(t *testing.T) {
	e := newEnv(t, nil)
	addOA(t, e, "notes", newOAFake(t).URL, "GET /notes")
	key, _, _ := e.keys.Create("t")
	for _, p := range []string{"/mcp", "/mcp/notes"} {
		_, m := e.rpc(key, p, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
		tools, _ := m["result"].(map[string]any)["capabilities"].(map[string]any)["tools"].(map[string]any)
		if tools["listChanged"] != true {
			t.Fatalf("%s: %v", p, m)
		}
	}
}

func TestOpenAPIToolChangesReachTheStreams(t *testing.T) {
	e := newEnv(t, nil)
	addOA(t, e, "notes", newOAFake(t).URL, "GET /notes")
	key, _, _ := e.keys.Create("t")
	one := listStream(t, e, key, "/mcp/notes")
	agg := listStream(t, e, key, "/mcp")
	e.rpc(key, "/mcp/notes", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)

	// Saving the same selection changes nothing and sends nothing.
	e.srv.ToolsChanged("notes")
	expectQuiet(t, one, "unchanged /mcp/notes")
	expectQuiet(t, agg, "unchanged /mcp")

	st, _ := e.srv.Upstreams.OpenAPI("notes")
	cfg := st.Config
	cfg.Selection.Enabled = map[string]bool{"DELETE /notes/{id}": true}
	if err := e.srv.Upstreams.SetOpenAPI("notes", cfg); err != nil {
		t.Fatal(err)
	}
	e.srv.ToolsChanged("notes")
	expectListChanged(t, one, "/mcp/notes")
	expectListChanged(t, agg, "/mcp")
	_, m := e.rpc(key, "/mcp/notes", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if got := strings.Join(toolNames(t, m), ","); got != "deleteNote" {
		t.Fatalf("fresh list = %q", got)
	}

	// Leaving an upstream out of /mcp changes only the aggregate.
	e.srv.UpstreamsChanged("notes")
	expectListChanged(t, agg, "/mcp after a flag change")
	expectQuiet(t, one, "/mcp/notes after a flag change")
}

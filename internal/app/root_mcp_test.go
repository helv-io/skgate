package app

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Root / is the aggregate MCP for MCP-shaped requests; browsers still land on /admin.
func TestRootMCPHeaderRouting(t *testing.T) {
	_, ts, _ := newApp(t, nil)
	cli := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// Browser-like GET → admin.
	req, _ := http.NewRequest("GET", ts.URL+"/", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/admin" {
		t.Fatalf("browser GET /: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Accept application/json → aggregate MCP (401 without token, PRM for /mcp).
	req, _ = http.NewRequest("POST", ts.URL+"/", strings.NewReader(`{}`))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err = cli.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("MCP Accept POST /: %d %s", resp.StatusCode, body)
	}
	www := resp.Header.Get("WWW-Authenticate")
	if !strings.Contains(www, "/.well-known/oauth-protected-resource/mcp") {
		t.Fatalf("WWW-Authenticate: %q", www)
	}

	// Accept text/event-stream alone.
	req, _ = http.NewRequest("GET", ts.URL+"/", nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err = cli.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("SSE Accept GET /: %d", resp.StatusCode)
	}

	// Content-Type JSON without Accept still routes to MCP.
	req, _ = http.NewRequest("POST", ts.URL+"/", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = cli.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("JSON Content-Type POST /: %d", resp.StatusCode)
	}

	// /mcp unchanged.
	req, _ = http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = cli.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST /mcp: %d", resp.StatusCode)
	}
}

func TestRootProtectedResourceMetadata(t *testing.T) {
	_, ts, _ := newApp(t, nil)
	cli := ts.Client()

	get := func(path string) map[string]any {
		t.Helper()
		resp, err := cli.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
		var m map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	root := get("/.well-known/oauth-protected-resource")
	mcp := get("/.well-known/oauth-protected-resource/mcp")
	rb, _ := json.Marshal(root)
	mb, _ := json.Marshal(mcp)
	if string(rb) != string(mb) {
		t.Fatalf("root PRM != /mcp PRM:\nroot %s\nmcp  %s", rb, mb)
	}
	if root["resource"] != ts.URL+"/mcp" {
		t.Fatalf("resource: %v", root["resource"])
	}
}

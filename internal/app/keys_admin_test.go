package app

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

var tokenRe = regexp.MustCompile(`id="new-token">(sk-[A-Za-z0-9_-]+)<`)

func TestKeyCreateShowsTokenOnceWithCopyButton(t *testing.T) {
	_, ts, br, csrf := signedIn(t, nil)
	r, page := br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"svc"}})
	m := tokenRe.FindStringSubmatch(page)
	if r.StatusCode != 200 || m == nil {
		t.Fatalf("token not shown: %d", r.StatusCode)
	}
	if !strings.Contains(page, `data-copy="#new-token"`) || !strings.Contains(page, ">Copy</button>") {
		t.Error("one-time token needs a Copy button")
	}
	if _, again := br.get("/admin/keys"); strings.Contains(again, m[1]) {
		t.Error("token must be shown only once")
	}
	r2, _ := http.Get(ts.URL + "/admin/static/app.js")
	js, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	for _, want := range []string{"navigator.clipboard", "execCommand", "data-copy"} {
		if !strings.Contains(string(js), want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
}

func TestKeyRegenerateFlow(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	_, page := br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"svc"}})
	old := tokenRe.FindStringSubmatch(page)[1]
	ks, _ := a.Keys.List()
	id := ks[0].ID
	idS := url.Values{"id": {itoa(id)}}
	// POST + CSRF only
	if r, _ := br.get("/admin/keys/regenerate?id=" + itoa(id)); r.StatusCode != 405 {
		t.Errorf("GET: %d", r.StatusCode)
	}
	if r, _ := br.post("/admin/keys/regenerate", idS); r.StatusCode != 403 {
		t.Errorf("no csrf: %d", r.StatusCode)
	}
	idS.Set("csrf", csrf)
	r, page := br.post("/admin/keys/regenerate", idS)
	m := tokenRe.FindStringSubmatch(page)
	if r.StatusCode != 200 || m == nil || m[1] == old {
		t.Fatalf("regenerate: %d, new token %v", r.StatusCode, m)
	}
	if !strings.Contains(page, `data-copy="#new-token"`) {
		t.Error("regenerated token needs a Copy button")
	}
	if _, ok := a.Keys.Verify(old); ok {
		t.Error("old token still works")
	}
	if k, ok := a.Keys.Verify(m[1]); !ok || k.ID != id || k.Label != "svc" {
		t.Error("new token must verify as the same record")
	}
	if l, _ := a.Keys.List(); len(l) != 1 {
		t.Errorf("regenerate must not add records: %d", len(l))
	}
	if !strings.Contains(page, "/admin/keys/regenerate") || !strings.Contains(page, "/admin/keys/revoke") {
		t.Error("keys list needs regenerate and revoke actions")
	}
	if _, again := br.get("/admin/keys"); strings.Contains(again, m[1]) {
		t.Error("token shown again")
	}
	// unknown key: error toast via redirect
	r, _ = br.post("/admin/keys/regenerate", url.Values{"csrf": {csrf}, "id": {"999"}})
	if r.StatusCode != 303 || flashKind(r) != "bad" {
		t.Errorf("unknown id: %d %q", r.StatusCode, flashKind(r))
	}
}

func TestKeyRevokeRemovesNeverUsedKey(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"x"}})
	ks, _ := a.Keys.List()
	r, _ := br.post("/admin/keys/revoke", url.Values{"csrf": {csrf}, "id": {itoa(ks[0].ID)}})
	if r.StatusCode != 303 || r.Header.Get("Location") != "/admin/keys" || flashKind(r) != "ok" {
		t.Fatalf("revoke: %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	if l, _ := a.Keys.List(); len(l) != 0 {
		t.Fatalf("never-used revoked key must be deleted: %+v", l)
	}
}

func TestClientsPageShowsLastUsed(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	br.post("/admin/clients/create", url.Values{"csrf": {csrf}, "name": {"fresh"}, "redirects": {"https://c.example/cb"}, "method": {"none"}})
	_, page := br.get("/admin/clients")
	if !strings.Contains(page, "<th>last used</th>") || !regexp.MustCompile(`<th>last used</th><td>-</td>`).MatchString(page) {
		t.Fatalf("never-used client must show a dash:\n%s", page)
	}
	l, _ := a.MCP.Clients.List()
	a.MCP.Clients.Touch(l[0].ID)
	_, page = br.get("/admin/clients")
	if strings.Contains(page, "<th>last used</th><td>-</td>") {
		t.Fatal("used client still shows a dash")
	}
	if !strings.Contains(page, time.Now().Format("2006-01-02")) {
		t.Fatal("last used time not rendered")
	}
}

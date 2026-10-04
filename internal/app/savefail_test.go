package app

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A write the database refuses must not be reported as done.
func TestAdminReportsAFailedDelete(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	if _, err := a.DB.Exec(`DROP TABLE oauth_tokens`); err != nil {
		t.Fatal(err)
	}
	r, _ := br.post("/admin/clients/delete", url.Values{"csrf": {csrf}, "id": {"nobody"}})
	if flashKind(r) != "bad" {
		t.Errorf("client delete with a broken database was reported as %q", flashKind(r))
	}
	r, _ = br.post("/admin/upstreams/nope/delete", url.Values{"csrf": {csrf}})
	if flashKind(r) != "bad" {
		t.Errorf("deleting an unknown upstream was reported as %q", flashKind(r))
	}
}

// Signing out ends the session on the server too: a copied cookie stops working, and a link cannot sign anyone out.
func TestLogoutRevokesTheCookieAndNeedsAPost(t *testing.T) {
	_, ts, idp := newApp(t, nil)
	br := newBrowser(t, ts)
	br.sso(idp, "/admin")
	u, _ := url.Parse(ts.URL)
	var copied *http.Cookie
	for _, c := range br.c.Jar.Cookies(u) {
		if c.Name == "skgate_session" {
			copied = c
		}
	}
	if copied == nil {
		t.Fatal("no session cookie")
	}
	if r, _ := br.get("/admin/logout"); r.StatusCode != 303 || r.Header.Get("Location") != "/admin" {
		t.Errorf("GET logout: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	_, page := br.get("/admin")
	csrf := between(page, `name="csrf" value="`, `"`)
	if csrf == "" {
		t.Fatal("a GET to /admin/logout ended the session")
	}
	br.post("/admin/logout", url.Values{"csrf": {csrf}})
	other := newBrowser(t, ts)
	other.c.Jar.SetCookies(u, []*http.Cookie{copied})
	if r, _ := other.get("/admin"); r.StatusCode != 302 {
		t.Errorf("a copied cookie still works after logout: %d", r.StatusCode)
	}
}

// An upstream is edited through its own address; the generic save address refuses an edit. (The test browser maps
// one onto the other, so this goes around it.)
func TestEditThroughTheGenericAddressIsRefused(t *testing.T) {
	_, _, br, csrf := signedIn(t, nil)
	req, _ := http.NewRequest("POST", br.ts.URL+"/admin/upstreams/save", strings.NewReader(url.Values{"csrf": {csrf}, "mode": {"edit"}, "alias": {"x"}, "url": {"http://127.0.0.1:1/mcp"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r, _ := br.do(req)
	if flashKind(r) != "bad" {
		t.Errorf("edit through /admin/upstreams/save: %d %q", r.StatusCode, flashKind(r))
	}
}

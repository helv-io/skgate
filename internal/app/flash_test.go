package app

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestFlashToastRendersOnceAndIsSigned(t *testing.T) {
	_, ts, br, csrf := signedIn(t, nil)
	r, _ := br.post("/admin/upstreams/nope/delete", url.Values{"csrf": {csrf}})
	if r.StatusCode != 303 || r.Header.Get("Location") != "/admin/upstreams" {
		t.Fatalf("delete: %d %q (no query params allowed)", r.StatusCode, r.Header.Get("Location"))
	}
	var fc *http.Cookie
	for _, c := range r.Cookies() {
		if c.Name == "skgate_flash" {
			fc = c
		}
	}
	if fc == nil || !fc.HttpOnly || fc.MaxAge <= 0 || fc.MaxAge > 120 {
		t.Fatalf("flash cookie must be HttpOnly and short-lived: %+v", fc)
	}
	_, page := br.get("/admin/upstreams")
	if !strings.Contains(page, `<div class="toast ok">upstream deleted</div>`) {
		t.Fatalf("toast not rendered:\n%s", page)
	}
	if _, again := br.get("/admin/upstreams"); strings.Contains(again, `class="toast`) {
		t.Fatal("flash must be consumed by the first render")
	}
	// query params are not notifications any more
	if _, p := br.get("/admin/upstreams?ok=hello&err=boom"); strings.Contains(p, "hello") || strings.Contains(p, "boom") || strings.Contains(p, `class="toast`) {
		t.Fatal("?ok= and ?err= must be ignored")
	}
	// a forged or tampered cookie shows nothing
	for _, v := range []string{"eyJrIjoib2siLCJtIjoiaGkifQ.AAAA", "garbage", fc.Value[:len(fc.Value)-2] + "xx"} {
		req, _ := http.NewRequest("GET", ts.URL+"/admin/keys", nil)
		req.AddCookie(&http.Cookie{Name: "skgate_flash", Value: v})
		_, p := br.do(req)
		if strings.Contains(p, `class="toast`) {
			t.Errorf("forged flash %q rendered", v)
		}
	}
}

func TestFlashErrorToastAndEscaping(t *testing.T) {
	_, _, br, csrf := signedIn(t, nil)
	r, _ := br.post("/admin/clients/create", url.Values{"csrf": {csrf}, "redirects": {"http://evil.example/<b>"}, "method": {"none"}})
	if r.StatusCode != 303 || r.Header.Get("Location") != "/admin/clients" {
		t.Fatalf("%d %q", r.StatusCode, r.Header.Get("Location"))
	}
	if k, _ := flashOf(r); k != "bad" {
		t.Fatalf("kind %q", k)
	}
	_, page := br.get("/admin/clients")
	if !strings.Contains(page, `class="toast bad"`) || strings.Contains(page, "<b>") {
		t.Fatalf("error toast missing or unescaped:\n%s", page)
	}
}

func TestNoNotificationInRedirectQuery(t *testing.T) {
	_, _, br, csrf := signedIn(t, nil)
	up := fakeUpstream(t)
	posts := []struct {
		path string
		v    url.Values
	}{
		{"/admin/providers/grok/settings", url.Values{"base": {"bad"}}},
		{"/admin/providers/grok/settings", url.Values{"base": {"https://a.example/v1"}}},
		{"/admin/providers/grok/aliases/put", url.Values{"name": {"x"}, "target": {"y"}}},
		{"/admin/providers/grok/model", url.Values{"model": {"y"}}},
		{"/admin/providers/grok/models/reload", url.Values{}},
		{"/admin/keys/update", url.Values{"id": {"999"}, "urlkey": {"1"}}},
		{"/admin/keys/revoke", url.Values{"id": {"999"}}},
		{"/admin/upstreams/save", url.Values{"mode": {"new"}, "alias": {"x"}, "url": {up.URL}, "auth_kind": {"none"}}},
		{"/admin/upstreams/save", url.Values{"mode": {"new"}, "alias": {"X!"}, "url": {up.URL}}},
		{"/admin/upstreams/x/redetect", url.Values{}},
		{"/admin/upstreams/zz/redetect", url.Values{}},
		{"/admin/upstreams/x/delete", url.Values{}},
		{"/admin/clients/create", url.Values{"method": {"none"}}},
		{"/admin/clients/delete", url.Values{"id": {"none"}}},
		{"/admin/providers/grok/refresh", url.Values{}},
		{"/admin/providers/grok/signout", url.Values{}},
		{"/admin/providers/grok/device/cancel", url.Values{}},
		{"/admin/providers/grok/browser/paste", url.Values{"callback": {"x"}}},
	}
	for _, p := range posts {
		p.v.Set("csrf", csrf)
		r, _ := br.post(p.path, p.v)
		loc := r.Header.Get("Location")
		if r.StatusCode != 303 {
			t.Errorf("%s: %d", p.path, r.StatusCode)
		}
		if strings.Contains(loc, "?") || strings.Contains(loc, "ok=") || strings.Contains(loc, "err=") {
			t.Errorf("%s redirects with a query notification: %q", p.path, loc)
		}
		if k, m := flashOf(r); k == "" || m == "" {
			t.Errorf("%s queued no toast", p.path)
		}
		br.get("/admin") // consume
	}
}

// Server-rendered toasts are popovers too, so they sit in the top layer above any dialog, and no stylesheet
// rule gives anything a z-index above the toast token.
func TestToastsAreInFrontOfEverything(t *testing.T) {
	_, _, br, csrf := signedIn(t, nil)
	br.post("/admin/upstreams/nope/delete", url.Values{"csrf": {csrf}})
	_, page := br.get("/admin/upstreams")
	if !strings.Contains(page, `<div class="toasts" popover="manual">`) {
		t.Errorf("the toast container must be a popover:\n%s", page)
	}
	css, _ := os.ReadFile(filepath.Join("..", "admin", "static", "app.css"))
	for _, m := range regexp.MustCompile(`z-index:\s*([^;}\s]+)`).FindAllStringSubmatch(string(css), -1) {
		if m[1] != "var(--z-toast)" {
			v := m[1]
			if tok := regexp.MustCompile(`^var\((--z-[a-z]+)\)$`).FindStringSubmatch(v); tok != nil { // a layer token: its value counts
				if d := regexp.MustCompile(tok[1] + `:(\d+)`).FindStringSubmatch(string(css)); d != nil {
					v = d[1]
				}
			}
			if n, err := strconv.Atoi(v); err != nil || n >= 1000 {
				t.Errorf("z-index %s competes with the toast layer", m[1])
			}
		}
	}
}

func TestToastAssets(t *testing.T) {
	_, ts, _, _ := signedIn(t, nil)
	get := func(p string) string {
		r, _ := http.Get(ts.URL + p)
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		return string(b)
	}
	css, js := get("/admin/static/app.css"), get("/admin/static/app.js")
	for _, want := range []string{".toasts{position:fixed;bottom:", "z-index:var(--z-toast)", "--z-toast:2147483647", ".toast.ok", ".toast.bad"} {
		if !strings.Contains(css, want) {
			t.Errorf("css lacks %q", want)
		}
	}
	for _, want := range []string{"5000", `".toast"`, `"click"`, "showPopover", "skgateRaiseToasts"} {
		if !strings.Contains(js, want) {
			t.Errorf("js lacks %q", want)
		}
	}
}

package app

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestFlashToastRendersOnceAndIsSigned(t *testing.T) {
	_, ts, br, csrf := signedIn(t, nil)
	r, _ := br.post("/admin/upstreams/delete", url.Values{"csrf": {csrf}, "alias": {"nope"}})
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
		{"/admin/settings/query-key", url.Values{}},
		{"/admin/keys/revoke", url.Values{"id": {"999"}}},
		{"/admin/upstreams/save", url.Values{"mode": {"new"}, "alias": {"x"}, "url": {up.URL}, "auth_kind": {"none"}}},
		{"/admin/upstreams/save", url.Values{"mode": {"new"}, "alias": {"X!"}, "url": {up.URL}}},
		{"/admin/upstreams/redetect", url.Values{"alias": {"x"}}},
		{"/admin/upstreams/redetect", url.Values{"alias": {"zz"}}},
		{"/admin/upstreams/delete", url.Values{"alias": {"x"}}},
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

func TestToastAssets(t *testing.T) {
	_, ts, _, _ := signedIn(t, nil)
	get := func(p string) string {
		r, _ := http.Get(ts.URL + p)
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		return string(b)
	}
	css, js := get("/admin/static/app.css"), get("/admin/static/app.js")
	for _, want := range []string{".toasts{position:fixed;bottom:", ".toast.ok", ".toast.bad"} {
		if !strings.Contains(css, want) {
			t.Errorf("css lacks %q", want)
		}
	}
	for _, want := range []string{"5000", `".toast"`, `"click"`} {
		if !strings.Contains(js, want) {
			t.Errorf("js lacks %q", want)
		}
	}
}

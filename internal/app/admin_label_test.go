package app

import (
	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/oidctest"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAdminHeaderLabel(t *testing.T) {
	// preferred_username wins over email and sub
	_, _, br, _ := signedIn(t, func(c *config.Config, p *oidctest.Provider) { p.Username = "admin" })
	_, page := br.get("/admin")
	if !strings.Contains(page, `<span class="who">admin</span>`) || strings.Contains(page, `>u-1<`) {
		t.Fatalf("who label: %s", between(page, `<header>`, `</header>`))
	}
	// info-only providers (claims only in userinfo) still give a label
	_, _, br, _ = signedIn(t, func(c *config.Config, p *oidctest.Provider) { p.Username = "fromuserinfo"; p.InfoOnly = true })
	if _, page = br.get("/admin"); !strings.Contains(page, `<span class="who">fromuserinfo</span>`) {
		t.Fatal("label from userinfo")
	}
	// no preferred_username: email, then the subject as a last resort
	_, _, br, _ = signedIn(t, nil)
	if _, page = br.get("/admin"); !strings.Contains(page, `<span class="who">admin@example.com</span>`) {
		t.Fatal("email fallback")
	}
	_, _, br, _ = signedIn(t, func(c *config.Config, p *oidctest.Provider) { p.Email = "" })
	if _, page = br.get("/admin"); !strings.Contains(page, `<span class="who">u-1</span>`) {
		t.Fatal("subject last resort")
	}
}

// The label lives in the signed cookie, so a restart (fresh App and empty in-memory session
// store, same database and cookie) still shows it.
func TestAdminLabelSurvivesRestart(t *testing.T) {
	a, ts, br, _ := signedIn(t, func(c *config.Config, p *oidctest.Provider) { p.Username = "admin" })
	a2 := New(a.Cfg, a.DB)
	h := &swapHandler{h: a2.Handler()}
	ts2 := httptest.NewServer(h)
	t.Cleanup(ts2.Close)
	u, _ := url.Parse(ts.URL)
	req, _ := http.NewRequest("GET", ts2.URL+"/admin", nil)
	for _, c := range br.c.Jar.Cookies(u) {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), `<span class="who">admin</span>`) {
		t.Fatalf("after restart: %d %s", resp.StatusCode, b)
	}
}

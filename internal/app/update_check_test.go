package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/admin"
	"github.com/helv-io/skgate/internal/config"
)

// fakeGitHub answers the latest-release API with body at status and counts the calls.
func fakeGitHub(t *testing.T, status int, body string) (*httptest.Server, *int32) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("User-Agent") == "" {
			t.Errorf("the check is anonymous and names itself: %v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(ts.Close)
	return ts, &hits
}

func release(tag string, pre bool) string {
	return fmt.Sprintf(`{"tag_name":%q,"prerelease":%v,"draft":false}`, tag, pre)
}

func versionLink(page string) string {
	i := strings.Index(page, `<a class="ver`)
	if i < 0 {
		return ""
	}
	return page[i : i+strings.Index(page[i:], "</a>")+4]
}

// The version in the header always links to the repository; it turns yellow, with a tooltip, only when GitHub has a
// newer stable release. Every other outcome (same version, older, prerelease, errors, rate limit, junk) stays quiet.
func TestHeaderVersionLinkAndUpdateHint(t *testing.T) {
	a, _, br, _ := signedIn(t, nil)
	_, page := br.get("/admin")
	plain := versionLink(page)
	for _, want := range []string{`href="https://github.com/helv-io/skgate"`, `target="_blank"`, `rel="noopener noreferrer"`, ">v" + config.Version + "</a>"} {
		if !strings.Contains(plain, want) {
			t.Errorf("version link lacks %q: %s", want, plain)
		}
	}
	if strings.Contains(plain, " new") || strings.Contains(plain, "title=") {
		t.Errorf("no check configured, so nothing is flagged: %s", plain)
	}
	if a.Admin.Releases.Newer() != "" {
		t.Error("without a URL nothing is asked")
	}

	check := func(status int, body string) string {
		gh, _ := fakeGitHub(t, status, body)
		a.Admin.Releases = admin.NewReleaseWatch(gh.URL, config.Version)
		a.Admin.Releases.Refresh(context.Background())
		_, page := br.get("/admin")
		return versionLink(page)
	}
	if l := check(200, release("v99.0.0", false)); !strings.Contains(l, `class="ver new"`) || !strings.Contains(l, `title="Update available: v99.0.0"`) || !strings.Contains(l, `rel="noopener noreferrer"`) {
		t.Errorf("a newer release must turn the link yellow with a tooltip: %s", l)
	}
	quiet := map[string]struct {
		status int
		body   string
	}{
		"same version": {200, release("v"+config.Version, false)}, "older": {200, release("v0.0.1", false)},
		"prerelease": {200, release("v99.0.0-rc1", true)}, "prerelease tag": {200, release("v99.0.0-rc1", false)},
		"draft": {200, `{"tag_name":"v99.0.0","draft":true}`}, "rate limited": {403, `{"message":"API rate limit exceeded"}`},
		"too many": {429, ``}, "not found": {404, `{}`}, "server error": {500, `oops`}, "junk": {200, `<html>`}, "no tag": {200, `{}`}, "odd tag": {200, release("nightly", false)},
	}
	for name, c := range quiet {
		if l := check(c.status, c.body); strings.Contains(l, "new") || strings.Contains(l, "title=") || !strings.Contains(l, ">v"+config.Version+"</a>") {
			t.Errorf("%s: must stay quiet: %s", name, l)
		}
	}
	// unreachable
	a.Admin.Releases = admin.NewReleaseWatch("http://127.0.0.1:1/none", config.Version)
	a.Admin.Releases.Refresh(context.Background())
	if _, page := br.get("/admin"); strings.Contains(versionLink(page), "new") {
		t.Error("an unreachable GitHub must stay quiet")
	}
}

// A page never waits for GitHub: the first page starts one background check, the answer is cached for hours, and a
// failure is not retried at once nor does it erase a good answer.
func TestUpdateCheckIsBackgroundCachedAndRetriesSlowly(t *testing.T) {
	gh, hits := fakeGitHub(t, 200, release("v99.1.0", false))
	w := admin.NewReleaseWatch(gh.URL, config.Version)
	if got := w.Newer(); got != "" { // answered from memory: nothing known yet
		t.Fatalf("first call must not wait for the network: %q", got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for w.Newer() == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if w.Newer() != "v99.1.0" {
		t.Fatal("the background check never delivered")
	}
	for i := 0; i < 20; i++ {
		w.Newer()
	}
	if n := atomic.LoadInt32(hits); n != 1 {
		t.Fatalf("cached for hours, got %d calls", n)
	}
	// stale: one refresh at a time; a failing answer keeps the last good value and waits Retry
	var fail atomic.Bool
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "limited", 403)
			return
		}
		fmt.Fprint(w, release("v99.2.0", false))
	}))
	defer flaky.Close()
	f := admin.NewReleaseWatch(flaky.URL, config.Version)
	f.Refresh(context.Background())
	if f.Newer() != "v99.2.0" {
		t.Fatal("first answer")
	}
	fail.Store(true)
	f.Every, f.Retry = 0, time.Hour
	f.Refresh(context.Background()) // the failure
	f.Every = time.Hour
	if f.Newer() != "v99.2.0" {
		t.Error("a failed refresh must not forget the last good answer")
	}
}

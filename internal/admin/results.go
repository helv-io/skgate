package admin

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/helv-io/skgate/internal/httputil"
)

// A request that changes something never renders a page itself: it keeps what the next screen shows (a new key
// or client secret, the outcome of an import) here and redirects to GET /admin/results/<token>, so the screen
// can be refreshed and a refresh never repeats the change. Results live in memory for resultTTL, belong to the
// session that made them, and are gone after a restart. The token is not the secret it protects.
const (
	resultTTL = 10 * time.Minute
	resultMax = 100
)

type result struct {
	kind    string // "key", "client" or "import"
	data    any
	toast   toast  // shown on the first view only (empty message: none)
	session string // the CSRF token of the admin session
	expires time.Time
	seen    bool
}

type results struct {
	mu sync.Mutex
	m  map[string]*result
}

// stash keeps a result for the session of r and returns the URL of its screen.
func (a *Admin) stash(r *http.Request, kind string, data any, t toast) string {
	csrf, _ := a.Session(r)
	var b [16]byte
	_, _ = rand.Read(b[:])
	token := hex.EncodeToString(b[:])
	now := time.Now()
	a.results.mu.Lock()
	defer a.results.mu.Unlock()
	if a.results.m == nil {
		a.results.m = map[string]*result{}
	}
	for k, v := range a.results.m {
		if now.After(v.expires) {
			delete(a.results.m, k)
		}
	}
	for len(a.results.m) >= resultMax { // drop the oldest
		var oldest string
		for k, v := range a.results.m {
			if oldest == "" || v.expires.Before(a.results.m[oldest].expires) {
				oldest = k
			}
		}
		delete(a.results.m, oldest)
	}
	a.results.m[token] = &result{kind: kind, data: data, toast: t, session: csrf, expires: now.Add(resultTTL)}
	return "/admin/results/" + token
}

// showOnce answers a change whose result is shown once (a new key or client secret, an import's outcome, an update
// to review). Posted in place (Accept: application/json), the answer carries the dialog that shows it, rendered from
// the named template of the page set ({"toast", "show"}); the page script opens it over the page the person was on
// and drops it when it closes. The result is never kept for that answer, and it never travels in a URL. A plain form
// goes to the result screen as before. data is what the template and the screen read.
func (a *Admin) showOnce(w http.ResponseWriter, r *http.Request, set, name, kind string, data any, t toast) {
	if !wantsJSON(r) {
		http.Redirect(w, r, a.stash(r, kind, data, t), http.StatusSeeOther)
		return
	}
	a.showDialog(w, r, set, name, data, t)
}

// showDialog renders the dialog template name of the page set with data and answers it with the toast.
func (a *Admin) showDialog(w http.ResponseWriter, r *http.Request, set, name string, data any, t toast) {
	csrf, _ := a.Session(r)
	var b bytes.Buffer
	if err := a.tpl[set].ExecuteTemplate(&b, name, page{CSRF: csrf, Public: a.Cfg.PublicURL, Data: data}); err != nil {
		log.Printf("admin: dialog %s: %v", name, err)
		httputil.JSON(w, http.StatusOK, map[string]any{"toast": toast{toastBad, "skgate could not show the result, reload the page"}})
		return
	}
	if t.Kind == "" {
		t.Kind = toastOK
	}
	t.Msg = clip(t.Msg)
	httputil.JSON(w, http.StatusOK, map[string]any{"toast": t, "show": b.String()})
}

// stashToken keeps a result like stash and returns its token alone.
func (a *Admin) stashToken(r *http.Request, kind string, data any) string {
	return strings.TrimPrefix(a.stash(r, kind, data, toast{}), "/admin/results/")
}

// resultScreen renders a stashed result, again on every refresh until it expires.
func (a *Admin) resultScreen(w http.ResponseWriter, r *http.Request) {
	csrf, _ := a.Session(r)
	a.results.mu.Lock()
	res := a.results.m[r.PathValue("token")]
	if res != nil && (time.Now().After(res.expires) || subtle.ConstantTimeCompare([]byte(res.session), []byte(csrf)) != 1) {
		res = nil
	}
	var first bool
	if res != nil {
		first, res.seen = !res.seen, true
	}
	a.results.mu.Unlock()
	if res == nil {
		a.back(w, r, "/admin", "", "that result has expired")
		return
	}
	var toasts []toast
	if first && res.toast.Msg != "" {
		toasts = []toast{res.toast}
	}
	switch d := res.data.(type) {
	case keysData:
		ks, _ := a.Keys.List()
		d.Keys = ks
		a.render(w, r, "keys", page{Title: "Virtual keys", Nav: "keys", Toasts: toasts, Data: d})
	case clientsData:
		a.render(w, r, "clients", page{Title: "OAuth clients", Nav: "clients", Toasts: toasts, Data: a.fillClients(d)})
	case oaUpdateData:
		d.Token = r.PathValue("token")
		a.render(w, r, "upstream_spec_update", page{Title: "Update " + d.Alias, Nav: "upstreams", Toasts: toasts, Data: d})
	case importData:
		a.render(w, r, "upstream_import", page{Title: "Import upstreams", Nav: "upstreams", Toasts: toasts, Data: d})
	default:
		http.NotFound(w, r)
	}
}

package admin

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
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
	case importData:
		a.render(w, r, "upstream_import", page{Title: "Import upstreams", Nav: "upstreams", Toasts: toasts, Data: d})
	default:
		http.NotFound(w, r)
	}
}

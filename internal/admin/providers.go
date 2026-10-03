package admin

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/provider"
)

// dialogHash is the URL fragment that reopens a provider dialog after an action; it carries no
// message, notifications still travel as toasts.
func dialogHash(id string) string { return "/admin#provider-" + id }

// providerRoutes registers /admin/providers/<id>/<action>.
func (a *Admin) providerRoutes(mux *http.ServeMux) {
	act := map[string]http.HandlerFunc{
		"device/start":   a.deviceStart,
		"device/cancel":  a.deviceCancel,
		"device/state":   a.deviceState,
		"browser/start":  a.browserStart,
		"browser/paste":  a.browserPaste,
		"refresh":        a.providerRefresh,
		"signout":        a.providerSignOut,
		"settings":       a.providerSettings,
		"models/reload":  a.modelsReload,
		"model":          a.modelSelect,
		"chat-effort":    a.chatEffort,
		"aliases/put":    a.aliasPut,
		"aliases/delete": a.aliasDelete,
	}
	for name, h := range act {
		h := h
		post := name != "device/state"
		hh := a.guard(func(w http.ResponseWriter, r *http.Request) {
			p, ok := a.Providers.Get(r.PathValue("id"))
			if !ok {
				http.NotFound(w, r)
				return
			}
			if post && r.Method != http.MethodPost {
				http.Error(w, "POST only", http.StatusMethodNotAllowed)
				return
			}
			h(w, r.WithContext(context.WithValue(r.Context(), providerKey{}, p)))
		})
		mux.HandleFunc("/admin/providers/{id}/"+name, hh)
	}
}

type providerKey struct{}

func providerOf(r *http.Request) provider.Provider {
	return r.Context().Value(providerKey{}).(provider.Provider)
}

func (a *Admin) deviceStart(w http.ResponseWriter, r *http.Request) {
	if _, err := providerOf(r).StartDevice(r.Context()); err != nil {
		a.back(w, r, "/admin", "", err.Error())
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *Admin) deviceCancel(w http.ResponseWriter, r *http.Request) {
	providerOf(r).CancelDevice()
	a.back(w, r, "/admin", "device sign-in cancelled", "")
}

func (a *Admin) deviceState(w http.ResponseWriter, r *http.Request) {
	p := providerOf(r)
	d := p.Device()
	httputil.JSON(w, 200, map[string]any{"state": d.State, "error": d.Err, "signed_in": p.Status().SignedIn})
}

func (a *Admin) browserStart(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, providerOf(r).StartBrowser(r.Context()), http.StatusSeeOther)
}

func (a *Admin) browserPaste(w http.ResponseWriter, r *http.Request) {
	p := providerOf(r)
	code, state := provider.ParseCallback(r.PostFormValue("callback"))
	if err := p.FinishBrowser(r.Context(), code, state); err != nil {
		a.back(w, r, dialogHash(p.ID()), "", err.Error())
		return
	}
	a.back(w, r, "/admin", "signed in to "+p.Name(), "")
}

// browserCallback receives the redirect of a browser sign-in for whichever provider started one.
func (a *Admin) browserCallback(w http.ResponseWriter, r *http.Request) {
	p, ok := a.Providers.Pending()
	if !ok {
		p = a.Providers.Default()
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		a.back(w, r, "/admin", "", "authorization failed: "+e)
		return
	}
	if err := p.FinishBrowser(r.Context(), q.Get("code"), q.Get("state")); err != nil {
		a.back(w, r, "/admin", "", err.Error())
		return
	}
	a.back(w, r, "/admin", "signed in to "+p.Name(), "")
}

func (a *Admin) providerRefresh(w http.ResponseWriter, r *http.Request) {
	p := providerOf(r)
	if err := p.Refresh(r.Context()); err != nil {
		a.back(w, r, dialogHash(p.ID()), "", "refresh failed: "+err.Error())
		return
	}
	a.back(w, r, dialogHash(p.ID()), "token refreshed", "")
}

func (a *Admin) providerSignOut(w http.ResponseWriter, r *http.Request) {
	p := providerOf(r)
	p.SignOut()
	a.back(w, r, "/admin", p.Name()+" signed out", "")
}

// providerSettings saves the provider's upstream base and fallback. The pair is validated together;
// trailing slashes are dropped silently.
func (a *Admin) providerSettings(w http.ResponseWriter, r *http.Request) {
	p := providerOf(r)
	up, fb := cleanBase(r.PostFormValue("base")), cleanBase(r.PostFormValue("fallback"))
	if up == "" {
		up = p.DefaultBase()
	}
	if !validBase(up) || (fb != "" && !validBase(fb)) {
		a.back(w, r, dialogHash(p.ID()), "", "URLs must be absolute http(s)")
		return
	}
	_ = a.Set.Set(p.ID(), "base", up)
	_ = a.Set.Set(p.ID(), "fallback", fb)
	a.back(w, r, dialogHash(p.ID()), "upstream saved", "")
}

func (a *Admin) queryKeyToggle(w http.ResponseWriter, r *http.Request) {
	v := "1"
	if a.Cfg.QueryKeyAllowed() {
		v = "0"
	}
	_ = a.DB.SetSetting("allow_query_key", v)
	msg := "?key= accepted on MCP endpoints"
	if v == "0" {
		msg = "?key= refused on MCP endpoints"
	}
	a.back(w, r, "/admin/keys", msg, "")
}

// ---- models ----

// modelTries throttles on-demand model loads per provider.
type modelTries struct {
	mu sync.Mutex
	at map[string]time.Time
}

// allow reports whether a load may start now (at most one every 30 s per provider).
func (m *modelTries) allow(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.at == nil {
		m.at = map[string]time.Time{}
	}
	if time.Since(m.at[id]) < 30*time.Second {
		return false
	}
	m.at[id] = time.Now()
	return true
}

// models returns the provider's model ids: the cache, loaded on demand (at most every 30 s per
// provider, short timeout) when the account is signed in and nothing is cached yet.
func (a *Admin) models(ctx context.Context, p provider.Provider) (ids []string, at time.Time, known bool) {
	px := a.proxyFor(p.ID())
	if px == nil {
		return nil, time.Time{}, false
	}
	if ids, at, ok := px.Models.Get(p.ID()); ok {
		return ids, at, true
	}
	if !p.Status().SignedIn {
		return nil, time.Time{}, false
	}
	if !a.tries.allow(p.ID()) {
		return nil, time.Time{}, false
	}
	c, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if ids, err := px.FetchModels(c); err == nil {
		return ids, time.Now(), true
	}
	return nil, time.Time{}, false
}

func (a *Admin) proxyFor(id string) *provider.Proxy {
	if a.Proxy != nil && a.Proxy.Backend.ID() == id {
		return a.Proxy
	}
	return nil
}

// wantsJSON reports whether the client script asked for an in-place answer.
func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

// helperDone answers a change of the MCP helper model setup. The page script posts with
// Accept: application/json and gets the toast plus the re-rendered Suggest controls; a plain form
// post is redirected back to the provider dialog with the toast as a flash.
func (a *Admin) helperDone(w http.ResponseWriter, r *http.Request, id, ok, errMsg string) {
	if !wantsJSON(r) {
		a.back(w, r, dialogHash(id), ok, errMsg)
		return
	}
	t := toast{toastOK, ok}
	if errMsg != "" {
		log.Printf("admin: %s %s refused: %s", r.Method, r.URL.Path, clip(errMsg))
		t = toast{toastBad, errMsg}
	}
	t.Msg = clip(t.Msg)
	html, err := a.controlsHTML(r)
	if err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]any{"toast": t, "html": html})
}

func (a *Admin) modelsReload(w http.ResponseWriter, r *http.Request) {
	p := providerOf(r)
	px := a.proxyFor(p.ID())
	if px == nil {
		a.helperDone(w, r, p.ID(), "", "models are not available for this provider")
		return
	}
	ids, err := px.FetchModels(r.Context())
	if err != nil {
		a.helperDone(w, r, p.ID(), "", err.Error())
		return
	}
	a.helperDone(w, r, p.ID(), fmt.Sprintf("%d models loaded", len(ids)), "")
}

func (a *Admin) modelSelect(w http.ResponseWriter, r *http.Request) {
	p := providerOf(r)
	m := r.PostFormValue("model")
	if m != "" {
		ids, _, known := a.models(r.Context(), p)
		if !known || !contains(ids, m) {
			a.helperDone(w, r, p.ID(), "", "not one of the provider's models")
			return
		}
	}
	effort := r.PostFormValue("effort")
	if effort != "" && !provider.ValidEffort(effort) {
		a.helperDone(w, r, p.ID(), "", "not a valid effort")
		return
	}
	timeout := 0
	if t := r.PostFormValue("timeout"); t != "" {
		n, err := provider.ParseTimeout(t)
		if err != nil {
			a.helperDone(w, r, p.ID(), "", err.Error())
			return
		}
		timeout = n
	}
	_ = a.Set.SetModel(p.ID(), m)
	msg := "MCP helper model cleared"
	if m != "" {
		msg = "MCP helper model: " + m
	}
	if effort != "" {
		_ = a.Set.SetEffort(p.ID(), effort)
		msg += ", effort " + effort
	}
	if timeout > 0 {
		_ = a.Set.SetHelperTimeout(p.ID(), timeout)
		msg += ", timeout " + strconv.Itoa(timeout) + " s"
	}
	a.helperDone(w, r, p.ID(), msg, "")
}

// chatEffort stores the reasoning effort added to proxied chat requests that set none.
func (a *Admin) chatEffort(w http.ResponseWriter, r *http.Request) {
	p := providerOf(r)
	effort := r.PostFormValue("effort")
	if !provider.ValidEffort(effort) {
		a.back(w, r, dialogHash(p.ID()), "", "not a valid effort")
		return
	}
	_ = a.Set.SetChatEffort(p.ID(), effort)
	a.back(w, r, dialogHash(p.ID()), "chat effort: "+effort, "")
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func (a *Admin) aliasPut(w http.ResponseWriter, r *http.Request) {
	p := providerOf(r)
	ids, _, _ := a.models(r.Context(), p)
	if err := a.Set.PutAlias(p.ID(), r.PostFormValue("name"), r.PostFormValue("target"), ids); err != nil {
		a.back(w, r, dialogHash(p.ID()), "", err.Error())
		return
	}
	a.back(w, r, dialogHash(p.ID()), "alias saved", "")
}

func (a *Admin) aliasDelete(w http.ResponseWriter, r *http.Request) {
	p := providerOf(r)
	_ = a.Set.DeleteAlias(p.ID(), r.PostFormValue("name"))
	a.back(w, r, dialogHash(p.ID()), "alias removed", "")
}

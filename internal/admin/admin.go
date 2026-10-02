// Package admin is the small server-rendered admin UI.
package admin

import (
	"crypto/subtle"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"time"

	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/managed"
	"github.com/helv-io/skgate/internal/mcp"
	"github.com/helv-io/skgate/internal/numfmt"
	"github.com/helv-io/skgate/internal/oidcauth"
	"github.com/helv-io/skgate/internal/provider"
	"github.com/helv-io/skgate/internal/reqlog"
	"github.com/helv-io/skgate/internal/store"
	"github.com/helv-io/skgate/internal/suggest"
	"github.com/helv-io/skgate/internal/vkeys"
)

//go:embed templates/*.html static/*
var assets embed.FS

// Admin holds dependencies for the UI handlers.
type Admin struct {
	Cfg       *config.Config
	DB        *store.DB
	Providers *provider.Registry
	Proxy     *provider.Proxy
	Set       provider.Settings
	tries     modelTries
	// SuggestFetch replaces the public fetcher (tests).
	SuggestFetch *suggest.Fetcher
	Keys         *vkeys.Manager
	MCP          *mcp.Server
	tpl          map[string]*template.Template
	oidc         *oidcauth.Client
	sessions     sessionStore
}

var funcs = template.FuncMap{
	"ago": func(t time.Time) string {
		if t.IsZero() {
			return "never"
		}
		d := time.Since(t).Round(time.Second)
		return d.String() + " ago"
	},
	"ts": func(t time.Time) string {
		if t.IsZero() {
			return "-"
		}
		return t.Local().Format("2006-01-02 15:04")
	},
	"dur":  func(d time.Duration) string { return d.Round(time.Second).String() },
	"list": func(v ...string) []string { return v },
	// pairRow and pair feed the "pair_row" component (see templates/components.html).
	"pairRow": func(l pairList, r pair, removable bool) pairRowData {
		return pairRowData{NameKey: l.NameKey, ValueKey: l.ValueKey, NamePH: l.NamePH, Row: r, Removable: removable}
	},
	"pair":   func() pair { return pair{} },
	"dlg":    func(id, title string) dialogHead { return dialogHead{ID: id, Title: title} },
	"inList": contains,
	// pill feeds the "pill" component: class, label and hover text.
	"pill": func(class, text, tip string) pillView { return pillView{Class: class, Text: text, Tip: tip} },
	// tip feeds the "tip" component: visible text with a hover tooltip. usage builds the Usage cell of a key.
	"tip":   func(text, tip string) tipView { return tipView{Text: text, Tip: tip} },
	"usage": usageCell,
	"rowItem": func(l rowList, v string, removable bool) rowItemData {
		return rowItemData{Key: l.Key, PH: l.PH, Value: v, Removable: removable}
	},
}

// New builds the Admin and parses templates.
func New(cfg *config.Config, db *store.DB, reg *provider.Registry, px *provider.Proxy, k *vkeys.Manager, m *mcp.Server) *Admin {
	a := &Admin{Cfg: cfg, DB: db, Providers: reg, Proxy: px, Set: provider.Settings{KV: db}, Keys: k, MCP: m, tpl: map[string]*template.Template{}}
	for _, p := range []string{"status", "keys", "upstreams", "upstream_edit", "upstream_test", "upstream_logs", "upstream_import", "clients", "signedout", "autherror", "notconfigured"} {
		a.tpl[p] = template.Must(template.New(p).Funcs(funcs).ParseFS(assets, "templates/layout.html", "templates/components.html", "templates/provider.html", "templates/upstream_form.html", "templates/"+p+".html"))
	}
	if cfg.OIDCEnabled() {
		a.oidc = oidcauth.New(oidcauth.Config{
			Issuer: cfg.OIDCIssuer, ClientID: cfg.OIDCClientID, ClientSecret: cfg.OIDCClientSecret,
			Scopes: cfg.OIDCScopes, AllowedEmails: cfg.OIDCEmails, AllowedGroups: cfg.OIDCGroups,
		}, a.secret, cfg.OIDCRedirect, a.secure)
	}
	m.AdminSession = a.Session
	m.AdminIdentity = a.Identity
	return a
}

type page struct {
	Title, Nav, CSRF, User, Version string
	// Toasts are transient notices (flash from the previous request plus any set by the handler).
	Toasts []toast
	Data   any
	Public string
}

func (a *Admin) render(w http.ResponseWriter, r *http.Request, name string, p page) {
	p.Public = a.Cfg.PublicURL
	p.Version = config.Version
	if csrf, ok := a.Session(r); ok {
		p.CSRF = csrf
		p.User = a.Label(r)
	}
	if t, ok := a.takeFlash(w, r); ok {
		p.Toasts = append(p.Toasts, t)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; frame-ancestors 'none'; form-action 'self'")
	if err := a.tpl[name].ExecuteTemplate(w, "layout", p); err != nil {
		http.Error(w, "template error", 500)
	}
}

// guard wraps a handler requiring a session; POSTs also require a valid CSRF token.
func (a *Admin) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.oidc == nil {
			a.notConfigured(w, r)
			return
		}
		csrf, ok := a.Session(r)
		if !ok {
			a.startLogin(w, r)
			return
		}
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			if r.ParseForm() != nil || subtle.ConstantTimeCompare([]byte(csrf), []byte(r.PostFormValue("csrf"))) != 1 {
				http.Error(w, "invalid CSRF token", http.StatusForbidden)
				return
			}
		}
		h(w, r)
	}
}

// postOnly rejects anything but POST (guard already CSRF-checks POSTs), so these actions can never
// be triggered by a plain link or GET request.
func (a *Admin) postOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}

// startLogin sends an unauthenticated browser straight into the OIDC authorization flow (no welcome
// page). Only GET and HEAD requests can be resumed after login, so anything else returns to /admin.
func (a *Admin) startLogin(w http.ResponseWriter, r *http.Request) {
	next := "/admin"
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		next = safeNext(r.URL.RequestURI())
	}
	http.Redirect(w, r, "/admin/oidc/login?next="+url.QueryEscape(next), http.StatusFound)
}

// safeNext validates a post-login target. Only same-origin relative paths are accepted, and only
// the admin UI or the MCP /authorize endpoint (so an OAuth flow can resume). Anything else, such as
// absolute URLs, protocol-relative URLs, backslashes, control characters or other paths, becomes /admin.
func safeNext(n string) string {
	if n == "" || len(n) > 3000 || n[0] != '/' || strings.HasPrefix(n, "//") || strings.ContainsAny(n, "\\\r\n\t") {
		return "/admin"
	}
	for _, c := range n {
		if c < 0x20 || c == 0x7f {
			return "/admin"
		}
	}
	u, err := url.Parse(n)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "/admin"
	}
	switch {
	case u.Path == "/authorize", u.Path == "/admin":
		return n
	case strings.HasPrefix(u.Path, "/admin/") && !strings.HasPrefix(u.Path, "/admin/oidc/") &&
		u.Path != "/admin/logout" && u.Path != "/admin/login" && u.Path != "/admin/signed-out" &&
		!strings.Contains(u.Path, ".."):
		return n
	}
	return "/admin"
}

// Routes registers admin routes.
func (a *Admin) Routes(mux *http.ServeMux) {
	mux.Handle("/admin/static/", http.StripPrefix("/admin/", http.FileServerFS(assets)))
	// Browser icons live in static/ (replace the files to change them). Exact paths, no login.
	for _, name := range []string{"favicon.svg", "favicon.ico", "apple-touch-icon.png"} {
		name := name
		mux.HandleFunc("GET /"+name, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age=86400")
			http.ServeFileFS(w, r, assets, "static/"+name)
		})
	}
	mux.HandleFunc("/admin/login", a.login)
	mux.HandleFunc("/admin/oidc/login", a.oidcLogin)
	mux.HandleFunc("/admin/oidc/callback", a.oidcCallback)
	mux.HandleFunc("/admin/logout", a.logout)
	mux.HandleFunc("/admin/signed-out", a.signedOut)
	mux.HandleFunc("/admin", a.guard(a.status))
	mux.HandleFunc("/admin/", http.NotFound)
	a.providerRoutes(mux)
	mux.HandleFunc("/admin/oauth/callback", a.guard(a.browserCallback))
	mux.HandleFunc("/admin/settings/query-key", a.guard(a.postOnly(a.queryKeyToggle)))
	mux.HandleFunc("/admin/keys", a.guard(a.keys))
	mux.HandleFunc("/admin/keys/create", a.guard(a.keyCreate))
	mux.HandleFunc("/admin/keys/revoke", a.guard(a.postOnly(a.keyRevoke)))
	mux.HandleFunc("/admin/keys/regenerate", a.guard(a.postOnly(a.keyRegenerate)))
	mux.HandleFunc("/admin/upstreams", a.guard(a.upstreams))
	mux.HandleFunc("/admin/upstreams/edit", a.guard(a.upstreamEdit))
	mux.HandleFunc("/admin/upstreams/save", a.guard(a.upstreamSave))
	mux.HandleFunc("/admin/upstreams/delete", a.guard(a.upstreamDelete))
	mux.HandleFunc("/admin/upstreams/toggle", a.guard(a.postOnly(a.upstreamToggle)))
	mux.HandleFunc("/admin/upstreams/redetect", a.guard(a.postOnly(a.upstreamRedetect)))
	mux.HandleFunc("/admin/upstreams/test", a.guard(a.postOnly(a.upstreamTest)))
	mux.HandleFunc("/admin/upstreams/process", a.guard(a.postOnly(a.upstreamProcess)))
	mux.HandleFunc("/admin/upstreams/logs", a.guard(a.upstreamLogs))
	mux.HandleFunc("/admin/upstreams/suggest", a.guard(a.postOnly(a.upstreamSuggest)))
	mux.HandleFunc("/admin/upstreams/import", a.guard(a.upstreamImport))
	mux.HandleFunc("/admin/upstreams/export", a.guard(a.upstreamExport))
	mux.HandleFunc("/admin/clients", a.guard(a.clients))
	mux.HandleFunc("/admin/clients/create", a.guard(a.clientCreate))
	mux.HandleFunc("/admin/clients/delete", a.guard(a.clientDelete))
}

func (a *Admin) notConfigured(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusServiceUnavailable)
	a.render(w, r, "notconfigured", page{Title: "OIDC not configured"})
}

// login is kept as an alias: it never renders a welcome page, it goes straight to the OIDC flow
// (or to the target when a session already exists).
func (a *Admin) login(w http.ResponseWriter, r *http.Request) {
	if a.oidc == nil {
		a.notConfigured(w, r)
		return
	}
	next := safeNext(r.URL.Query().Get("next"))
	if _, ok := a.Session(r); ok {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/oidc/login?next="+url.QueryEscape(next), http.StatusFound)
}

// authError renders a plain error page with a manual "Sign in" link. It never redirects, so a
// denied account cannot bounce between skgate and the identity provider.
func (a *Admin) authError(w http.ResponseWriter, r *http.Request, code int, msg string) {
	w.WriteHeader(code)
	a.render(w, r, "autherror", page{Title: "Sign-in error", Data: msg})
}

func (a *Admin) oidcLogin(w http.ResponseWriter, r *http.Request) {
	if a.oidc == nil {
		a.notConfigured(w, r)
		return
	}
	if err := a.oidc.Begin(w, r, safeNext(r.URL.Query().Get("next"))); err != nil {
		reqlog.Reject(r, "upstream unreachable: identity provider discovery failed: %s", reqlog.Sanitize(err))
		a.authError(w, r, http.StatusBadGateway, "identity provider unavailable: "+err.Error())
	}
}

func (a *Admin) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if a.oidc == nil {
		a.notConfigured(w, r)
		return
	}
	id, next, err := a.oidc.Finish(w, r)
	if err != nil {
		code := http.StatusUnauthorized
		msg := "sign-in failed: " + err.Error()
		reqlog.Reject(r, "OIDC sign-in failed: %s", reqlog.Sanitize(err))
		if errors.Is(err, oidcauth.ErrForbidden) {
			code = http.StatusForbidden
			msg = id.Display() + " is not allowed"
		}
		a.authError(w, r, code, msg)
		return
	}
	a.setSession(w, id)
	http.Redirect(w, r, safeNext(next), http.StatusSeeOther)
}

// signedOut is the public landing page after logout. It shows a manual "Sign in" link and never
// redirects, which is what prevents a logout to login to IdP to admin loop.
func (a *Admin) signedOut(w http.ResponseWriter, r *http.Request) {
	if a.oidc == nil {
		a.notConfigured(w, r)
		return
	}
	a.render(w, r, "signedout", page{Title: "Signed out"})
}

// logout (POST needs the CSRF token) clears the session and, if the IdP advertises end_session_endpoint,
// continues there; otherwise it shows the signed-out page. Without a session it just shows that page.
func (a *Admin) logout(w http.ResponseWriter, r *http.Request) {
	if a.oidc == nil {
		a.notConfigured(w, r)
		return
	}
	csrf, ok := a.Session(r)
	if !ok {
		http.Redirect(w, r, "/admin/signed-out", http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodPost { // same rule as guard: POSTs need the CSRF token
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if r.ParseForm() != nil || subtle.ConstantTimeCompare([]byte(csrf), []byte(r.PostFormValue("csrf"))) != 1 {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}
	}
	nonce, si := a.sessionWho(r)
	a.sessions.del(nonce)
	a.clearSession(w)
	if u := a.oidc.EndSessionURL(r.Context(), si.idToken, ""); u != "" {
		http.Redirect(w, r, u, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/signed-out", http.StatusSeeOther)
}

func validBase(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

// cleanBase trims blanks and trailing slashes; stored bases never carry either.
func cleanBase(s string) string { return strings.TrimRight(strings.TrimSpace(s), "/") }

type keysData struct {
	Keys     []vkeys.Key
	NewKey   string
	QueryKey bool
}

func (a *Admin) keys(w http.ResponseWriter, r *http.Request) {
	ks, _ := a.Keys.List()
	a.render(w, r, "keys", page{Title: "Virtual keys", Nav: "keys", Data: keysData{Keys: ks, QueryKey: a.Cfg.QueryKeyAllowed()}})
}

func (a *Admin) keyCreate(w http.ResponseWriter, r *http.Request) {
	full, _, err := a.Keys.Create(r.PostFormValue("label"))
	if err != nil {
		a.back(w, r, "/admin/keys", "", "create failed")
		return
	}
	ks, _ := a.Keys.List()
	a.render(w, r, "keys", page{Title: "Virtual keys", Nav: "keys", Toasts: []toast{{toastOK, "key created"}}, Data: keysData{Keys: ks, NewKey: full, QueryKey: a.Cfg.QueryKeyAllowed()}})
}

// keyRegenerate swaps the secret of an active key and shows the new one once (rendered directly,
// not redirected, so the token never sits in a cookie or URL).
func (a *Admin) keyRegenerate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	full, _, err := a.Keys.Regenerate(id)
	if err != nil {
		a.back(w, r, "/admin/keys", "", err.Error())
		return
	}
	ks, _ := a.Keys.List()
	a.render(w, r, "keys", page{Title: "Virtual keys", Nav: "keys", Toasts: []toast{{toastOK, "key regenerated"}}, Data: keysData{Keys: ks, NewKey: full, QueryKey: a.Cfg.QueryKeyAllowed()}})
}

func (a *Admin) keyRevoke(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	if err := a.Keys.Revoke(id); err != nil {
		a.back(w, r, "/admin/keys", "", err.Error())
		return
	}
	a.back(w, r, "/admin/keys", "key revoked", "")
}

type upstreamView struct {
	mcp.Upstream
	Masked string
	// Effective is the outbound auth in use ("auto" upstreams show what was detected).
	Effective string
	// Target is the URL of a remote upstream or the command line of a managed one.
	Target string
	// Tip is the hover text of the alias: type, target, source and revision, host override.
	Tip string
	// Proc is set for managed upstreams.
	Proc      *procView
	ProcClass string
}

// procView is the process status shown for a managed upstream.
type procView struct {
	mcp.ProcInfo
	Class string // pill class
	Text  string // pill label: the state word only
	Tip   string // hover text: pid, uptime, restarts, last error
	Upd   updView
}

// updView is the update state as display text.
type updView struct {
	Rev, Ref, RefKind, Remote string
	Available, Pinned         bool
	PinNote                   string
	Checked, CheckErr         string
	Package                   string
	Last, Err                 string
	Auto, Next                string
	Any                       bool // there is something to show
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("Jan 2 15:04 MST")
}

// everyText names an auto-update interval.
func everyText(d time.Duration) string {
	switch {
	case d <= 0:
		return "off"
	case d == time.Hour:
		return "hourly"
	case d == 24*time.Hour:
		return "daily"
	case d == 7*24*time.Hour:
		return "weekly"
	case d%time.Hour == 0:
		return fmt.Sprintf("every %d hours", int(d/time.Hour))
	}
	return fmt.Sprintf("every %d minutes", int(d/time.Minute))
}

func updViewOf(u managed.UpdateInfo) updView {
	v := updView{Rev: u.Rev, Ref: u.Ref, RefKind: u.RefKind, Remote: shortSHA(u.RemoteRev), Available: u.Available, Pinned: u.Pinned,
		PinNote: u.PinNote, Checked: stamp(u.Checked), CheckErr: u.CheckErr, Err: u.Err, Auto: everyText(u.AutoEvery), Next: stamp(u.AutoNext)}
	if u.Git && u.Ref == "" && u.Rev != "" {
		v.Ref = "default branch"
	}
	if u.HasPkg {
		v.Package = u.Package.Name
		if u.Package.Version != "" {
			v.Package += " " + u.Package.Version
		}
	}
	if u.LastUpdate != "" {
		v.Last = u.LastUpdate + ", " + stamp(u.LastAt)
	}
	if u.Err != "" {
		v.Err = u.Err + " (" + stamp(u.ErrAt) + ")"
	}
	v.Any = u.Git || u.HasPkg
	return v
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// procTip lists the details that do not fit in the pill.
func procTip(pi mcp.ProcInfo) string {
	if !pi.Available {
		return pi.Why
	}
	var parts []string
	if pi.State == "starting" && pi.Phase != "" {
		parts = append(parts, pi.Phase)
	}
	if pi.State == "stopped" && pi.Held {
		parts = append(parts, "stopped by an administrator")
	}
	if pi.PID > 0 {
		parts = append(parts, fmt.Sprintf("pid %d", pi.PID))
	}
	if pi.State == "running" {
		parts = append(parts, "up "+pi.Uptime().String())
	}
	if pi.Restarts > 0 {
		parts = append(parts, fmt.Sprintf("%d restarts", pi.Restarts))
	}
	if pi.LastErr != "" {
		parts = append(parts, "last error: "+pi.LastErr)
	}
	if len(parts) == 0 {
		return pi.State
	}
	return strings.Join(parts, " | ")
}

// upstreamsData is the upstreams page.
type upstreamsData struct {
	List    []upstreamView
	Aliases []string
	Form    formData // the add form
	Managed bool
	Why     string
}

// formData feeds the shared upstream form (add and edit).
type formData struct {
	U           upstreamView
	New         bool
	Managed     bool
	Why         string
	Include     bool
	Args        rowList
	Cmd         pickList
	Env, Hdr    pairList
	TokenMasked string
	// Source is the "MCP source URL / package" field: the repository of a git upstream.
	Source  string
	Suggest suggestState
}

// suggestState says whether the configuration helper can run, and why not.
type suggestState struct {
	Enabled bool
	Why     string
	P       *providerView // the default provider with its models; nil without one
}

type pair struct{ Name, Value string }

// dialogHead feeds the shared "dialog_open" component.
type dialogHead struct{ ID, Title string }

// pillView is a status pill: Class ok|bad|warn|off, Text the label, Tip the hover text (may be empty).
type pillView struct{ Class, Text, Tip string }

// tipView is a value with a hover tooltip (the "tip" component); Tip may be empty.
type tipView struct{ Text, Tip string }

// usageCell is the Usage cell of a key: input and output tokens in compact form, the exact counts
// and requests in the tooltip (last use has its own column). Keys without tokens show an em-dash; the tooltip is kept when
// calls were counted without tokens (MCP, or a provider that reported none).
func usageCell(u vkeys.Usage) tipView {
	v := tipView{Text: "\u2014"}
	if u.Empty() {
		return v
	}
	if u.PromptTokens > 0 || u.CompletionTokens > 0 {
		v.Text = numfmt.Compact(u.PromptTokens) + " in / " + numfmt.Compact(u.CompletionTokens) + " out"
	}
	lines := []string{
		"Input tokens: " + numfmt.Exact(u.PromptTokens),
		"Output tokens: " + numfmt.Exact(u.CompletionTokens),
		"Total tokens: " + numfmt.Exact(u.TotalTokens),
		"API requests: " + numfmt.Exact(u.Requests),
	}
	if u.MCPRequests > 0 {
		lines = append(lines, "MCP requests: "+numfmt.Exact(u.MCPRequests))
	}
	v.Tip = strings.Join(lines, "\n")
	return v
}

// rowList feeds the shared "rows" template component: a dynamic list of single values (arguments).
// Values always holds at least one entry; the first row cannot be deleted in the UI.
type rowList struct {
	Key, PH, Label string
	Values         []string
}

// pickList feeds the shared "pick" template component: a select of known values plus a "Custom"
// entry that reveals a text input. A stored value that is not listed loads as Custom.
type pickList struct {
	Key, Label, CustomLabel, PH string
	Options                     []managed.Command
	Value                       string
	Custom                      bool // the text input holds the value
}

func newPickList(key, label, customLabel, ph string, options []managed.Command, value string, isNew bool) pickList {
	p := pickList{Key: key, Label: label, CustomLabel: customLabel, PH: ph, Options: options, Value: value}
	found := false
	for _, o := range options {
		if o.Name == value {
			found = true
		}
	}
	// A new upstream starts on the first listed command; an existing one keeps its value.
	if !found && value == "" && isNew && len(options) > 0 {
		p.Value, found = options[0].Name, true
	}
	p.Custom = !found
	return p
}

type rowItemData struct {
	Key, PH, Value string
	Removable      bool
}

func newRowList(key, ph, label string, values []string) rowList {
	if len(values) == 0 {
		values = []string{""}
	}
	return rowList{Key: key, PH: ph, Label: label, Values: values}
}

type pairRowData struct {
	NameKey, ValueKey, NamePH string
	Row                       pair
	Removable                 bool
}

// pairList feeds the shared "pairs" template component: a dynamic list of name/value rows (env vars,
// headers). Rows always holds at least one row; the first one cannot be deleted in the UI.
type pairList struct {
	NameKey, ValueKey, NamePH, Label string
	Rows                             []pair
	Err                              bool // stored values could not be decrypted
}

func newPairList(nameKey, valueKey, namePH, label string, kv []mcp.KV, unreadable bool) pairList {
	l := pairList{NameKey: nameKey, ValueKey: valueKey, NamePH: namePH, Label: label, Rows: maskedPairs(kv), Err: unreadable}
	if len(l.Rows) == 0 {
		l.Rows = []pair{{}}
	}
	return l
}

// settingLastInclude remembers the "include in /mcp" choice of the most recent upstream save.
const settingLastInclude = "last_include_in_mcp"

// mask renders the stored credential as asterisks plus its last 4 characters (asterisks only when
// it is shorter than 8 characters).
func mask(u mcp.Upstream) string { return httputil.Mask(u.AuthValue) }

func (a *Admin) view(u mcp.Upstream) upstreamView {
	eff := u.AuthKind
	if u.AuthKind == mcp.AuthAuto {
		eff = "auto: " + orDash(u.DetectedKind, "pending")
	}
	v := upstreamView{Upstream: u, Masked: mask(u), Effective: eff, Target: u.URL}
	if u.Kind == "" {
		v.Kind = mcp.KindRemote
	}
	if u.Managed() {
		v.Target = commandLine(u)
		v.Effective = u.Lifecycle
		pi := a.MCP.ProcessInfo(u)
		pv := &procView{ProcInfo: pi, Class: "off", Text: pi.State}
		switch pi.State {
		case "running":
			pv.Class = "ok"
		case "starting":
			pv.Class = "warn"
		case "failed":
			pv.Class = "bad"
		}
		if !pi.Available {
			pv.Class, pv.Text = "off", "unavailable"
		}
		pv.Tip = procTip(pi)
		pv.Upd = updViewOf(pi.Update)
		v.Proc = pv
	}
	v.Tip = aliasTip(v)
	return v
}

// redactURL drops any user info from a URL shown in the UI.
func redactURL(s string) string {
	if u, err := url.Parse(s); err == nil && u.User != nil {
		u.User = nil
		return u.String()
	}
	return s
}

// aliasTip lists what the table no longer shows in columns: one fact per line.
func aliasTip(v upstreamView) string {
	lines := []string{"type: " + v.Kind}
	if v.Managed() {
		if v.Kind == mcp.KindGit {
			lines = append(lines, "source: "+redactURL(v.GitURL))
		}
		lines = append(lines, "command: "+v.Target)
		if p := v.Proc; p != nil {
			u := p.Upd
			if u.Package != "" {
				lines = append(lines, "package: "+u.Package)
			}
			if u.Rev != "" {
				lines = append(lines, "installed: "+strings.TrimSpace(u.Rev+" "+u.Ref))
			}
			if u.Available {
				lines = append(lines, "update available"+map[bool]string{true: " (" + u.Remote + ")", false: ""}[u.Remote != ""])
			}
			if u.Pinned {
				lines = append(lines, "pinned: "+u.PinNote)
			}
		}
		lines = append(lines, "lifecycle: "+orDash(v.Lifecycle, "on-demand"))
	} else {
		lines = append(lines, "url: "+v.URL)
	}
	if v.HostOverride != "" {
		lines = append(lines, "host override: "+v.HostOverride)
	}
	return strings.Join(lines, "\n")
}

// commandLine renders command and arguments for display.
func commandLine(u mcp.Upstream) string {
	parts := append([]string{u.Command}, u.Args...)
	return strings.Join(parts, " ")
}

func orDash(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// maskedPairs lists stored name/value pairs with the values masked.
func maskedPairs(kv []mcp.KV) []pair {
	var out []pair
	for _, x := range kv {
		out = append(out, pair{x.Name, httputil.Mask(x.Value)})
	}
	return out
}

func (a *Admin) form(r *http.Request, u mcp.Upstream, isNew bool, include bool) formData {
	ok, why := a.MCP.ManagedState()
	f := formData{U: a.view(u), New: isNew, Managed: ok, Why: why, Include: include, Args: newRowList("args", "argument", "Arguments", u.Args),
		Cmd: newPickList("command", "Command", "Custom path…", "/usr/local/bin/tool", a.MCP.Commands(), u.Command, isNew),
		Env: newPairList("env_name", "env_value", "NAME", "Environment", u.Env, u.SecretErr),
		Hdr: newPairList("hdr_name", "hdr_value", "X-Header", "Custom headers", u.Headers, u.SecretErr)}
	if f.U.Kind == "" {
		f.U.Kind = mcp.KindRemote
	}
	f.TokenMasked = httputil.Mask(u.GitToken)
	f.Source = u.GitURL
	f.Suggest = a.suggestState(r)
	return f
}

func (a *Admin) upstreams(w http.ResponseWriter, r *http.Request) {
	list, _ := a.MCP.Upstreams.List()
	v, _ := a.DB.GetSetting(settingLastInclude)
	ok, why := a.MCP.ManagedState()
	d := upstreamsData{Managed: ok, Why: why, Form: a.form(r, mcp.Upstream{Kind: mcp.KindRemote, Enabled: true}, true, v == "1")}
	// Suggest configuration is the quickest way in: with a signed-in provider and an MCP helper model the
	// add form starts on the managed type. Editing never changes the type.
	if d.Form.Managed && d.Form.Suggest.Enabled {
		d.Form.U.Kind = mcp.KindStdio
	}
	for _, u := range list {
		d.List = append(d.List, a.view(u))
		d.Aliases = append(d.Aliases, u.Alias)
	}
	a.render(w, r, "upstreams", page{Title: "MCP upstreams", Nav: "upstreams", Data: d})
}

type editData struct {
	Form formData
	U    upstreamView
}

func (a *Admin) upstreamEdit(w http.ResponseWriter, r *http.Request) {
	alias := r.URL.Query().Get("alias")
	u, ok := a.MCP.Upstreams.Get(alias)
	if !ok {
		a.back(w, r, "/admin/upstreams", "", "unknown alias")
		return
	}
	f := a.form(r, u, false, u.IncludeInMCP)
	a.render(w, r, "upstream_edit", page{Title: "Edit upstream", Nav: "upstreams", Data: editData{Form: f, U: f.U}})
}

// pairsFrom reads parallel name/value form fields; rows without a name are dropped.
func pairsFrom(r *http.Request, nameKey, valueKey string) []mcp.KV {
	names, values := r.PostForm[nameKey], r.PostForm[valueKey]
	var out []mcp.KV
	for i, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		v := ""
		if i < len(values) {
			v = values[i]
		}
		out = append(out, mcp.KV{Name: n, Value: v})
	}
	return out
}

// rowsFrom reads the values of a dynamic single-value list; blank rows are dropped.
func rowsFrom(r *http.Request, key string) []string {
	var out []string
	for _, v := range r.PostForm[key] {
		if strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimRight(v, "\r\n"))
		}
	}
	return out
}

func atoiField(r *http.Request, key string) (int, error) {
	v := strings.TrimSpace(r.PostFormValue(key))
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be a whole number of seconds", key)
	}
	return n, nil
}

// upstreamFromForm builds an upstream from the add/edit form. Only the fields of the selected kind
// are read, so hidden fields of another kind never leak into the record.
func upstreamFromForm(r *http.Request) (mcp.Upstream, error) {
	u := mcp.Upstream{
		Alias: strings.TrimSpace(r.PostFormValue("alias")), Kind: r.PostFormValue("kind"),
		Enabled: r.PostFormValue("enabled") == "1", IncludeInMCP: r.PostFormValue("include") == "1",
	}
	if u.Kind == "" {
		u.Kind = mcp.KindRemote
	}
	switch u.Kind {
	case mcp.KindRemote:
		u.URL, u.AuthKind = strings.TrimSpace(r.PostFormValue("url")), r.PostFormValue("auth_kind")
		u.AuthName, u.AuthValue = strings.TrimSpace(r.PostFormValue("auth_name")), r.PostFormValue("auth_value")
		u.HostOverride = strings.TrimSpace(r.PostFormValue("host_override"))
		u.Headers = pairsFrom(r, "hdr_name", "hdr_value")
	case mcp.KindStdio, mcp.KindGit:
		// One managed type in the UI: a repository makes it a git upstream, otherwise a command.
		// The source field carries the repository; the ref field overrides a ref given in the address.
		gu, gr := strings.TrimSpace(r.PostFormValue("git_url")), strings.TrimSpace(r.PostFormValue("git_ref"))
		if raw := strings.TrimSpace(r.PostFormValue("source")); raw != "" {
			src, err := suggest.ParseSource(raw)
			switch {
			case err != nil:
				return u, err
			case src.Kind == suggest.KindGit:
				gu = src.CloneURL()
				if gr == "" {
					gr = src.Ref
				}
			case src.Kind == suggest.KindUnsupported:
				return u, errors.New(src.UnsupportedMessage(nil))
			}
		}
		if gu != "" {
			u.Kind = mcp.KindGit
		}
		u.Command = strings.TrimSpace(r.PostFormValue("command"))
		if pick := strings.TrimSpace(r.PostFormValue("command_pick")); pick != "" {
			u.Command = pick // a listed command; the text input is only read for Custom
		}
		u.Args = rowsFrom(r, "args")
		u.Env = pairsFrom(r, "env_name", "env_value")
		u.Shell = r.PostFormValue("shell") == "1"
		u.Install = strings.TrimSpace(r.PostFormValue("install"))
		u.Lifecycle = r.PostFormValue("lifecycle")
		var err error
		if u.StartupSecs, err = atoiField(r, "startup_secs"); err != nil {
			return u, err
		}
		if u.AutoUpdateSecs, err = atoiField(r, "auto_update"); err != nil {
			return u, err
		}
		if u.Kind != mcp.KindStdio {
			u.GitURL, u.GitRef, u.GitToken = gu, gr, r.PostFormValue("git_token")
		}
	default:
		return u, errors.New("unknown type")
	}
	return u, nil
}

func (a *Admin) upstreamSave(w http.ResponseWriter, r *http.Request) {
	u, err := upstreamFromForm(r)
	if err != nil {
		a.back(w, r, "/admin/upstreams", "", err.Error())
		return
	}
	edit := r.PostFormValue("mode") == "edit"
	if edit {
		old, ok := a.MCP.Upstreams.Get(u.Alias)
		if !ok {
			a.back(w, r, "/admin/upstreams", "", "unknown alias")
			return
		}
		if old.KindOrRemote() != u.Kind {
			a.back(w, r, "/admin/upstreams", "", "the type cannot be changed; create a new upstream")
			return
		}
		u = mcp.MergeSecrets(old, u, r.PostFormValue("clear_token") == "1")
	}
	if u.Managed() {
		if ok, why := a.MCP.ManagedState(); !ok {
			a.back(w, r, "/admin/upstreams", "", "managed upstreams: "+why)
			return
		}
	}
	if edit {
		err = a.MCP.Upstreams.Update(u, true)
	} else {
		err = a.MCP.Upstreams.Create(u)
	}
	if err != nil {
		a.back(w, r, "/admin/upstreams", "", err.Error())
		return
	}
	_ = a.DB.SetSetting(settingLastInclude, map[bool]string{true: "1", false: "0"}[u.IncludeInMCP])
	a.MCP.SyncManaged(u.Alias)
	msg := fmt.Sprintf("%s saved", u.Alias)
	if u.AuthKind == mcp.AuthAuto && !u.Managed() {
		// Detect right away so the list shows the result (the probe has its own timeouts).
		if up, err := a.MCP.Redetect(r.Context(), u.Alias); err == nil {
			msg += "; detected: " + up.DetectedKind
		}
	}
	a.back(w, r, "/admin/upstreams", msg, "")
}

// upstreamToggle flips "enabled" or "include" for one upstream in place (POST, CSRF-protected by guard).
func (a *Admin) upstreamToggle(w http.ResponseWriter, r *http.Request) {
	alias, flag := r.PostFormValue("alias"), r.PostFormValue("flag")
	u, ok := a.MCP.Upstreams.Get(alias)
	if !ok {
		a.back(w, r, "/admin/upstreams", "", "unknown alias")
		return
	}
	var on bool
	var word string
	switch flag {
	case mcp.FlagEnabled:
		on, word = !u.Enabled, "enabled"
	case mcp.FlagInclude:
		on, word = !u.IncludeInMCP, "in /mcp"
	default:
		a.back(w, r, "/admin/upstreams", "", "unknown flag")
		return
	}
	if _, err := a.MCP.Upstreams.SetFlag(alias, flag, on); err != nil {
		a.back(w, r, "/admin/upstreams", "", err.Error())
		return
	}
	if flag == mcp.FlagEnabled {
		a.MCP.SyncManaged(alias)
	}
	state := "on"
	if !on {
		state = "off"
	}
	a.back(w, r, "/admin/upstreams", fmt.Sprintf("%s: %s %s", alias, word, state), "")
}

// upstreamRedetect re-runs auth auto-detection for one upstream (POST, CSRF-protected by guard).
func (a *Admin) upstreamRedetect(w http.ResponseWriter, r *http.Request) {
	alias := r.PostFormValue("alias")
	up, err := a.MCP.Redetect(r.Context(), alias)
	if err != nil {
		a.back(w, r, "/admin/upstreams", "", err.Error())
		return
	}
	msg := fmt.Sprintf("%s: detected %s (%s)", alias, up.DetectedKind, up.DetectedNote)
	if up.AuthKind != mcp.AuthAuto {
		msg += " (manual mode " + up.AuthKind + ", not applied)"
	}
	if up.DetectedKind == mcp.DetectedFailed || up.DetectedKind == mcp.DetectedOAuth {
		a.back(w, r, "/admin/upstreams", "", msg)
		return
	}
	a.back(w, r, "/admin/upstreams", msg, "")
}

// upstreamTest runs initialize + tools/list against one upstream and renders the outcome.
func (a *Admin) upstreamTest(w http.ResponseWriter, r *http.Request) {
	alias := r.PostFormValue("alias")
	up, ok := a.MCP.Upstreams.Get(alias)
	if !ok {
		a.back(w, r, "/admin/upstreams", "", "unknown alias")
		return
	}
	res := a.MCP.Test(r.Context(), alias)
	a.MCP.Log.Printf("upstream_test alias=%s ok=%v status=%d auth=%s latency=%s error=%q", alias, res.OK, res.Status, res.Auth, res.Latency.Round(time.Millisecond), res.Error)
	a.render(w, r, "upstream_test", page{Title: "Test upstream", Nav: "upstreams", Data: testData{U: a.view(up), R: res}})
}

type testData struct {
	U upstreamView
	R mcp.TestResult
}

func (a *Admin) upstreamDelete(w http.ResponseWriter, r *http.Request) {
	alias := r.PostFormValue("alias")
	_ = a.MCP.Upstreams.Delete(alias)
	a.MCP.SyncManaged(alias)
	a.back(w, r, "/admin/upstreams", "upstream deleted", "")
}

type clientsData struct {
	List             []mcp.Client
	NewID, NewSecret string
}

func (a *Admin) clients(w http.ResponseWriter, r *http.Request) {
	l, _ := a.MCP.Clients.List()
	a.render(w, r, "clients", page{Title: "OAuth clients", Nav: "clients", Data: clientsData{List: l}})
}

func (a *Admin) clientCreate(w http.ResponseWriter, r *http.Request) {
	var uris []string
	for _, f := range strings.FieldsFunc(r.PostFormValue("redirects"), func(r rune) bool { return r == '\n' || r == ' ' || r == ',' || r == '\r' }) {
		uris = append(uris, f)
	}
	if len(uris) == 0 {
		a.back(w, r, "/admin/clients", "", "redirect URI required")
		return
	}
	for _, u := range uris {
		pu, err := url.Parse(u)
		if err != nil || pu.User != nil || pu.Fragment != "" || pu.Host == "" || !(pu.Scheme == "https" || (pu.Scheme == "http" && mcp.IsLoopbackHost(pu.Hostname()))) {
			a.back(w, r, "/admin/clients", "", "redirect URI must be https or loopback http: "+u)
			return
		}
	}
	method := r.PostFormValue("method")
	secret := ""
	switch method {
	case "none":
	case "client_secret_post", "client_secret_basic":
		secret = httputil.RandString(43)
	default:
		a.back(w, r, "/admin/clients", "", "bad auth method")
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" {
		name = "manual client"
	}
	c, err := a.MCP.Clients.Create(mcp.Client{ID: "skc-" + httputil.RandString(24), Name: name, RedirectURIs: uris, AuthMethod: method, Source: "admin"}, secret)
	if err != nil {
		a.back(w, r, "/admin/clients", "", "create failed")
		return
	}
	l, _ := a.MCP.Clients.List()
	a.render(w, r, "clients", page{Title: "OAuth clients", Nav: "clients", Data: clientsData{List: l, NewID: c.ID, NewSecret: secret}})
}

func (a *Admin) clientDelete(w http.ResponseWriter, r *http.Request) {
	_ = a.MCP.Clients.Delete(r.PostFormValue("id"))
	a.back(w, r, "/admin/clients", "client deleted", "")
}

// Package app wires all skgate components into one http.Handler.
package app

import (
	"net/http"
	"strings"

	"github.com/helv-io/skgate/internal/admin"
	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/mcp"
	"github.com/helv-io/skgate/internal/provider"
	"github.com/helv-io/skgate/internal/provider/grok"
	"github.com/helv-io/skgate/internal/reqlog"
	"github.com/helv-io/skgate/internal/store"
	"github.com/helv-io/skgate/internal/vkeys"
)

// App holds the wired components.
type App struct {
	Cfg *config.Config
	DB  *store.DB
	// Providers are the sign-in providers, Grok first; the first one serves the API proxy.
	Providers *provider.Registry
	Keys      *vkeys.Manager
	MCP       *mcp.Server
	Admin     *admin.Admin
	Proxy     *provider.Proxy
	Log       *reqlog.Logger
}

// New wires the application.
func New(cfg *config.Config, db *store.DB) *App {
	cfg.Bind(db)
	a := &App{Cfg: cfg, DB: db}
	g := grok.New(cfg, db)
	a.Providers = provider.NewRegistry(g)
	set := provider.Settings{KV: db}
	set.MigrateLegacy(g.ID())
	a.Keys = vkeys.New(db)
	a.MCP = mcp.NewServer(cfg, db, a.Keys)
	a.Proxy = provider.NewProxy(g, set, a.Keys)
	a.Admin = admin.New(cfg, db, a.Providers, a.Proxy, a.Keys, a.MCP)
	a.Log = a.MCP.Log
	return a
}

// Handler returns the root handler.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/admin", http.StatusFound) })
	// The provider API: /v1, /api/v1, /api and bare API paths. Only API roots are claimed.
	for _, p := range provider.Patterns() {
		mux.Handle(p, httputil.CORS(a.Proxy))
	}
	a.MCP.Routes(mux)
	a.Admin.Routes(mux)
	// Request logging covers the OAuth/OIDC endpoints and /mcp (+SSE); other paths pass through.
	return trimAdminSlash(a.Log.Middleware(mux))
}

// trimAdminSlash serves /admin/... with a trailing slash as the same page without it, for every
// admin route (rewrite, no redirect, so POST bodies and the login return path are unchanged).
// Nothing outside /admin is touched, and /admin/static/ keeps its directory form for the file server.
func trimAdminSlash(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/admin/") && strings.HasSuffix(p, "/") && !strings.HasPrefix(p, "/admin/static/") {
			r2 := r.Clone(r.Context())
			r2.URL.Path = strings.TrimRight(p, "/")
			if rp := r.URL.RawPath; rp != "" {
				r2.URL.RawPath = strings.TrimRight(rp, "/")
			}
			r = r2
		}
		next.ServeHTTP(w, r)
	})
}

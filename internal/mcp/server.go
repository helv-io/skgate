package mcp

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/managed"
	"github.com/helv-io/skgate/internal/reqlog"
	"github.com/helv-io/skgate/internal/store"
	"github.com/helv-io/skgate/internal/vkeys"
)

// Token lifetimes for tokens issued by skgate's authorization server.
const (
	AccessTTL  = time.Hour
	RefreshTTL = 30 * 24 * time.Hour
	CodeTTL    = 10 * time.Minute
)

// SupportedScopes are advertised in metadata. Scopes do not restrict access (single admin).
var SupportedScopes = []string{"mcp", "offline_access"}

// Server bundles the MCP proxy and skgate's OAuth 2.1 authorization server.
type Server struct {
	Cfg       *config.Config
	DB        *store.DB
	Keys      *vkeys.Manager
	Upstreams *Upstreams
	Clients   *Clients
	HTTP      *http.Client
	Log       *reqlog.Logger

	// AdminSession reports whether the request carries a valid admin (OIDC) session and returns
	// its CSRF token. /authorize uses it as the user authentication step: without a session the
	// browser is sent through OIDC login, and nothing is ever auto-approved.
	AdminSession func(r *http.Request) (csrf string, ok bool)
	// AdminIdentity returns the OIDC subject and email of that session.
	AdminIdentity func(r *http.Request) (sub, email string, ok bool)
	// Consent renders the Approve/Deny page (the admin layout owns the design).
	Consent func(w http.ResponseWriter, r *http.Request, v ConsentView)

	sessMu   sync.Mutex
	sessions map[string]*sseSession
	detectMu sync.Mutex // serializes lazy auth detection
	up       *upClient  // per-upstream MCP sessions used by the /mcp aggregator
	health   healthBook // how the latest calls to each remote upstream went

	// Managed runs the child processes of stdio and git upstreams (never spawns when disabled).
	Managed *managed.Manager
}

// NewServer wires a Server.
func NewServer(cfg *config.Config, db *store.DB, keys *vkeys.Manager) *Server {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 2 * time.Minute
	lg := reqlog.New(reqlog.ParseLevel(cfg.LogLevel), os.Stderr)
	return &Server{
		Managed: managed.NewManager(managed.Options{Enabled: config.ManagedAvailable(), Dir: cfg.ManagedDir, CacheDir: cfg.ManagedCacheDir, MaxProcs: cfg.ManagedMaxProcs, RunAs: managed.DetectRunAs(),
			StopGrace: cfg.ManagedStopGrace, LogLines: cfg.ManagedLogLines, InstallMax: cfg.ManagedInstallMax, Logf: lg.Printf, Version: config.Version}),
		Cfg: cfg, DB: db, Keys: keys, Upstreams: NewUpstreams(db), Clients: NewClients(db),
		HTTP:     &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		sessions: map[string]*sseSession{},
		up:       newUpClient(),
		Log:      lg,
	}
}

// Issuer is the AS issuer identifier (PUBLIC_URL).
func (s *Server) Issuer() string { return strings.TrimRight(s.Cfg.PublicURL, "/") }

// Routes registers all MCP and OAuth routes on mux.
func (s *Server) Routes(mux *http.ServeMux) {
	wk := func(p string, h http.HandlerFunc) {
		mux.Handle(p, httputil.CORS(h))
		mux.Handle(p+"/", httputil.CORS(h))
	}
	wk("/.well-known/oauth-authorization-server", s.asMetadata)
	wk("/.well-known/openid-configuration", s.asMetadata)
	wk("/.well-known/oauth-protected-resource", s.prm)
	// Every endpoint answers identically with and without a trailing slash, never with a redirect.
	mux.Handle("/register", httputil.CORS(http.HandlerFunc(s.register)))
	mux.Handle("/register/", httputil.CORS(http.HandlerFunc(s.register)))
	mux.Handle("/token", httputil.CORS(http.HandlerFunc(s.token)))
	mux.Handle("/token/", httputil.CORS(http.HandlerFunc(s.token)))
	mux.HandleFunc("/authorize", s.authorize)
	mux.HandleFunc("/authorize/", s.authorize)
	mux.HandleFunc("/mcp", s.serveMCP)
	mux.HandleFunc("/mcp/", s.serveMCP)
	mux.HandleFunc("/sse", s.serveSSE)
	mux.HandleFunc("/sse/", s.serveSSE)
	mux.HandleFunc("/messages", s.serveMessages)
	mux.HandleFunc("/messages/", s.serveMessages)
}

func (s *Server) asMetadataDoc() map[string]any {
	iss := s.Issuer()
	return map[string]any{
		"issuer":                                         iss,
		"authorization_endpoint":                         iss + "/authorize",
		"token_endpoint":                                 iss + "/token",
		"registration_endpoint":                          iss + "/register",
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none", "client_secret_post", "client_secret_basic"},
		"scopes_supported":                               SupportedScopes,
		"authorization_response_iss_parameter_supported": true,
	}
}

func (s *Server) asMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		reqlog.Reject(r, "method %s not allowed on metadata endpoint (GET only)", r.Method)
		httputil.OAuthError(w, http.StatusMethodNotAllowed, "invalid_request", "GET only")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	httputil.JSON(w, http.StatusOK, s.asMetadataDoc())
}

// resourceFor maps a well-known suffix or request path to a canonical resource URL: the public URL
// plus the path, without a trailing slash.
func (s *Server) resourceFor(path string) string {
	path = "/" + strings.Trim(path, "/")
	if path == "/" {
		return s.Issuer()
	}
	return s.Issuer() + path
}

func (s *Server) prmDoc(resource string) map[string]any {
	return map[string]any{
		"resource":                 resource,
		"authorization_servers":    []string{s.Issuer()},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         SupportedScopes,
		"resource_name":            "skgate MCP proxy",
	}
}

func (s *Server) prm(w http.ResponseWriter, r *http.Request) {
	suffix := strings.TrimPrefix(r.URL.Path, "/.well-known/oauth-protected-resource")
	if strings.Trim(suffix, "/") == "" {
		// A document at the root would name the root as the resource, but the MCP endpoints are under
		// /mcp. Clients that take the first document they get (Home Assistant) reject the mismatch,
		// so there is none; the path-specific documents are the only ones.
		reqlog.Reject(r, "no protected resource at the root: use /.well-known/oauth-protected-resource/mcp or /mcp/<alias>")
		httputil.JSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "error_description": "no protected resource at the root; use /.well-known/oauth-protected-resource/mcp or /mcp/<alias>"})
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	httputil.JSON(w, http.StatusOK, s.prmDoc(s.resourceFor(suffix)))
}

// prmURL returns the absolute protected-resource metadata URL for a request path.
func (s *Server) prmURL(path string) string {
	p := "/" + strings.Trim(path, "/")
	if p == "/" {
		return s.Issuer() + "/.well-known/oauth-protected-resource"
	}
	return s.Issuer() + "/.well-known/oauth-protected-resource" + p
}

func (s *Server) unauthorized(w http.ResponseWriter, r *http.Request, path string, presented bool) {
	if presented {
		reqlog.Reject(r, "invalid token: the presented credential is invalid, expired, revoked or for another resource")
	} else {
		reqlog.Reject(r, "missing token: no Authorization Bearer, X-API-Key or ?key= credential was sent")
	}
	httputil.SetCORS(w)
	h := `Bearer resource_metadata="` + s.prmURL(path) + `"`
	desc := "authentication required: send a skgate virtual key (Authorization: Bearer sk-... or X-API-Key) or an OAuth access token"
	if presented {
		h += `, error="invalid_token"`
		desc = "the presented credential is invalid, expired or revoked"
	}
	w.Header().Set("WWW-Authenticate", h)
	w.Header().Set("Cache-Control", "no-store")
	httputil.JSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized", "error_description": desc})
}

// authenticate checks inbound MCP credentials: virtual key via Authorization Bearer or X-API-Key,
// optional ?key=, or an OAuth access token issued by this server. path is the request path (for the
// RFC 8707 audience check). It returns (ok, presentedSomething).
func (s *Server) authenticate(r *http.Request, path string) (ok, presented bool) {
	// An earlier credential may fail with a recorded reason while a later one succeeds.
	defer func() {
		if ok {
			reqlog.ClearReason(r)
		}
	}()
	if tok := httputil.BearerToken(r); tok != "" {
		presented = true
		if vkeys.LooksLikeKey(tok) {
			if key, ok := s.Keys.Verify(tok); ok {
				s.Keys.Record(key.ID, vkeys.Usage{MCPRequests: 1})
				reqlog.Note(r, "auth=virtual-key")
				return true, true
			}
			if at, late := s.Keys.ExpiredAt(tok); late {
				reqlog.Reject(r, "invalid token: virtual key expired %s (key ending %s)", at.Local().Format(time.RFC3339), tokTail(tok))
			} else {
				reqlog.Reject(r, "invalid token: virtual key is unknown or revoked (key ending %s)", tokTail(tok))
			}
		} else if why := s.verifyAccessToken(tok, path); why == "" {
			reqlog.Note(r, "auth=oauth-token")
			return true, true
		} else {
			reqlog.Reject(r, "invalid token: %s", why)
		}
	}
	if k := strings.TrimSpace(r.Header.Get("X-API-Key")); k != "" {
		presented = true
		if key, ok := s.Keys.Verify(k); ok {
			s.Keys.Record(key.ID, vkeys.Usage{MCPRequests: 1})
			reqlog.Note(r, "auth=x-api-key")
			return true, true
		}
		if at, late := s.Keys.ExpiredAt(k); late {
			reqlog.Reject(r, "invalid token: X-API-Key is a virtual key that expired %s", at.Local().Format(time.RFC3339))
		} else {
			reqlog.Reject(r, "invalid token: X-API-Key is not a valid virtual key")
		}
	}
	if k := r.URL.Query().Get("key"); k != "" {
		presented = true
		if key, ok := s.Keys.Verify(k); ok && !key.URLKey {
			reqlog.Reject(r, "invalid token: ?key= is a valid virtual key that is not allowed in the URL (enable it in the key's details)")
		} else if ok {
			s.Keys.Record(key.ID, vkeys.Usage{MCPRequests: 1})
			reqlog.Note(r, "auth=query-key")
			return true, true
		} else {
			if at, late := s.Keys.ExpiredAt(k); late {
				reqlog.Reject(r, "invalid token: ?key= is a virtual key that expired %s", at.Local().Format(time.RFC3339))
			} else {
				reqlog.Reject(r, "invalid token: ?key= is not a valid virtual key")
			}
		}
	}
	return false, presented
}

// tokTail shows only the last 4 characters of a credential (asterisks when it is short), the most
// a log line or the admin UI may reveal.
func tokTail(tok string) string { return httputil.Mask(tok) }

// verifyAccessToken validates a skgate-issued OAuth access token for a request path. It returns ""
// when the token is valid, else a short reason (no token value in it).
func (s *Server) verifyAccessToken(tok, path string) string {
	var client, resource string
	var exp int64
	err := s.DB.QueryRow(`SELECT client_id,resource,expires_at FROM oauth_tokens WHERE hash=? AND kind='access'`, httputil.SHA256Hex(tok)).Scan(&client, &resource, &exp)
	if err != nil {
		return "OAuth access token is unknown (never issued by this skgate, revoked, or purged after expiry)"
	}
	if time.Now().Unix() >= exp {
		return "OAuth access token has expired (the client should refresh it)"
	}
	if _, ok := s.Clients.Get(client); !ok {
		return "the OAuth client that owns this token no longer exists"
	}
	if !s.audienceOK(resource, path) {
		return "OAuth access token is bound to another resource (" + resource + "), not " + path
	}
	return ""
}

// audienceOK enforces RFC 8707 binding: a token bound to a resource only works for that resource
// (or its parents, PUBLIC_URL and PUBLIC_URL/mcp). Legacy SSE paths are not audience checked.
func (s *Server) audienceOK(resource, path string) bool {
	resource = strings.TrimRight(resource, "/")
	if resource == "" || resource == s.Issuer() {
		return true
	}
	p := "/" + strings.Trim(path, "/")
	if !strings.HasPrefix(p, "/mcp") {
		return true
	}
	if resource == s.Issuer()+"/mcp" {
		return true
	}
	return resource == s.Issuer()+p
}

// validResource reports whether a client supplied RFC 8707 resource belongs to this server.
func (s *Server) validResource(res string) bool {
	u, err := url.Parse(res)
	if err != nil || u.Fragment != "" || u.RawQuery != "" {
		return false
	}
	base := s.Issuer()
	r := strings.TrimRight(res, "/")
	return r == base || strings.HasPrefix(r, base+"/")
}

// purgeExpired removes expired codes and tokens.
func (s *Server) purgeExpired() {
	now := time.Now().Unix()
	_, _ = s.DB.Exec(`DELETE FROM oauth_codes WHERE expires_at<?`, now)
	_, _ = s.DB.Exec(`DELETE FROM oauth_tokens WHERE expires_at<?`, now)
}

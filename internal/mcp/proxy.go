package mcp

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/reqlog"
)

const maxMCPBody = 16 << 20

// forwardHeaders are the inbound headers passed to the upstream MCP server.
var forwardHeaders = []string{"Accept", "Content-Type", "Mcp-Session-Id", "Mcp-Protocol-Version", "Last-Event-ID", "User-Agent"}

// LooksLikeClient reports whether r is from an MCP client rather than a browser: Accept asks for
// application/json or text/event-stream, Content-Type is JSON or an MCP type, or it is a CORS preflight
// (OPTIONS with Access-Control-Request-Method). Browsers navigating to / send Accept: text/html and are not matched.
func LooksLikeClient(r *http.Request) bool {
	if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
		return true
	}
	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "application/json") || strings.Contains(accept, "text/event-stream") {
		return true
	}
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch ct {
	case "application/json", "application/json-rpc", "application/jsonrpc", "application/mcp+json":
		return true
	}
	return false
}

// ServeRoot handles / for MCP clients: the same aggregate Streamable HTTP as /mcp. Browsers are routed elsewhere.
func (s *Server) ServeRoot(w http.ResponseWriter, r *http.Request) {
	r2 := r.Clone(r.Context())
	u := *r.URL
	u.Path = "/mcp"
	r2.URL = &u
	s.serveMCP(w, r2)
}

// splitAlias parses /mcp, /mcp/, /mcp/{alias}, /mcp/{alias}/ into an alias ("" for bare /mcp).
func splitAlias(path string) (alias string, ok bool) {
	rest := strings.Trim(strings.TrimPrefix(path, "/mcp"), "/")
	if rest == "" {
		return "", true
	}
	if strings.Contains(rest, "/") {
		return "", false
	}
	return rest, true
}

// resolve finds the enabled upstream for /mcp/{alias} and /sse/{alias}. The bare paths are served
// by the aggregator and never resolve to one upstream.
func (s *Server) resolve(alias string) (Upstream, string, int) {
	u, ok := s.Upstreams.Get(alias)
	if alias == "" || !ok || !u.Enabled {
		return u, "unknown or disabled MCP upstream alias", http.StatusNotFound
	}
	return u, "", 0
}

func jsonErr(w http.ResponseWriter, status int, code, desc string) {
	httputil.SetCORS(w)
	httputil.JSON(w, status, map[string]string{"error": code, "error_description": desc})
}

// serveMCP is the Streamable HTTP reverse proxy at /mcp and /mcp/{alias}.
func (s *Server) serveMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		httputil.SetCORS(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	alias, ok := splitAlias(r.URL.Path)
	if !ok {
		jsonErr(w, http.StatusNotFound, "not_found", "unknown path")
		return
	}
	authPath := "/mcp"
	if alias != "" {
		authPath += "/" + alias
	}
	if okAuth, presented := s.authenticate(r, authPath); !okAuth {
		s.unauthorized(w, r, authPath, presented)
		return
	}
	if alias == "" {
		s.serveAggregate(w, r)
		return
	}
	up, msg, status := s.resolve(alias)
	if status != 0 {
		reqlog.Reject(r, "unknown upstream: %s (alias %q)", msg, alias)
		jsonErr(w, status, "not_found", msg)
		return
	}
	if up.Managed() {
		s.serveManaged(w, r, up)
		return
	}
	if up.IsOpenAPI() {
		s.serveOpenAPI(w, r, up)
		return
	}
	up = s.ensureDetected(r.Context(), up)
	reqlog.Upstream(r, up.Alias, up.URL, 0, up.EffectiveKind())
	switch r.Method {
	case http.MethodGet, http.MethodPost, http.MethodDelete:
	default:
		reqlog.Reject(r, "method %s not allowed on /mcp", r.Method)
		w.Header().Set("Allow", "GET, POST, DELETE, OPTIONS")
		jsonErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST (Streamable HTTP), GET (SSE stream) or DELETE (end session)")
		return
	}
	var body []byte
	if r.Method == http.MethodPost {
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMCPBody))
		if err != nil {
			reqlog.Reject(r, "request body too large or unreadable")
			jsonErr(w, http.StatusRequestEntityTooLarge, "invalid_request", "request body too large or unreadable")
			return
		}
		body = b
	}
	resp, err := s.forward(r, up, r.Method, body, r.Header)
	if err != nil {
		reqlog.Reject(r, "upstream unreachable: %s", reqlog.Sanitize(err))
		jsonErr(w, http.StatusBadGateway, "upstream_error", "MCP upstream request failed")
		return
	}
	defer resp.Body.Close()
	reqlog.Upstream(r, up.Alias, up.URL, resp.StatusCode, up.EffectiveKind())
	if resp.StatusCode == http.StatusUnauthorized {
		reqlog.Reject(r, "upstream error: upstream answered HTTP 401 to the outbound credentials (auth %s)%s", up.EffectiveKind(), oauthHint(resp))
		// Do not leak the upstream's challenge; it would send clients to the wrong authorization server.
		jsonErr(w, http.StatusBadGateway, "upstream_unauthorized", "the MCP upstream rejected skgate's outbound credentials (check the upstream auth settings)")
		return
	}
	if resp.StatusCode >= 400 {
		reqlog.Reject(r, "upstream error: upstream answered HTTP %d", resp.StatusCode)
	}
	httputil.SetCORS(w)
	rewriteLocation(resp.Header, up, s.Issuer())
	httputil.CopyResponse(w, resp, "Www-Authenticate")
}

func oauthHint(resp *http.Response) string {
	if advertisesOAuth(resp.Header.Get("Www-Authenticate")) {
		return "; it advertises OAuth, which skgate does not support for upstreams"
	}
	return ""
}

// forward sends a request to the upstream with outbound auth applied.
func (s *Server) forward(in *http.Request, up Upstream, method string, body []byte, hdr http.Header) (*http.Response, error) {
	target := up.URL
	if in != nil && in.URL.RawQuery != "" {
		q := in.URL.Query()
		q.Del("key")
		q.Del("sessionId")
		if len(q) > 0 {
			if strings.Contains(target, "?") {
				target += "&" + q.Encode()
			} else {
				target += "?" + q.Encode()
			}
		}
	}
	if _, err := url.Parse(target); err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	ctx := in.Context()
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return nil, err
	}
	for _, h := range forwardHeaders {
		if v := hdr.Values(h); len(v) > 0 {
			for _, x := range v {
				req.Header.Add(h, x)
			}
		}
	}
	applyOutbound(req, in, up)
	return s.doUpstream(up, req, body)
}

package mcp

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/reqlog"
)

var (
	dcrGrants    = map[string]bool{"authorization_code": true, "refresh_token": true}
	dcrResponses = map[string]bool{"code": true}
	authMethods  = map[string]bool{"none": true, "client_secret_post": true, "client_secret_basic": true}
)

// register implements RFC 7591 Dynamic Client Registration. Every redirect_uri must pass RedirectAllowed.
// token_endpoint_auth_method defaults to "none" (public client + PKCE) when omitted; this deviates from the
// RFC 7591 default of client_secret_basic on purpose, since MCP clients are overwhelmingly public clients.
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		reqlog.Reject(r, "method %s not allowed on /register (POST only)", r.Method)
		httputil.OAuthError(w, http.StatusMethodNotAllowed, "invalid_request", "POST only")
		return
	}
	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
		GrantTypes   []string `json:"grant_types"`
		ResponseType []string `json:"response_types"`
		Scope        string   `json:"scope"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	if err := dec.Decode(&req); err != nil {
		reqlog.Reject(r, "invalid client metadata: body is not a JSON object")
		httputil.OAuthError(w, 400, "invalid_client_metadata", "body must be a JSON object")
		return
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > 10 {
		reqlog.Reject(r, "invalid redirect_uri: redirect_uris must list 1 to 10 URIs (got %d)", len(req.RedirectURIs))
		httputil.OAuthError(w, 400, "invalid_redirect_uri", "redirect_uris must list 1 to 10 URIs")
		return
	}
	reqlog.Redirect(r, req.RedirectURIs[0])
	// Unusable redirect URIs (custom schemes such as cursor://, plain http off loopback, userinfo, ...)
	// are ignored as long as one usable URI remains; only then is the registration refused.
	var uris []string
	var skipped []string
	for _, u := range req.RedirectURIs {
		if why, ok := RedirectCheck(u); !ok {
			skipped = append(skipped, why+" (host "+reqlog.HostOf(u)+")")
			continue
		}
		uris = append(uris, u)
	}
	if len(uris) == 0 {
		reqlog.Redirect(r, req.RedirectURIs[0])
		reqlog.Reject(r, "bad redirect origin: %s", skipped[0])
		httputil.OAuthError(w, 400, "invalid_redirect_uri", "redirect_uri is not allowed: "+truncate(req.RedirectURIs[0], 120))
		return
	}
	if len(skipped) > 0 {
		reqlog.Note(r, "ignored %d unusable redirect_uri(s): %s", len(skipped), skipped[0])
	}
	req.RedirectURIs = uris
	// Auth method, grant types and response types are reduced to what skgate supports and the
	// response says what was granted (RFC 7591 section 3.2.1). A client that asks for a method we
	// do not offer is registered as a public client (PKCE), which is what MCP clients expect.
	method := req.AuthMethod
	if !authMethods[method] {
		method = "none"
	}
	grants := supported(req.GrantTypes, dcrGrants, []string{"authorization_code", "refresh_token"})
	if len(grants) == 0 {
		reqlog.Reject(r, "invalid client metadata: none of grant_types %v is supported", req.GrantTypes)
		httputil.OAuthError(w, 400, "invalid_client_metadata", "none of the requested grant_types is supported (authorization_code, refresh_token)")
		return
	}
	resps := supported(req.ResponseType, dcrResponses, []string{"code"})
	if len(resps) == 0 {
		reqlog.Reject(r, "invalid client metadata: none of response_types %v is supported", req.ResponseType)
		httputil.OAuthError(w, 400, "invalid_client_metadata", "none of the requested response_types is supported (code)")
		return
	}
	name := truncate(strings.TrimSpace(req.ClientName), 100)
	if name == "" {
		name = "dcr client"
	}
	secret := ""
	if method != "none" {
		secret = httputil.RandString(43)
	}
	c, err := s.Clients.Create(Client{ID: "skc-" + httputil.RandString(24), Name: name, RedirectURIs: req.RedirectURIs, AuthMethod: method, Source: "dcr"}, secret)
	if err != nil {
		reqlog.Reject(r, "server error: could not store client")
		httputil.OAuthError(w, 500, "server_error", "could not store client")
		return
	}
	scope := strings.TrimSpace(req.Scope)
	if scope == "" {
		scope = "mcp"
	}
	resp := map[string]any{
		"client_id": c.ID, "client_id_issued_at": c.CreatedAt.Unix(), "client_name": c.Name,
		"redirect_uris": c.RedirectURIs, "grant_types": grants, "response_types": resps,
		"token_endpoint_auth_method": method, "scope": scope,
	}
	if secret != "" {
		resp["client_secret"] = secret
		resp["client_secret_expires_at"] = 0
	}
	reqlog.Client(r, c.ID)
	w.Header().Set("Cache-Control", "no-store")
	httputil.JSON(w, http.StatusCreated, resp)
}

// supported returns the entries of asked that are in ok (in order, no duplicates, at most 10 are
// considered), or def when nothing was asked.
func supported(asked []string, ok map[string]bool, def []string) []string {
	if len(asked) == 0 {
		return def
	}
	if len(asked) > 10 {
		asked = asked[:10]
	}
	var out []string
	seen := map[string]bool{}
	for _, v := range asked {
		if ok[v] && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

type authParams struct {
	ClientID, RedirectURI, State, Challenge, Method, Resource, Scope, ResponseType string
	Client                                                                         Client
}

func (p authParams) query() url.Values {
	return url.Values{"response_type": {p.ResponseType}, "client_id": {p.ClientID}, "redirect_uri": {p.RedirectURI}, "state": {p.State},
		"code_challenge": {p.Challenge}, "code_challenge_method": {p.Method}, "resource": {p.Resource}, "scope": {p.Scope}}
}

// redirectMatches reports whether uri may be used with client c: it must equal a registered
// redirect_uri (loopback ports may differ) and pass the syntax and trusted-origin rules. The
// second result is the reason for a refusal.
func (s *Server) redirectMatches(c Client, uri string) (bool, string) {
	found := false
	for _, reg := range c.RedirectURIs {
		if subtle.ConstantTimeCompare([]byte(reg), []byte(uri)) == 1 || loopbackEquivalent(reg, uri) {
			found = true
			break
		}
	}
	if !found {
		return false, "redirect_uri does not exactly match any redirect_uri registered for this client (host " + reqlog.HostOf(uri) + ")"
	}
	if c.Source == "admin" {
		// Admin-registered clients: exact match against what the admin typed, https or loopback only.
		u, err := url.Parse(uri)
		if err == nil && u.User == nil && u.Fragment == "" && (u.Scheme == "https" || (u.Scheme == "http" && IsLoopbackHost(u.Hostname()))) {
			return true, ""
		}
		return false, "redirect_uri must be https or http on loopback"
	}
	if why, ok := RedirectCheck(uri); !ok {
		return false, "bad redirect origin: " + why
	}
	return true, ""
}

func errorPage(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>skgate</title><body style="font:14px monospace;background:#111;color:#ddd;padding:2em"><h3>Authorization error</h3><p>%s</p>`, html.EscapeString(msg))
}

func (s *Server) redirectError(w http.ResponseWriter, r *http.Request, ap authParams, code, desc string) {
	u, err := url.Parse(ap.RedirectURI)
	if err != nil {
		errorPage(w, 400, desc)
		return
	}
	q := u.Query()
	q.Set("error", code)
	q.Set("error_description", desc)
	if ap.State != "" {
		q.Set("state", ap.State)
	}
	q.Set("iss", s.Issuer())
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// parseAuth validates authorization request parameters. On a client_id or redirect_uri problem it renders
// an error page (never redirecting); otherwise it may return an error code to send via redirect.
func (s *Server) parseAuth(w http.ResponseWriter, r *http.Request, get func(string) string) (ap authParams, code, desc string, ok bool) {
	ap = authParams{ClientID: get("client_id"), RedirectURI: get("redirect_uri"), State: get("state"), Challenge: get("code_challenge"),
		Method: get("code_challenge_method"), Resource: get("resource"), Scope: get("scope"), ResponseType: get("response_type")}
	reqlog.Client(r, ap.ClientID)
	reqlog.Redirect(r, ap.RedirectURI)
	c, found := s.Clients.Get(ap.ClientID)
	if ap.ClientID == "" || !found {
		if ap.ClientID == "" {
			reqlog.Reject(r, "unknown client: client_id is missing")
		} else {
			reqlog.Reject(r, "unknown client: client_id is not registered (the client must register again via /register)")
		}
		errorPage(w, 400, "unknown client_id")
		return ap, "", "", false
	}
	ap.Client = c
	s.Clients.Touch(c.ID)
	if ap.RedirectURI == "" && len(c.RedirectURIs) == 1 {
		ap.RedirectURI = c.RedirectURIs[0]
	}
	reqlog.Redirect(r, ap.RedirectURI)
	if ap.RedirectURI == "" {
		reqlog.Reject(r, "redirect_uri is missing and the client has several registered")
		errorPage(w, 400, "redirect_uri is missing, not registered for this client, or not on the trusted origin list")
		return ap, "", "", false
	}
	if ok, why := s.redirectMatches(c, ap.RedirectURI); !ok {
		reqlog.Reject(r, "%s", why)
		errorPage(w, 400, "redirect_uri is missing, not registered for this client, or not on the trusted origin list")
		return ap, "", "", false
	}
	if ap.ResponseType != "code" {
		reqlog.Reject(r, "unsupported response_type %q (only code)", truncate(ap.ResponseType, 40))
		return ap, "unsupported_response_type", "only response_type=code is supported", true
	}
	// A challenge that is sent must be S256 (plain is never accepted) and is verified at /token. Without
	// one, only a confidential client that has never used PKCE gets through; it authenticates with its
	// secret at /token instead.
	if ap.Challenge != "" || !c.PKCEOptional() {
		if ap.Challenge == "" || ap.Method != "S256" {
			reqlog.Reject(r, "PKCE required: code_challenge missing or code_challenge_method is %q, not S256", truncate(ap.Method, 20))
			return ap, "invalid_request", "PKCE is required: send code_challenge and code_challenge_method=S256", true
		}
		if len(ap.Challenge) < 43 || len(ap.Challenge) > 128 {
			reqlog.Reject(r, "PKCE: code_challenge has an invalid length (%d)", len(ap.Challenge))
			return ap, "invalid_request", "code_challenge has an invalid length", true
		}
	}
	if ap.Resource != "" && !s.validResource(ap.Resource) {
		reqlog.Reject(r, "invalid_target: resource %q does not identify this server", truncate(ap.Resource, 120))
		return ap, "invalid_target", "resource does not identify this server", true
	}
	if ap.Scope == "" {
		ap.Scope = "mcp"
	}
	return ap, "", "", true
}

// authorize is the authorization endpoint. Order of checks:
//  1. client_id, exact redirect_uri, trusted-origin allowlist, response_type and PKCE S256 are
//     validated first, so bad requests fail (error page or redirect error) before any IdP bounce.
//  2. If OIDC is not configured, it answers with an error page. It never auto-approves.
//  3. Without a valid admin/OIDC session, the browser is redirected to /admin/oidc/login with the
//     original /authorize URL as next. After the IdP round trip (and the OIDC_ALLOWED_* checks)
//     the browser returns here and the request resumes. With a session, login is skipped.
//  4. Unless MCP_OAUTH_REQUIRE_CONSENT=false, an extra consent page follows; otherwise the code is
//     issued straight away, recording the OIDC subject and email on it.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		q := r.URL.Query()
		ap, code, desc, ok := s.parseAuth(w, r, q.Get)
		if !ok {
			return
		}
		if code != "" {
			s.redirectError(w, r, ap, code, desc)
			return
		}
		if !s.oidcReady() {
			reqlog.Reject(r, "OIDC is not configured, so /authorize refuses to issue codes")
			errorPage(w, http.StatusServiceUnavailable, "OIDC is not configured on this skgate (OIDC_ISSUER, OIDC_CLIENT_ID, OIDC_CLIENT_SECRET), so authorization requests are refused. skgate never auto-approves.")
			return
		}
		csrf, authed := s.AdminSession(r)
		if !authed {
			reqlog.Note(r, "no admin session, redirecting to OIDC login")
			http.Redirect(w, r, "/admin/oidc/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
			return
		}
		if s.Cfg.RequireConsent {
			s.consentPage(w, r, ap, csrf)
			return
		}
		s.issueCode(w, r, ap)
	case http.MethodPost:
		// POST exists only for the optional consent page.
		if !s.Cfg.RequireConsent || !s.oidcReady() {
			reqlog.Reject(r, "POST /authorize is only used by the consent page (consent is off or OIDC is not configured)")
			errorPage(w, 405, "method not allowed")
			return
		}
		csrf, authed := s.AdminSession(r)
		if !authed || r.ParseForm() != nil || subtle.ConstantTimeCompare([]byte(csrf), []byte(r.PostFormValue("csrf"))) != 1 {
			reqlog.Reject(r, "admin session or CSRF token invalid on consent POST")
			errorPage(w, 403, "admin session or CSRF token invalid")
			return
		}
		ap, code, desc, ok := s.parseAuth(w, r, r.PostFormValue)
		if !ok {
			return
		}
		if code != "" {
			s.redirectError(w, r, ap, code, desc)
			return
		}
		if r.PostFormValue("decision") != "approve" {
			reqlog.Reject(r, "access_denied: the user denied the consent request")
			s.redirectError(w, r, ap, "access_denied", "the user denied the request")
			return
		}
		s.issueCode(w, r, ap)
	default:
		reqlog.Reject(r, "method %s not allowed on /authorize", r.Method)
		errorPage(w, 405, "method not allowed")
	}
}

// oidcReady reports whether user authentication for /authorize can work at all.
func (s *Server) oidcReady() bool {
	return s.Cfg.OIDCEnabled() && s.AdminSession != nil && s.AdminIdentity != nil
}

// ConsentView is what the consent page shows and posts back.
type ConsentView struct {
	Client       string // client name
	RedirectURI  string
	RedirectHost string
	Scope        string
	CSRF         string
	Fields       []ConsentField // hidden fields that repeat the authorization request
}

// ConsentField is one hidden form field.
type ConsentField struct{ Name, Value string }

func (s *Server) consentPage(w http.ResponseWriter, r *http.Request, ap authParams, csrf string) {
	if s.Consent == nil {
		errorPage(w, http.StatusInternalServerError, "consent page unavailable")
		return
	}
	v := ConsentView{Client: ap.Client.Name, RedirectURI: ap.RedirectURI, Scope: ap.Scope, CSRF: csrf}
	if u, err := url.Parse(ap.RedirectURI); err == nil {
		v.RedirectHost = u.Host
	}
	q := ap.query()
	names := make([]string, 0, len(q))
	for k := range q {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		v.Fields = append(v.Fields, ConsentField{Name: k, Value: q[k][0]})
	}
	s.Consent(w, r, v)
}

func (s *Server) issueCode(w http.ResponseWriter, r *http.Request, ap authParams) {
	sub, email, ok := s.AdminIdentity(r)
	if !ok || sub == "" {
		reqlog.Reject(r, "no authenticated user for the authorization code")
		errorPage(w, http.StatusForbidden, "no authenticated user")
		return
	}
	code := httputil.RandString(43)
	_, err := s.DB.Exec(`INSERT INTO oauth_codes(hash,client_id,redirect_uri,challenge,resource,scope,expires_at,sub,email) VALUES(?,?,?,?,?,?,?,?,?)`,
		httputil.SHA256Hex(code), ap.ClientID, ap.RedirectURI, ap.Challenge, strings.TrimRight(ap.Resource, "/"), ap.Scope, time.Now().Add(CodeTTL).Unix(), sub, email)
	if err != nil {
		reqlog.Reject(r, "server error: could not store authorization code")
		s.redirectError(w, r, ap, "server_error", "could not store authorization code")
		return
	}
	reqlog.Note(r, "authorization code issued")
	u, _ := url.Parse(ap.RedirectURI)
	q := u.Query()
	q.Set("code", code)
	if ap.State != "" {
		q.Set("state", ap.State)
	}
	q.Set("iss", s.Issuer())
	u.RawQuery = q.Encode()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// clientAuth authenticates the client at the token endpoint (client_secret_basic, client_secret_post, or none).
func (s *Server) clientAuth(w http.ResponseWriter, r *http.Request) (Client, bool) {
	id, secret, viaBasic := "", "", false
	if u, p, ok := r.BasicAuth(); ok {
		viaBasic = true
		id, _ = url.QueryUnescape(u)
		secret, _ = url.QueryUnescape(p)
	} else {
		id, secret = r.PostFormValue("client_id"), r.PostFormValue("client_secret")
	}
	reqlog.Client(r, id)
	if id == "" {
		reqlog.Reject(r, "invalid request: client_id is required at the token endpoint")
		httputil.OAuthError(w, 400, "invalid_request", "client_id is required")
		return Client{}, false
	}
	c, ok := s.Clients.Get(id)
	if !ok || (c.Confidential() && !c.CheckSecret(secret)) {
		if !ok {
			reqlog.Reject(r, "unknown client: client_id is not registered")
		} else {
			reqlog.Reject(r, "invalid client: client secret is missing or wrong (client %s is confidential)", c.AuthMethod)
		}
		if viaBasic {
			w.Header().Set("WWW-Authenticate", `Basic realm="skgate"`)
		}
		httputil.OAuthError(w, 401, "invalid_client", "client authentication failed")
		return Client{}, false
	}
	s.Clients.Touch(c.ID)
	return c, true
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		reqlog.Reject(r, "method %s not allowed on /token (POST only)", r.Method)
		httputil.OAuthError(w, 405, "invalid_request", "POST only")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var m map[string]any
		if json.NewDecoder(r.Body).Decode(&m) != nil {
			reqlog.Reject(r, "invalid request: body is not valid JSON")
			httputil.OAuthError(w, 400, "invalid_request", "invalid JSON body")
			return
		}
		r.PostForm = url.Values{}
		for k, v := range m {
			if sv, ok := v.(string); ok {
				r.PostForm.Set(k, sv)
			}
		}
	} else if err := r.ParseForm(); err != nil {
		reqlog.Reject(r, "invalid request: body is not application/x-www-form-urlencoded")
		httputil.OAuthError(w, 400, "invalid_request", "body must be application/x-www-form-urlencoded")
		return
	}
	s.purgeExpired()
	c, ok := s.clientAuth(w, r)
	if !ok {
		return
	}
	resource := strings.TrimRight(r.PostFormValue("resource"), "/")
	if resource != "" && !s.validResource(resource) {
		reqlog.Reject(r, "invalid_target: resource %q does not identify this server", truncate(resource, 120))
		httputil.OAuthError(w, 400, "invalid_target", "resource does not identify this server")
		return
	}
	switch r.PostFormValue("grant_type") {
	case "authorization_code":
		s.grantCode(w, r, c, resource)
	case "refresh_token":
		s.grantRefresh(w, r, c, resource)
	case "":
		reqlog.Reject(r, "invalid request: grant_type is required")
		httputil.OAuthError(w, 400, "invalid_request", "grant_type is required")
	default:
		reqlog.Reject(r, "unsupported grant_type %q", truncate(r.PostFormValue("grant_type"), 40))
		httputil.OAuthError(w, 400, "unsupported_grant_type", "supported: authorization_code, refresh_token")
	}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func pkceOK(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(strings.TrimRight(challenge, "="))) == 1
}

func (s *Server) grantCode(w http.ResponseWriter, r *http.Request, c Client, resource string) {
	code := r.PostFormValue("code")
	verifier := r.PostFormValue("code_verifier")
	if code == "" || (verifier == "" && !c.PKCEOptional()) {
		reqlog.Reject(r, "invalid request: code and code_verifier are required (PKCE)")
		httputil.OAuthError(w, 400, "invalid_request", "code and code_verifier are required")
		return
	}
	// Single use: the code is deleted whether or not the rest of the checks pass.
	var client, redirect, challenge, res, scope string
	var sub, email sql.NullString
	var exp int64
	err := s.DB.QueryRow(`DELETE FROM oauth_codes WHERE hash=? RETURNING client_id,redirect_uri,challenge,resource,scope,expires_at,sub,email`,
		httputil.SHA256Hex(code)).Scan(&client, &redirect, &challenge, &res, &scope, &exp, &sub, &email)
	if err != nil || client != c.ID || time.Now().Unix() >= exp {
		switch {
		case err != nil:
			reqlog.Reject(r, "invalid_grant: authorization code is unknown or already used")
		case client != c.ID:
			reqlog.Reject(r, "invalid_grant: authorization code was issued to a different client")
		default:
			reqlog.Reject(r, "invalid_grant: authorization code has expired")
		}
		httputil.OAuthError(w, 400, "invalid_grant", "authorization code is invalid, expired or already used")
		return
	}
	if ru := r.PostFormValue("redirect_uri"); ru != "" && ru != redirect {
		reqlog.Redirect(r, ru)
		reqlog.Reject(r, "invalid_grant: redirect_uri differs from the authorization request (token host %s, authorize host %s)", reqlog.HostOf(ru), reqlog.HostOf(redirect))
		httputil.OAuthError(w, 400, "invalid_grant", "redirect_uri does not match the authorization request")
		return
	}
	switch {
	case challenge != "":
		// Issued with a challenge: always verified, whatever the client type.
		if !pkceOK(verifier, challenge) {
			reqlog.Reject(r, "PKCE mismatch: code_verifier does not match the code_challenge")
			httputil.OAuthError(w, 400, "invalid_grant", "PKCE verification failed")
			return
		}
		s.Clients.MarkPKCE(c.ID)
	case !c.PKCEOptional():
		// Issued without one, but this client is public or has used PKCE since.
		reqlog.Reject(r, "invalid_grant: the authorization request had no code_challenge and this client needs PKCE")
		httputil.OAuthError(w, 400, "invalid_grant", "PKCE verification failed")
		return
	}
	if resource == "" {
		resource = res
	}
	s.mintTokens(w, r, c, resource, scope, sub.String, email.String)
}

func (s *Server) grantRefresh(w http.ResponseWriter, r *http.Request, c Client, resource string) {
	rt := r.PostFormValue("refresh_token")
	if rt == "" {
		reqlog.Reject(r, "invalid request: refresh_token is required")
		httputil.OAuthError(w, 400, "invalid_request", "refresh_token is required")
		return
	}
	var client, res, scope string
	var sub, email sql.NullString
	var exp int64
	err := s.DB.QueryRow(`DELETE FROM oauth_tokens WHERE hash=? AND kind='refresh' RETURNING client_id,resource,scope,expires_at,sub,email`,
		httputil.SHA256Hex(rt)).Scan(&client, &res, &scope, &exp, &sub, &email)
	if err != nil || client != c.ID || time.Now().Unix() >= exp {
		switch {
		case err != nil:
			reqlog.Reject(r, "invalid_grant: refresh token is unknown or already used")
		case client != c.ID:
			reqlog.Reject(r, "invalid_grant: refresh token was issued to a different client")
		default:
			reqlog.Reject(r, "invalid_grant: refresh token has expired")
		}
		httputil.OAuthError(w, 400, "invalid_grant", "refresh token is invalid or expired")
		return
	}
	if resource == "" {
		resource = res
	}
	if req := strings.TrimSpace(r.PostFormValue("scope")); req != "" {
		scope = req
	}
	s.mintTokens(w, r, c, resource, scope, sub.String, email.String)
}

func (s *Server) mintTokens(w http.ResponseWriter, r *http.Request, c Client, resource, scope, sub, email string) {
	access := "skat_" + httputil.RandString(43)
	refresh := "skrt_" + httputil.RandString(43)
	now := time.Now()
	reqlog.Note(r, "tokens issued")
	tx, err := s.DB.Begin()
	if err != nil {
		reqlog.Reject(r, "server error: could not store tokens")
		httputil.OAuthError(w, 500, "server_error", "")
		return
	}
	defer tx.Rollback()
	for _, t := range []struct {
		tok, kind string
		ttl       time.Duration
	}{{access, "access", AccessTTL}, {refresh, "refresh", RefreshTTL}} {
		if _, err := tx.Exec(`INSERT INTO oauth_tokens(hash,kind,client_id,resource,scope,expires_at,sub,email) VALUES(?,?,?,?,?,?,?,?)`,
			httputil.SHA256Hex(t.tok), t.kind, c.ID, resource, scope, now.Add(t.ttl).Unix(), nullable(sub), nullable(email)); err != nil {
			httputil.OAuthError(w, 500, "server_error", "")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		httputil.OAuthError(w, 500, "server_error", "")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	httputil.JSON(w, 200, map[string]any{
		"access_token": access, "token_type": "Bearer", "expires_in": int(AccessTTL.Seconds()),
		"refresh_token": refresh, "scope": scope,
	})
}

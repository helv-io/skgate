// Package oidcauth implements OpenID Connect single sign-on (authorization code flow with PKCE S256,
// confidential client) for the admin UI. All provider endpoints come from
// {issuer}/.well-known/openid-configuration discovery, which is fetched lazily so a briefly
// unavailable IdP never prevents skgate from starting.
package oidcauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// DefaultScopes is used when OIDC_SCOPES is empty.
const DefaultScopes = "openid profile email groups"

const (
	flowCookie     = "skgate_oidc"
	flowTTL        = 10 * time.Minute
	discoveryTTL   = time.Hour
	discoveryRetry = 3 * time.Second
)

// ErrForbidden means the user authenticated but is not allowed to administer skgate.
var ErrForbidden = errors.New("user is not allowed to access the admin UI")

// Config is the OIDC configuration (from env).
type Config struct {
	Issuer        string // exact issuer identifier, e.g. https://auth.example.com
	ClientID      string
	ClientSecret  string
	Scopes        string
	AllowedEmails []string
	AllowedGroups []string
}

// Enabled reports whether enough is configured to run SSO.
func (c Config) Enabled() bool { return c.Issuer != "" && c.ClientID != "" && c.ClientSecret != "" }

// Identity is the authenticated user.
type Identity struct {
	Subject string
	Email   string
	Name    string
	// Username is the preferred_username claim.
	Username string
	Groups   []string
	IDToken  string // raw, kept only to send as id_token_hint on logout
}

// Display is a short human label for the user: preferred_username, else email, else name,
// and only as a last resort the opaque subject.
func (i Identity) Display() string {
	switch {
	case i.Username != "":
		return i.Username
	case i.Email != "":
		return i.Email
	case i.Name != "":
		return i.Name
	}
	return i.Subject
}

// Client runs the flow against one provider.
type Client struct {
	Cfg Config
	// Secret returns the HMAC key for the short-lived flow cookie.
	Secret func() []byte
	// RedirectURL returns the callback URL registered at the IdP.
	RedirectURL func() string
	// Secure reports whether cookies get the Secure attribute.
	Secure func() bool
	HTTP   *http.Client

	mu       sync.Mutex
	prov     *oidc.Provider
	meta     metadata
	fetched  time.Time
	lastTry  time.Time
	lastErr  error
	verifier *oidc.IDTokenVerifier
}

type metadata struct {
	EndSession    string   `json:"end_session_endpoint"`
	AuthMethods   []string `json:"token_endpoint_auth_methods_supported"`
	SigningAlgs   []string `json:"id_token_signing_alg_values_supported"`
	TokenEndpoint string   `json:"token_endpoint"`
}

// New builds a Client.
func New(cfg Config, secret func() []byte, redirect func() string, secure func() bool) *Client {
	if strings.TrimSpace(cfg.Scopes) == "" {
		cfg.Scopes = DefaultScopes
	}
	return &Client{Cfg: cfg, Secret: secret, RedirectURL: redirect, Secure: secure,
		HTTP: &http.Client{Timeout: 10 * time.Second}}
}

func (c *Client) ctx(ctx context.Context) context.Context { return oidc.ClientContext(ctx, c.HTTP) }

// discover returns the cached provider, (re)fetching discovery lazily with retry. When a refresh
// fails but a cached copy exists, the stale copy keeps being used.
func (c *Client) discover(ctx context.Context) (*oidc.Provider, *oidc.IDTokenVerifier, metadata, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.prov != nil && time.Since(c.fetched) < discoveryTTL {
		return c.prov, c.verifier, c.meta, nil
	}
	if time.Since(c.lastTry) < discoveryRetry && c.lastErr != nil {
		if c.prov != nil {
			return c.prov, c.verifier, c.meta, nil
		}
		return nil, nil, metadata{}, c.lastErr
	}
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt) * 300 * time.Millisecond):
			case <-ctx.Done():
				err = ctx.Err()
			}
			if ctx.Err() != nil {
				break
			}
		}
		var p *oidc.Provider
		// NewProvider fetches {issuer}/.well-known/openid-configuration and fails unless the
		// document's "issuer" equals the configured issuer exactly.
		p, err = oidc.NewProvider(c.ctx(ctx), c.Cfg.Issuer)
		if err != nil {
			continue
		}
		var m metadata
		if err = p.Claims(&m); err != nil {
			continue
		}
		c.prov, c.meta, c.fetched, c.lastErr, c.lastTry = p, m, time.Now(), nil, time.Now()
		c.verifier = p.Verifier(&oidc.Config{ClientID: c.Cfg.ClientID, SupportedSigningAlgs: asymmetric(m.SigningAlgs)})
		return c.prov, c.verifier, c.meta, nil
	}
	c.lastErr, c.lastTry = fmt.Errorf("oidc discovery failed: %w", err), time.Now()
	if c.prov != nil {
		return c.prov, c.verifier, c.meta, nil
	}
	return nil, nil, metadata{}, c.lastErr
}

// asymmetric keeps only signature algorithms verifiable with JWKS keys (never "none" or HMAC).
func asymmetric(algs []string) []string {
	var out []string
	for _, a := range algs {
		switch {
		case strings.HasPrefix(a, "RS"), strings.HasPrefix(a, "PS"), strings.HasPrefix(a, "ES"), a == "EdDSA":
			out = append(out, a)
		}
	}
	return out // empty: go-oidc defaults to RS256
}

func (c *Client) oauthConfig(p *oidc.Provider, m metadata) *oauth2.Config {
	ep := p.Endpoint()
	// Prefer client_secret_basic. Use client_secret_post only when the IdP advertises methods
	// and client_secret_basic is not among them.
	ep.AuthStyle = oauth2.AuthStyleInHeader
	if len(m.AuthMethods) > 0 && !contains(m.AuthMethods, "client_secret_basic") && contains(m.AuthMethods, "client_secret_post") {
		ep.AuthStyle = oauth2.AuthStyleInParams
	}
	return &oauth2.Config{
		ClientID: c.Cfg.ClientID, ClientSecret: c.Cfg.ClientSecret, Endpoint: ep,
		RedirectURL: c.RedirectURL(), Scopes: strings.Fields(strings.ReplaceAll(c.Cfg.Scopes, ",", " ")),
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

type flowState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Next     string `json:"x"`
	Exp      int64  `json:"e"`
}

func (c *Client) seal(v any) string {
	b, _ := json.Marshal(v)
	p := base64.RawURLEncoding.EncodeToString(b)
	return p + "." + c.mac(p)
}

func (c *Client) mac(p string) string {
	m := hmac.New(sha256.New, c.Secret())
	m.Write([]byte("oidc-flow:" + p))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (c *Client) unseal(val string, v any) bool {
	i := strings.LastIndex(val, ".")
	if i < 0 || subtle.ConstantTimeCompare([]byte(c.mac(val[:i])), []byte(val[i+1:])) != 1 {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(val[:i])
	return err == nil && json.Unmarshal(b, v) == nil
}

func randToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (c *Client) cookie(name, val, path string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: name, Value: val, Path: path, MaxAge: maxAge, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: c.Secure()}
}

// Begin starts the login: it stores state, nonce and the PKCE verifier in a short-lived signed
// cookie and redirects the browser to the discovered authorization endpoint.
func (c *Client) Begin(w http.ResponseWriter, r *http.Request, next string) error {
	p, _, m, err := c.discover(r.Context())
	if err != nil {
		return err
	}
	fs := flowState{State: randToken(24), Nonce: randToken(24), Verifier: oauth2.GenerateVerifier(), Next: next,
		Exp: time.Now().Add(flowTTL).Unix()}
	http.SetCookie(w, c.cookie(flowCookie, c.seal(fs), "/admin/oidc", int(flowTTL.Seconds())))
	u := c.oauthConfig(p, m).AuthCodeURL(fs.State, oidc.Nonce(fs.Nonce), oauth2.S256ChallengeOption(fs.Verifier))
	http.Redirect(w, r, u, http.StatusFound)
	return nil
}

// Finish handles the callback: it checks state, exchanges the code with the PKCE verifier,
// verifies the ID token (signature via JWKS, iss, aud, exp, nonce, at_hash when present) and
// applies the allow lists. It returns the identity and the original "next" path.
func (c *Client) Finish(w http.ResponseWriter, r *http.Request) (Identity, string, error) {
	var id Identity
	ck, err := r.Cookie(flowCookie)
	http.SetCookie(w, c.cookie(flowCookie, "", "/admin/oidc", -1)) // single use
	if err != nil {
		return id, "", errors.New("login session expired or cookies are blocked, start again")
	}
	var fs flowState
	if !c.unseal(ck.Value, &fs) || time.Now().Unix() > fs.Exp {
		return id, "", errors.New("login session invalid or expired, start again")
	}
	q := r.URL.Query()
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(fs.State)) != 1 || fs.State == "" {
		return id, "", errors.New("state mismatch")
	}
	if e := q.Get("error"); e != "" {
		return id, "", fmt.Errorf("identity provider returned an error: %s %s", e, q.Get("error_description"))
	}
	code := q.Get("code")
	if code == "" {
		return id, "", errors.New("missing authorization code")
	}
	p, ver, m, err := c.discover(r.Context())
	if err != nil {
		return id, "", err
	}
	ctx := c.ctx(r.Context())
	oc := c.oauthConfig(p, m)
	tok, err := oc.Exchange(ctx, code, oauth2.VerifierOption(fs.Verifier))
	if err != nil {
		return id, "", fmt.Errorf("token exchange failed: %w", err)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return id, "", errors.New("token response has no id_token")
	}
	idt, err := ver.Verify(ctx, raw)
	if err != nil {
		return id, "", fmt.Errorf("id token rejected: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(fs.Nonce)) != 1 || fs.Nonce == "" {
		return id, "", errors.New("nonce mismatch")
	}
	if idt.AccessTokenHash != "" {
		if err := idt.VerifyAccessToken(tok.AccessToken); err != nil {
			return id, "", fmt.Errorf("at_hash mismatch: %w", err)
		}
	}
	var cl claims
	if err := idt.Claims(&cl); err != nil {
		return id, "", err
	}
	id = Identity{Subject: idt.Subject, IDToken: raw}
	cl.apply(&id)
	restricted := len(c.Cfg.AllowedEmails) > 0 || len(c.Cfg.AllowedGroups) > 0
	needInfo := (len(c.Cfg.AllowedEmails) > 0 && id.Email == "") || (len(c.Cfg.AllowedGroups) > 0 && len(id.Groups) == 0)
	// A readable label (preferred_username) is also wanted for the admin header.
	if (needInfo || id.Username == "") && p.UserInfoEndpoint() != "" {
		// Many IdPs (Authelia, Authentik) only put email, groups and preferred_username in userinfo.
		if ui, err := p.UserInfo(ctx, oauth2.StaticTokenSource(tok)); err == nil && ui.Subject == idt.Subject {
			var uc claims
			if ui.Claims(&uc) == nil {
				cl.merge(uc)
				cl.apply(&id)
			}
		}
	}
	if restricted && !c.allowed(id, cl) {
		return id, fs.Next, ErrForbidden
	}
	return id, fs.Next, nil
}

type claims struct {
	Email         string   `json:"email"`
	EmailVerified *bool    `json:"email_verified"`
	Name          string   `json:"name"`
	Username      string   `json:"preferred_username"`
	Groups        []string `json:"groups"`
}

func (c *claims) merge(o claims) {
	if c.Email == "" {
		c.Email, c.EmailVerified = o.Email, o.EmailVerified
	}
	if c.Name == "" {
		c.Name = o.Name
	}
	if c.Username == "" {
		c.Username = o.Username
	}
	if len(c.Groups) == 0 {
		c.Groups = o.Groups
	}
}

func (c *claims) apply(id *Identity) {
	id.Email, id.Groups = c.Email, c.Groups
	id.Name, id.Username = c.Name, c.Username
}

// allowed applies OIDC_ALLOWED_EMAILS / OIDC_ALLOWED_GROUPS (either list matching is enough).
// An email only counts when the IdP does not say it is unverified.
func (c *Client) allowed(id Identity, cl claims) bool {
	if id.Email != "" && (cl.EmailVerified == nil || *cl.EmailVerified) {
		for _, e := range c.Cfg.AllowedEmails {
			if strings.EqualFold(e, id.Email) {
				return true
			}
		}
	}
	for _, g := range id.Groups {
		for _, ag := range c.Cfg.AllowedGroups {
			if g == ag {
				return true
			}
		}
	}
	return false
}

// EndSessionURL returns the discovered end_session_endpoint URL for RP-initiated logout, or ""
// when the IdP does not advertise one (or discovery is unavailable).
func (c *Client) EndSessionURL(ctx context.Context, idToken, postLogout string) string {
	_, _, m, err := c.discover(ctx)
	es := m.EndSession
	if err != nil || es == "" {
		return ""
	}
	u, err := url.Parse(es)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("client_id", c.Cfg.ClientID)
	if idToken != "" {
		q.Set("id_token_hint", idToken)
	}
	if postLogout != "" {
		q.Set("post_logout_redirect_uri", postLogout)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

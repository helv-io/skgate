// Package grok is the Grok (xAI) provider: OAuth device-code flow, browser PKCE with paste-back,
// token persistence in SQLite, and refresh.
package grok

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/provider"
	"github.com/helv-io/skgate/internal/store"
)

const (
	deviceGrant   = "urn:ietf:params:oauth:grant-type:device_code"
	refreshAhead  = 5 * time.Minute // background refresh window
	demandSkew    = 2 * time.Minute // refresh-on-demand window
	discoveryTTL  = time.Hour
	fallbackRedir = "http://127.0.0.1:56121/callback"
)

// Errors.
var (
	ErrNotSignedIn = errors.New("not signed in to Grok")
	ErrReauth      = errors.New("grok sign-in expired, sign in again")
	ErrTierBlocked = errors.New("grok account is not entitled to this OAuth surface (HTTP 403)")
)

// Endpoints are the discovered OAuth endpoints.
type Endpoints struct {
	Authorization string `json:"authorization_endpoint"`
	Device        string `json:"device_authorization_endpoint"`
	Token         string `json:"token_endpoint"`
	Userinfo      string `json:"userinfo_endpoint"`
}

// Client is the Grok OAuth client and token holder.
type Client struct {
	Cfg  *config.Config
	DB   *store.DB
	HTTP *http.Client

	// The public Grok CLI OAuth client (no secret) and API hosts. Fixed defaults; the API base and
	// fallback can be overridden per provider in the admin UI.
	Issuer, ClientID, Scopes, Base, Fallback string

	mu      sync.Mutex // guards tokens and refresh
	epMu    sync.Mutex
	ep      Endpoints
	epAt    time.Time
	devMu   sync.Mutex
	dev     *DeviceFlow
	devStop context.CancelFunc
	pkce    *pkceState
}

// New returns a Client.
func New(cfg *config.Config, db *store.DB) *Client {
	migrateKeys(db)
	return &Client{Cfg: cfg, DB: db, HTTP: &http.Client{Timeout: 30 * time.Second},
		Issuer: DefaultIssuer, ClientID: DefaultClientID, Scopes: DefaultScopes, Base: DefaultBase, Fallback: DefaultFallback}
}

// Defaults of the public Grok CLI client and API hosts.
const (
	DefaultClientID = "b1a00492-073a-47ea-816f-4c329264a828"
	DefaultScopes   = "openid profile email offline_access grok-cli:access api:access conversations:read conversations:write"
	DefaultIssuer   = "https://auth.x.ai"
	DefaultBase     = "https://api.x.ai/v1"
	DefaultFallback = "https://cli-chat-proxy.grok.com/v1"
)

// migrateKeys moves the token settings of older releases (xai_*) to the provider namespace.
func migrateKeys(db *store.DB) {
	for _, n := range []string{"access", "refresh", "id", "expires", "account", "state", "last_error"} {
		old := "xai_" + n
		if v, ok := db.GetSetting(old); ok {
			if _, have := db.GetSetting("provider.grok." + n); !have {
				_ = db.SetSetting("provider.grok."+n, v)
			}
			_ = db.DeleteSetting(old)
		}
	}
}

// Grok CLI headers, required by the subscription proxy hosts.
var cliHeaders = map[string]string{
	"x-xai-token-auth":         "xai-grok-cli",
	"x-grok-client-identifier": "grok-shell",
	"x-grok-client-version":    "0.2.93",
}

// ID implements provider.Provider.
func (c *Client) ID() string { return "grok" }

// Name implements provider.Provider.
func (c *Client) Name() string { return "Grok" }

// DefaultBase implements provider.Provider.
func (c *Client) DefaultBase() string { return strings.TrimRight(c.Base, "/") }

// DefaultFallback implements provider.Provider.
func (c *Client) DefaultFallback() string { return strings.TrimRight(c.Fallback, "/") }

// Headers implements provider.Provider: the CLI headers for the grok.com hosts.
func (c *Client) Headers(base string) map[string]string {
	if u, err := url.Parse(base); err == nil && strings.HasSuffix(u.Hostname(), "grok.com") {
		return cliHeaders
	}
	return nil
}

// BrowserPending reports whether a browser sign-in is waiting for its callback.
func (c *Client) BrowserPending() bool {
	c.devMu.Lock()
	defer c.devMu.Unlock()
	return c.pkce != nil && time.Now().Before(c.pkce.expires)
}

// endpoints returns the cached endpoints, else the documented paths; it never touches the network.
func (c *Client) endpoints() Endpoints {
	c.epMu.Lock()
	defer c.epMu.Unlock()
	if c.ep.Token != "" {
		return c.ep
	}
	iss := strings.TrimRight(c.Issuer, "/")
	return Endpoints{Authorization: iss + "/oauth2/authorize", Device: iss + "/oauth2/device/code", Token: iss + "/oauth2/token", Userinfo: iss + "/oauth2/userinfo"}
}

// Info implements provider.Provider.
func (c *Client) Info() []provider.InfoRow {
	ep := c.endpoints()
	return []provider.InfoRow{
		{Name: "Client ID", Value: c.ClientID},
		{Name: "Scopes", Value: c.Scopes},
		{Name: "Issuer", Value: strings.TrimRight(c.Issuer, "/")},
		{Name: "Authorization endpoint", Value: ep.Authorization},
		{Name: "Device endpoint", Value: ep.Device},
		{Name: "Token endpoint", Value: ep.Token},
		{Name: "Userinfo endpoint", Value: ep.Userinfo},
		{Name: "Redirect URI", Value: c.RedirectURI()},
		{Name: "PKCE", Value: "S256, automatic for browser sign-in"},
	}
}

// Discover returns OAuth endpoints from {issuer}/.well-known/openid-configuration, falling back
// to the documented auth.x.ai paths when discovery fails.
func (c *Client) Discover(ctx context.Context) Endpoints {
	c.epMu.Lock()
	defer c.epMu.Unlock()
	if c.ep.Token != "" && time.Since(c.epAt) < discoveryTTL {
		return c.ep
	}
	iss := strings.TrimRight(c.Issuer, "/")
	fb := Endpoints{Authorization: iss + "/oauth2/authorize", Device: iss + "/oauth2/device/code", Token: iss + "/oauth2/token", Userinfo: iss + "/oauth2/userinfo"}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, iss+"/.well-known/openid-configuration", nil)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fb
	}
	defer resp.Body.Close()
	var e Endpoints
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&e) != nil || e.Token == "" {
		return fb
	}
	if e.Authorization == "" {
		e.Authorization = fb.Authorization
	}
	if e.Device == "" {
		e.Device = fb.Device
	}
	c.ep, c.epAt = e, time.Now()
	return e
}

// ---- token persistence ----

// Status returns the current sign-in status.
func (c *Client) Status() provider.Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := provider.Status{}
	acc, _ := c.DB.GetSetting("provider.grok.access")
	ref, _ := c.DB.GetSetting("provider.grok.refresh")
	s.SignedIn = acc != "" && ref != ""
	s.AccessMasked, s.RefreshMasked = httputil.Mask(acc), httputil.Mask(ref)
	if v, ok := c.DB.GetSetting("provider.grok.expires"); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			s.Expires = time.Unix(n, 0)
			s.ExpiresIn = time.Until(s.Expires)
		}
	}
	s.Account, _ = c.DB.GetSetting("provider.grok.account")
	s.State, _ = c.DB.GetSetting("provider.grok.state")
	s.LastError, _ = c.DB.GetSetting("provider.grok.last_error")
	return s
}

type tokens struct {
	Access, Refresh, ID string
	Expires             time.Time
}

func (c *Client) load() tokens {
	a, _ := c.DB.GetSetting("provider.grok.access")
	r, _ := c.DB.GetSetting("provider.grok.refresh")
	i, _ := c.DB.GetSetting("provider.grok.id")
	var exp time.Time
	if v, ok := c.DB.GetSetting("provider.grok.expires"); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			exp = time.Unix(n, 0)
		}
	}
	return tokens{a, r, i, exp}
}

func (c *Client) save(t tokens) {
	_ = c.DB.SetSetting("provider.grok.access", t.Access)
	_ = c.DB.SetSetting("provider.grok.refresh", t.Refresh)
	_ = c.DB.SetSetting("provider.grok.id", t.ID)
	_ = c.DB.SetSetting("provider.grok.expires", strconv.FormatInt(t.Expires.Unix(), 10))
	_ = c.DB.SetSetting("provider.grok.state", "ok")
	_ = c.DB.SetSetting("provider.grok.last_error", "")
}

// SetTokens stores tokens directly (used by tests).
func (c *Client) SetTokens(access, refresh string, expires time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.save(tokens{Access: access, Refresh: refresh, Expires: expires})
}

// SignOut clears stored tokens.
func (c *Client) SignOut() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range []string{"provider.grok.access", "provider.grok.refresh", "provider.grok.id", "provider.grok.expires", "provider.grok.account", "provider.grok.state", "provider.grok.last_error"} {
		_ = c.DB.DeleteSetting(k)
	}
}

type tokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    any    `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

func jwtExp(tok string) time.Time {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if json.Unmarshal(b, &claims) != nil || claims.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(int64(claims.Exp), 0)
}

func (r tokenResp) toTokens(prev tokens) (tokens, error) {
	if r.AccessToken == "" {
		return tokens{}, errors.New("token response has no access_token")
	}
	t := tokens{Access: r.AccessToken, Refresh: r.RefreshToken, ID: r.IDToken}
	if t.Refresh == "" {
		t.Refresh = prev.Refresh // refresh tokens rotate; keep the old one if the server omits it
	}
	if t.ID == "" {
		t.ID = prev.ID
	}
	var secs float64
	switch v := r.ExpiresIn.(type) {
	case float64:
		secs = v
	case string:
		secs, _ = strconv.ParseFloat(v, 64)
	}
	switch {
	case secs > 0:
		t.Expires = time.Now().Add(time.Duration(secs) * time.Second)
	case !jwtExp(t.Access).IsZero():
		t.Expires = jwtExp(t.Access)
	default:
		t.Expires = time.Now().Add(15 * time.Minute)
	}
	return t, nil
}

func (c *Client) postForm(ctx context.Context, endpoint string, form url.Values) (int, tokenResp, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, tokenResp{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, tokenResp{}, err
	}
	defer resp.Body.Close()
	var tr tokenResp
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tr)
	return resp.StatusCode, tr, nil
}

// ---- refresh ----

// Refresh exchanges the refresh token for a new access token (rotating the refresh token).
func (c *Client) Refresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refreshLocked(ctx)
}

func (c *Client) refreshLocked(ctx context.Context) error {
	prev := c.load()
	if prev.Refresh == "" {
		return ErrNotSignedIn
	}
	ep := c.Discover(ctx)
	code, tr, err := c.postForm(ctx, ep.Token, url.Values{
		"grant_type": {"refresh_token"}, "client_id": {c.ClientID}, "refresh_token": {prev.Refresh},
	})
	if err != nil {
		log.Printf("xai: token refresh request failed: %v", err)
		_ = c.DB.SetSetting("provider.grok.last_error", "refresh: "+err.Error())
		return err
	}
	switch {
	case code == http.StatusForbidden:
		log.Printf("xai: token refresh refused: HTTP 403, the account is not entitled to this OAuth surface")
		_ = c.DB.SetSetting("provider.grok.state", "tier_blocked")
		_ = c.DB.SetSetting("provider.grok.last_error", "refresh returned HTTP 403")
		return ErrTierBlocked
	case code == 400 || code == 401 || tr.Error == "invalid_grant":
		log.Printf("xai: token refresh rejected (HTTP %d %s): signed out, sign in again", code, firstNonEmpty(tr.Error, "-"))
		for _, k := range []string{"provider.grok.access", "provider.grok.refresh", "provider.grok.expires"} {
			_ = c.DB.DeleteSetting(k)
		}
		_ = c.DB.SetSetting("provider.grok.state", "reauth")
		_ = c.DB.SetSetting("provider.grok.last_error", "refresh rejected: "+firstNonEmpty(tr.Error, strconv.Itoa(code)))
		return ErrReauth
	case code < 200 || code > 299:
		log.Printf("xai: token refresh failed: HTTP %d", code)
		_ = c.DB.SetSetting("provider.grok.last_error", fmt.Sprintf("refresh HTTP %d", code))
		return fmt.Errorf("refresh: HTTP %d", code)
	}
	t, err := tr.toTokens(prev)
	if err != nil {
		return err
	}
	c.save(t)
	return nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// Token returns a live access token, refreshing on demand when it is about to expire.
func (c *Client) Token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.load()
	if t.Access == "" {
		if st, _ := c.DB.GetSetting("provider.grok.state"); st == "reauth" {
			return "", ErrReauth
		}
		return "", ErrNotSignedIn
	}
	if time.Until(t.Expires) < demandSkew {
		if err := c.refreshLocked(ctx); err != nil {
			return "", err
		}
		t = c.load()
	}
	return t.Access, nil
}

// ForceRefresh refreshes now and returns the new access token (used after an upstream 401).
func (c *Client) ForceRefresh(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.refreshLocked(ctx); err != nil {
		return "", err
	}
	return c.load().Access, nil
}

// Run refreshes tokens in the background about five minutes before they expire.
func (c *Client) Run(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			st := c.Status()
			if st.SignedIn && st.ExpiresIn < refreshAhead {
				if err := c.Refresh(ctx); err != nil {
					log.Printf("xai: background refresh failed: %v", err)
				}
			}
		}
	}
}

func (c *Client) fetchAccount(ctx context.Context, access string) {
	ep := c.Discover(ctx)
	if ep.Userinfo == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ep.Userinfo, nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}
	var u struct{ Email, Name string }
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&u) == nil {
		if a := firstNonEmpty(u.Email, u.Name); a != "" {
			_ = c.DB.SetSetting("provider.grok.account", a)
		}
	}
}

// ---- device flow (RFC 8628) ----

// DeviceFlow is the snapshot type shared by all providers.
type DeviceFlow = provider.DeviceFlow

// StartDevice begins a device-code sign-in and polls the token endpoint in the background.
func (c *Client) StartDevice(ctx context.Context) (DeviceFlow, error) {
	ep := c.Discover(ctx)
	form := url.Values{"client_id": {c.ClientID}, "scope": {c.Scopes}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ep.Device, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		log.Printf("xai: device authorization request failed: %v", err)
		return DeviceFlow{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var d struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		VerificationURL string `json:"verification_url"`
		Complete        string `json:"verification_uri_complete"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
		Error           string `json:"error"`
	}
	_ = json.Unmarshal(body, &d)
	if resp.StatusCode/100 != 2 || d.DeviceCode == "" {
		log.Printf("xai: device authorization failed: HTTP %d %s", resp.StatusCode, d.Error)
		return DeviceFlow{}, fmt.Errorf("device authorization failed: HTTP %d %s", resp.StatusCode, d.Error)
	}
	if d.ExpiresIn <= 0 {
		d.ExpiresIn = 900
	}
	if d.Interval < 1 {
		d.Interval = 5
	}
	f := &DeviceFlow{
		UserCode: d.UserCode, VerificationURI: firstNonEmpty(d.VerificationURI, d.VerificationURL),
		VerificationURIComplete: d.Complete, ExpiresAt: time.Now().Add(time.Duration(d.ExpiresIn) * time.Second),
		Interval: time.Duration(d.Interval) * time.Second, State: "pending", DeviceCode: d.DeviceCode,
	}
	c.devMu.Lock()
	if c.devStop != nil {
		c.devStop()
	}
	pctx, cancel := context.WithDeadline(context.Background(), f.ExpiresAt)
	c.dev, c.devStop = f, cancel
	c.devMu.Unlock()
	go c.pollDevice(pctx, f, ep.Token)
	return c.Device(), nil
}

// Device returns the current device flow snapshot (zero value State "" when none).
func (c *Client) Device() DeviceFlow {
	c.devMu.Lock()
	defer c.devMu.Unlock()
	if c.dev == nil {
		return DeviceFlow{}
	}
	d := *c.dev
	d.DeviceCode = ""
	return d
}

// CancelDevice stops any running device flow.
func (c *Client) CancelDevice() {
	c.devMu.Lock()
	defer c.devMu.Unlock()
	if c.devStop != nil {
		c.devStop()
	}
	c.dev, c.devStop = nil, nil
}

func (c *Client) setDev(f *DeviceFlow, state, errMsg string) {
	c.devMu.Lock()
	defer c.devMu.Unlock()
	if c.dev == f {
		f.State, f.Err = state, errMsg
		if state == "error" {
			log.Printf("xai: device sign-in failed: %s", errMsg)
		}
	}
}

func (c *Client) pollDevice(ctx context.Context, f *DeviceFlow, tokenURL string) {
	interval := f.Interval
	for {
		select {
		case <-ctx.Done():
			c.setDev(f, "error", "device code expired, start again")
			return
		case <-time.After(interval):
		}
		code, tr, err := c.postForm(ctx, tokenURL, url.Values{
			"grant_type": {deviceGrant}, "client_id": {c.ClientID}, "device_code": {f.DeviceCode},
		})
		if err != nil {
			log.Printf("xai: device sign-in poll failed, retrying: %v", err)
			continue
		}
		if code == http.StatusForbidden {
			c.setDev(f, "error", "account is not entitled to this OAuth surface (HTTP 403)")
			return
		}
		switch tr.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			if interval > 30*time.Second {
				interval = 30 * time.Second
			}
			continue
		case "access_denied", "authorization_denied":
			c.setDev(f, "error", "access denied")
			return
		case "expired_token":
			c.setDev(f, "error", "device code expired, start again")
			return
		case "":
		default:
			c.setDev(f, "error", "sign-in failed: "+tr.Error)
			return
		}
		if code/100 != 2 {
			c.setDev(f, "error", fmt.Sprintf("token endpoint HTTP %d", code))
			return
		}
		t, err := tr.toTokens(tokens{})
		if err != nil || t.Refresh == "" {
			c.setDev(f, "error", "token response is missing access_token or refresh_token")
			return
		}
		c.mu.Lock()
		c.save(t)
		c.mu.Unlock()
		c.fetchAccount(ctx, t.Access)
		c.setDev(f, "done", "")
		return
	}
}

// ---- browser PKCE ----

type pkceState struct {
	verifier, state, redirect string
	expires                   time.Time
}

// RedirectURI is the redirect used for the browser flow: PUBLIC_URL/admin/oauth/callback.
func (c *Client) RedirectURI() string {
	return c.Cfg.PublicURL + "/admin/oauth/callback"
}

func randB64(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// StartBrowser returns the authorization URL for the PKCE flow and remembers verifier+state.
func (c *Client) StartBrowser(ctx context.Context) string {
	ep := c.Discover(ctx)
	verifier := randB64(48)
	sum := sha256.Sum256([]byte(verifier))
	st := &pkceState{verifier: verifier, state: randB64(24), redirect: c.RedirectURI(), expires: time.Now().Add(15 * time.Minute)}
	c.devMu.Lock()
	c.pkce = st
	c.devMu.Unlock()
	q := url.Values{
		"response_type": {"code"}, "client_id": {c.ClientID}, "redirect_uri": {st.redirect},
		"scope": {c.Scopes}, "state": {st.state},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
	}
	return ep.Authorization + "?" + q.Encode()
}

// FinishBrowser exchanges the authorization code for tokens.
func (c *Client) FinishBrowser(ctx context.Context, code, state string) (err error) {
	defer func() {
		if err != nil {
			log.Printf("xai: browser sign-in failed: %v", err)
		}
	}()
	c.devMu.Lock()
	p := c.pkce
	c.devMu.Unlock()
	if p == nil || time.Now().After(p.expires) {
		return errors.New("no pending browser sign-in, start again")
	}
	if code == "" {
		return errors.New("no authorization code found")
	}
	if state != "" && state != p.state {
		return errors.New("state mismatch")
	}
	ep := c.Discover(ctx)
	status, tr, err := c.postForm(ctx, ep.Token, url.Values{
		"grant_type": {"authorization_code"}, "client_id": {c.ClientID}, "code": {code},
		"code_verifier": {p.verifier}, "redirect_uri": {p.redirect},
	})
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return fmt.Errorf("token exchange failed: HTTP %d %s", status, firstNonEmpty(tr.ErrorDesc, tr.Error))
	}
	t, err := tr.toTokens(tokens{})
	if err != nil || t.Refresh == "" {
		return errors.New("token response is missing access_token or refresh_token")
	}
	c.mu.Lock()
	c.save(t)
	c.mu.Unlock()
	c.devMu.Lock()
	c.pkce = nil
	c.devMu.Unlock()
	c.fetchAccount(ctx, t.Access)
	return nil
}

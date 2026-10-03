// Package oidctest is a fake OpenID Connect provider for tests. Its endpoints deliberately live
// under non-standard paths so tests can prove clients use discovery only.
package oidctest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// Provider is a fake IdP. Exported fields may be changed by tests before or between flows.
type Provider struct {
	*httptest.Server
	Key, OtherKey *rsa.PrivateKey
	ClientID      string
	ClientSecret  string

	mu             sync.Mutex
	Hits           []string
	Down           bool
	OnlyPost       bool   // advertise only client_secret_post and reject basic auth
	HideMethods    bool   // leave token_endpoint_auth_methods_supported out of discovery
	BasicHits      int    // token requests with client_secret_basic
	PostHits       int    // token requests with the secret in the form
	IssuerOverride string // discovery "issuer" value that differs from the URL
	NoEndSession   bool
	Sub, Email     string
	Username       string // preferred_username
	EmailVerified  *bool
	Groups         []string
	InfoOnly       bool   // email and groups only in userinfo, not in the ID token
	Aud            string // override aud
	NonceOverride  string // override nonce in the ID token
	SignWithOther  bool   // sign with a key that is not in the JWKS
	Expired        bool
	codes          map[string]codeInfo
	BasicAuthSeen  bool
	PostAuthSeen   bool
}

type codeInfo struct{ nonce, challenge, redirect string }

// New starts a provider. Close it with Close (registered on t.Cleanup).
func New(t *testing.T) *Provider {
	t.Helper()
	k1, _ := rsa.GenerateKey(rand.Reader, 2048)
	k2, _ := rsa.GenerateKey(rand.Reader, 2048)
	p := &Provider{Key: k1, OtherKey: k2, ClientID: "skgate", ClientSecret: "s3cret", Sub: "u-1",
		Email: "admin@example.com", Groups: []string{"admins"}, codes: map[string]codeInfo{}}
	p.Server = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.Server.Close)
	return p
}

// Paths returns the request paths seen so far.
func (p *Provider) Paths() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.Hits...)
}

// SetDown makes every endpoint answer 503.
func (p *Provider) SetDown(v bool) { p.mu.Lock(); p.Down = v; p.mu.Unlock() }

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (p *Provider) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.Hits = append(p.Hits, r.URL.Path)
	down := p.Down
	p.mu.Unlock()
	if down {
		http.Error(w, "down", 503)
		return
	}
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		iss := p.URL
		if p.IssuerOverride != "" {
			iss = p.IssuerOverride
		}
		methods := []string{"client_secret_basic", "client_secret_post"}
		if p.OnlyPost {
			methods = []string{"client_secret_post"}
		}
		doc := map[string]any{
			"issuer": iss, "authorization_endpoint": p.URL + "/x/authz", "token_endpoint": p.URL + "/x/token",
			"jwks_uri": p.URL + "/x/keys", "userinfo_endpoint": p.URL + "/x/userinfo",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"token_endpoint_auth_methods_supported": methods,
			"response_types_supported":              []string{"code"}, "subject_types_supported": []string{"public"},
		}
		if p.HideMethods {
			delete(doc, "token_endpoint_auth_methods_supported")
		}
		if !p.NoEndSession {
			doc["end_session_endpoint"] = p.URL + "/x/logout"
		}
		writeJSON(w, doc)
	case "/x/keys":
		writeJSON(w, map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "k1", "n": b64(p.Key.N.Bytes()),
			"e": b64(big.NewInt(int64(p.Key.E)).Bytes())}}})
	case "/x/authz":
		q := r.URL.Query()
		if q.Get("client_id") != p.ClientID || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" ||
			q.Get("state") == "" || q.Get("nonce") == "" || q.Get("code_challenge") == "" || !strings.Contains(q.Get("scope"), "openid") {
			http.Error(w, "bad authz request", 400)
			return
		}
		code := b64(randBytes(12))
		p.mu.Lock()
		p.codes[code] = codeInfo{q.Get("nonce"), q.Get("code_challenge"), q.Get("redirect_uri")}
		p.mu.Unlock()
		u, _ := url.Parse(q.Get("redirect_uri"))
		v := u.Query()
		v.Set("code", code)
		v.Set("state", q.Get("state"))
		u.RawQuery = v.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	case "/x/token":
		p.token(w, r)
	case "/x/userinfo":
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer at-") {
			http.Error(w, "no token", 401)
			return
		}
		writeJSON(w, p.userinfo())
	case "/x/logout":
		w.WriteHeader(200)
	default:
		http.NotFound(w, r)
	}
}

func (p *Provider) userinfo() map[string]any {
	m := map[string]any{"sub": p.Sub}
	if p.Email != "" {
		m["email"] = p.Email
	}
	if p.Username != "" {
		m["preferred_username"] = p.Username
	}
	if p.EmailVerified != nil {
		m["email_verified"] = *p.EmailVerified
	}
	if p.Groups != nil {
		m["groups"] = p.Groups
	}
	return m
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id, sec, basic := r.BasicAuth()
	if !basic {
		id, sec = r.PostFormValue("client_id"), r.PostFormValue("client_secret")
	}
	p.mu.Lock()
	if basic {
		p.BasicAuthSeen = true
		p.BasicHits++
	} else if sec != "" {
		p.PostAuthSeen = true
		p.PostHits++
	}
	p.mu.Unlock()
	if p.OnlyPost && basic {
		writeStatus(w, 401, map[string]string{"error": "invalid_client"})
		return
	}
	if id != p.ClientID || sec != p.ClientSecret {
		writeStatus(w, 401, map[string]string{"error": "invalid_client"})
		return
	}
	p.mu.Lock()
	ci, ok := p.codes[r.PostFormValue("code")]
	delete(p.codes, r.PostFormValue("code"))
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostFormValue("code_verifier")))
	if !ok || r.PostFormValue("grant_type") != "authorization_code" || ci.redirect != r.PostFormValue("redirect_uri") ||
		b64(sum[:]) != ci.challenge {
		writeStatus(w, 400, map[string]string{"error": "invalid_grant"})
		return
	}
	nonce := ci.nonce
	if p.NonceOverride != "" {
		nonce = p.NonceOverride
	}
	aud := p.ClientID
	if p.Aud != "" {
		aud = p.Aud
	}
	exp := time.Now().Add(5 * time.Minute)
	if p.Expired {
		exp = time.Now().Add(-time.Hour)
	}
	claims := map[string]any{"iss": p.URL, "sub": p.Sub, "aud": aud, "exp": exp.Unix(), "iat": time.Now().Unix(), "nonce": nonce}
	if !p.InfoOnly {
		if p.Username != "" {
			claims["preferred_username"] = p.Username
		}
		if p.Email != "" {
			claims["email"] = p.Email
		}
		if p.EmailVerified != nil {
			claims["email_verified"] = *p.EmailVerified
		}
		if p.Groups != nil {
			claims["groups"] = p.Groups
		}
	}
	writeJSON(w, map[string]any{"access_token": "at-" + b64(randBytes(6)), "token_type": "Bearer", "expires_in": 300,
		"id_token": p.sign(claims)})
}

func (p *Provider) sign(claims map[string]any) string {
	key := p.Key
	if p.SignWithOther {
		key = p.OtherKey
	}
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"})
	c, _ := json.Marshal(claims)
	in := b64(h) + "." + b64(c)
	sum := sha256.Sum256([]byte(in))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	return in + "." + b64(sig)
}

// Authorize performs the browser step: it GETs the authorization URL and returns the callback URL
// the IdP redirected to.
func (p *Provider) Authorize(t *testing.T, authURL string) string {
	t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 302 {
		t.Fatalf("authorization endpoint answered %d", resp.StatusCode)
	}
	return resp.Header.Get("Location")
}

func randBytes(n int) []byte { b := make([]byte, n); _, _ = rand.Read(b); return b }

func writeJSON(w http.ResponseWriter, v any) { writeStatus(w, 200, v) }

func writeStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

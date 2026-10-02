package admin

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/oidcauth"
)

const (
	cookieName = "skgate_session"
	sessionTTL = 12 * time.Hour
)

// sessionInfo is kept in memory next to the signed cookie (display name, id_token for logout).
type sessionInfo struct {
	who     string
	idToken string
	exp     time.Time
}

type sessionStore struct {
	mu sync.Mutex
	m  map[string]sessionInfo
}

func (s *sessionStore) put(nonce string, si sessionInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]sessionInfo{}
	}
	for k, v := range s.m {
		if time.Now().After(v.exp) {
			delete(s.m, k)
		}
	}
	s.m[nonce] = si
}

func (s *sessionStore) get(nonce string) (sessionInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	si, ok := s.m[nonce]
	return si, ok && time.Now().Before(si.exp)
}

func (s *sessionStore) del(nonce string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, nonce)
}

// secret is the HMAC key for cookies, random and persisted in SQLite.
func (a *Admin) secret() []byte {
	if v, ok := a.DB.GetSetting("session_secret"); ok && v != "" {
		if b, err := base64.RawStdEncoding.DecodeString(v); err == nil && len(b) >= 32 {
			return b
		}
	}
	b := []byte(httputil.RandString(48))
	_ = a.DB.SetSetting("session_secret", base64.RawStdEncoding.EncodeToString(b))
	return b
}

func (a *Admin) secure() bool { return strings.HasPrefix(a.Cfg.PublicURL, "https://") }

func (a *Admin) sign(msg string) string {
	m := hmac.New(sha256.New, a.secret())
	m.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// OIDC returns the SSO client, or nil when OIDC_* is not configured.
func (a *Admin) OIDC() *oidcauth.Client { return a.oidc }

// sessionClaims is carried inside the signed session cookie so the OIDC subject and email
// survive restarts and can be recorded on MCP authorization codes and tokens.
type sessionClaims struct {
	Sub   string `json:"s"`
	Email string `json:"e,omitempty"`
	// Label is the display name shown in the admin header (preferred_username, else email, else
	// name, else the subject). It lives in the signed cookie so it survives restarts.
	Label string `json:"l,omitempty"`
}

// label returns the header label, tolerating cookies issued before labels existed.
func (c sessionClaims) label() string {
	switch {
	case c.Label != "":
		return c.Label
	case c.Email != "":
		return c.Email
	}
	return c.Sub
}

func (a *Admin) setSession(w http.ResponseWriter, id oidcauth.Identity) {
	nonce := httputil.RandString(16)
	exp := time.Now().Add(sessionTTL)
	cj, _ := json.Marshal(sessionClaims{Sub: id.Subject, Email: id.Email, Label: id.Display()})
	payload := strconv.FormatInt(exp.Unix(), 10) + "." + nonce + "." + base64.RawURLEncoding.EncodeToString(cj)
	a.sessions.put(nonce, sessionInfo{who: id.Display(), idToken: id.IDToken, exp: exp})
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: payload + "." + a.sign(payload), Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.secure(), Expires: exp,
	})
}

func (a *Admin) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: a.secure()})
}

func (a *Admin) parseSession(r *http.Request) (nonce, payload string, cl sessionClaims, ok bool) {
	// No OIDC configuration means no valid session, ever.
	if !a.Cfg.OIDCEnabled() {
		return "", "", cl, false
	}
	c, err := r.Cookie(cookieName)
	if err != nil {
		return "", "", cl, false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 4 {
		return "", "", cl, false
	}
	payload = strings.Join(parts[:3], ".")
	if subtle.ConstantTimeCompare([]byte(a.sign(payload)), []byte(parts[3])) != 1 {
		return "", "", cl, false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", "", cl, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || json.Unmarshal(raw, &cl) != nil || cl.Sub == "" {
		return "", "", cl, false
	}
	return parts[1], payload, cl, true
}

// Session returns the CSRF token when the request has a valid admin session.
func (a *Admin) Session(r *http.Request) (string, bool) {
	_, payload, _, ok := a.parseSession(r)
	if !ok {
		return "", false
	}
	return a.sign("csrf:" + payload), true
}

// Identity returns the OIDC subject and email of the signed-in admin.
func (a *Admin) Identity(r *http.Request) (subject, email string, ok bool) {
	_, _, cl, ok := a.parseSession(r)
	return cl.Sub, cl.Email, ok
}

// sessionWho returns the session nonce and the in-memory info (id_token for logout, empty after a restart).
func (a *Admin) sessionWho(r *http.Request) (nonce string, si sessionInfo) {
	n, _, _, ok := a.parseSession(r)
	if !ok {
		return "", sessionInfo{}
	}
	si, _ = a.sessions.get(n)
	return n, si
}

// Label returns the display label of the signed-in admin, read from the signed cookie.
func (a *Admin) Label(r *http.Request) string {
	_, _, cl, ok := a.parseSession(r)
	if !ok {
		return ""
	}
	return cl.label()
}

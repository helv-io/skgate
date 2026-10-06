// Package httputil holds small HTTP helpers shared by skgate packages.
package httputil

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

// RandString returns n random url-safe characters (64 symbol alphabet, no modulo bias).
func RandString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	for i := range b {
		b[i] = alphabet[int(b[i])&63]
	}
	return string(b)
}

// SHA256Hex returns the lowercase hex SHA-256 of s.
func SHA256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// MaskStars is the fixed asterisk run used by Mask (fixed so the length is not revealed).
const MaskStars = "************"

// Mask renders a secret for display: 12 asterisks followed by the last 4 characters (for example
// ************abcd). Values shorter than 8 characters show asterisks only, so never more than the
// last 4 characters of a secret are revealed. An empty value renders as "".
func Mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) < 8 {
		return MaskStars
	}
	return MaskStars + s[len(s)-4:]
}

// JSON writes v as JSON with the given status.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// OAuthError writes an RFC 6749 style JSON error.
func OAuthError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	m := map[string]string{"error": code}
	if desc != "" {
		m["error_description"] = desc
	}
	JSON(w, status, m)
}

// CORS wraps h with permissive CORS for bearer-token (cookie-less) endpoints and answers preflights.
func CORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// SetCORS sets the CORS response headers.
func SetCORS(w http.ResponseWriter) {
	hd := w.Header()
	hd.Set("Access-Control-Allow-Origin", "*")
	hd.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	hd.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, X-API-Key, X-Upstream-Authorization, Mcp-Session-Id, Mcp-Protocol-Version, Mcp-Method, Mcp-Name, Last-Event-ID")
	hd.Set("Access-Control-Expose-Headers", "WWW-Authenticate, Mcp-Session-Id, Mcp-Protocol-Version")
	hd.Set("Access-Control-Max-Age", "600")
}

// BearerToken extracts a Bearer token from the Authorization header (scheme is case-insensitive).
func BearerToken(r *http.Request) string {
	v := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
		return strings.TrimSpace(v[7:])
	}
	return ""
}

var hopByHop = map[string]bool{
	"Connection": true, "Proxy-Connection": true, "Keep-Alive": true, "Proxy-Authenticate": true,
	"Proxy-Authorization": true, "Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true,
}

// CopyResponse relays an upstream response to w, streaming with a flush after every read
// so SSE works. Headers named in drop (canonical form) are not copied.
func CopyResponse(w http.ResponseWriter, resp *http.Response, drop ...string) {
	skip := map[string]bool{}
	for _, d := range drop {
		skip[http.CanonicalHeaderKey(d)] = true
	}
	for k, vv := range resp.Header {
		if hopByHop[k] || skip[k] || strings.HasPrefix(k, "Access-Control-") {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	rc := http.NewResponseController(w)
	_ = rc.Flush()
	buf := make([]byte, 16*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			_ = rc.Flush()
		}
		if err != nil {
			if err != io.EOF {
				return
			}
			return
		}
	}
}

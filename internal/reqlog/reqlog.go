// Package reqlog logs requests to the OAuth/OIDC and MCP endpoints, one line per request, so a
// failing MCP client (which usually only reports a generic "trouble connecting") can be diagnosed.
//
// A line carries method, path, status, duration, client_id (when known), redirect_uri host,
// User-Agent, Origin and, for every rejection, an explicit reason=. It never contains tokens,
// secrets, authorization codes, cookies, Authorization values or upstream credentials, and query
// strings are only logged at debug level with every value redacted except a small allow list.
package reqlog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/helv-io/skgate/internal/timefmt"
)

// Level is the verbosity.
type Level int

// Levels.
const (
	Info Level = iota
	Debug
)

// ParseLevel maps LOG_LEVEL values to a Level ("debug" is debug, anything else is info).
func ParseLevel(s string) Level {
	if strings.EqualFold(strings.TrimSpace(s), "debug") {
		return Debug
	}
	return Info
}

// Logger writes request log lines.
type Logger struct {
	level Level
	mu    sync.Mutex
	out   io.Writer
}

// New returns a Logger writing to out at the given level.
func New(level Level, out io.Writer) *Logger { return &Logger{level: level, out: out} }

// Level returns the configured level.
func (l *Logger) Level() Level { return l.level }

// Debugging reports whether debug detail is enabled.
func (l *Logger) Debugging() bool { return l != nil && l.level >= Debug }

// SetOutput replaces the destination (tests).
func (l *Logger) SetOutput(w io.Writer) { l.mu.Lock(); l.out = w; l.mu.Unlock() }

// Printf writes a timestamped line (used for debug notes that are not tied to a request).
func (l *Logger) Printf(format string, args ...any) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.out != nil {
		fmt.Fprintf(l.out, "%s %s\n", timefmt.Log(time.Now()), fmt.Sprintf(format, args...))
	}
}

// Event collects what handlers learn about one request.
type Event struct {
	mu           sync.Mutex
	ClientID     string
	RedirectHost string
	Reason       string   // first rejection reason wins
	Notes        []string // non-fatal facts (debug level)
	UpAlias      string
	UpHost       string
	UpStatus     int
	UpAuth       string // effective outbound auth kind used
}

type ctxKey struct{}

// From returns the request's event, or nil when logging is not active (all setters are nil-safe).
func From(r *http.Request) *Event {
	if r == nil {
		return nil
	}
	e, _ := r.Context().Value(ctxKey{}).(*Event)
	return e
}

// Reject records why a request was refused. The first reason wins.
func Reject(r *http.Request, format string, args ...any) {
	if e := From(r); e != nil {
		e.mu.Lock()
		if e.Reason == "" {
			e.Reason = fmt.Sprintf(format, args...)
		}
		e.mu.Unlock()
	}
}

// ClearReason drops a recorded rejection reason (a later credential succeeded).
func ClearReason(r *http.Request) {
	if e := From(r); e != nil {
		e.mu.Lock()
		e.Reason = ""
		e.mu.Unlock()
	}
}

// Note records a non-fatal fact, printed at debug level.
func Note(r *http.Request, format string, args ...any) {
	if e := From(r); e != nil {
		e.mu.Lock()
		e.Notes = append(e.Notes, fmt.Sprintf(format, args...))
		e.mu.Unlock()
	}
}

// Client records the OAuth client_id when it is known.
func Client(r *http.Request, id string) {
	if e := From(r); e != nil && id != "" {
		e.mu.Lock()
		e.ClientID = id
		e.mu.Unlock()
	}
}

// Redirect records the host of a redirect_uri (never the path or query).
func Redirect(r *http.Request, uri string) {
	if e := From(r); e != nil && uri != "" {
		e.mu.Lock()
		e.RedirectHost = HostOf(uri)
		e.mu.Unlock()
	}
}

// Upstream records the MCP upstream involved: alias, host (no credentials, path or query), the
// upstream HTTP status (0 when none was received) and the outbound auth kind in effect.
func Upstream(r *http.Request, alias, rawURL string, status int, auth string) {
	if e := From(r); e != nil {
		e.mu.Lock()
		e.UpAlias, e.UpHost, e.UpAuth = alias, HostOf(rawURL), auth
		if status != 0 {
			e.UpStatus = status
		}
		e.mu.Unlock()
	}
}

// HostOf returns only the host[:port] of a URL ("" when unparsable).
func HostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

// Logged reports whether a path belongs to the endpoints that get request lines.
func Logged(path string) bool {
	switch {
	case path == "/authorize", path == "/token", path == "/register":
		return true
	case strings.HasPrefix(path, "/.well-known/"):
		return true
	case strings.HasPrefix(path, "/admin/oidc/"):
		return true
	case path == "/mcp" || strings.HasPrefix(path, "/mcp/"):
		return true
	case path == "/sse" || strings.HasPrefix(path, "/sse/"), path == "/messages" || strings.HasPrefix(path, "/messages/"):
		return true
	}
	return false
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach Flush and friends (needed for SSE).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Middleware logs requests to the covered endpoints and passes everything else through untouched.
func (l *Logger) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if l == nil || !Logged(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		ev := &Event{}
		sw := &statusWriter{ResponseWriter: w}
		start := time.Now()
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, ev))
		func() {
			defer func() {
				if p := recover(); p != nil {
					ev.mu.Lock()
					if ev.Reason == "" {
						ev.Reason = "handler panic"
					}
					ev.mu.Unlock()
					if sw.status == 0 {
						sw.status = http.StatusInternalServerError
					}
					l.write(r, sw, ev, time.Since(start))
					panic(p)
				}
			}()
			next.ServeHTTP(sw, r)
		}()
		l.write(r, sw, ev, time.Since(start))
	})
}

func (l *Logger) write(r *http.Request, sw *statusWriter, ev *Event, dur time.Duration) {
	status := sw.status
	if status == 0 {
		status = http.StatusOK
	}
	ev.mu.Lock()
	defer ev.mu.Unlock()
	var b strings.Builder
	b.WriteString("req")
	kv := func(k, v string) {
		if v == "" {
			return
		}
		b.WriteByte(' ')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(quote(v))
	}
	kv("method", r.Method)
	kv("path", r.URL.Path)
	b.WriteString(" status=" + strconv.Itoa(status))
	b.WriteString(" dur=" + dur.Round(time.Microsecond).String())
	kv("client_id", ev.ClientID)
	kv("redirect_host", ev.RedirectHost)
	kv("ua", clip(r.Header.Get("User-Agent"), 200))
	kv("origin", clip(r.Header.Get("Origin"), 200))
	if ev.UpAlias != "" || ev.UpHost != "" {
		kv("upstream", ev.UpAlias)
		kv("upstream_host", ev.UpHost)
		if ev.UpStatus != 0 {
			b.WriteString(" upstream_status=" + strconv.Itoa(ev.UpStatus))
		}
		kv("upstream_auth", ev.UpAuth)
	}
	if ev.Reason != "" {
		kv("reason", ev.Reason)
	} else if status >= 400 {
		kv("reason", "rejected with status "+strconv.Itoa(status)+" (no specific reason recorded)")
	}
	if l.level >= Debug {
		kv("query", RedactQuery(r.URL.Query()))
		kv("remote", r.RemoteAddr)
		kv("xff", clip(r.Header.Get("X-Forwarded-For"), 200))
		kv("content_type", clip(r.Header.Get("Content-Type"), 100))
		kv("accept", clip(r.Header.Get("Accept"), 100))
		kv("credentials", CredentialsPresent(r))
		kv("mcp_session", presence(r.Header.Get("Mcp-Session-Id")))
		kv("mcp_protocol", clip(r.Header.Get("Mcp-Protocol-Version"), 40))
		kv("bytes", strconv.FormatInt(sw.bytes, 10))
		if loc := sw.Header().Get("Location"); loc != "" {
			kv("location", redactLocation(loc))
		}
		if len(ev.Notes) > 0 {
			kv("notes", strings.Join(ev.Notes, "; "))
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.out != nil {
		fmt.Fprintf(l.out, "%s %s\n", timefmt.Log(time.Now()), b.String())
	}
}

func presence(s string) string {
	if s == "" {
		return ""
	}
	return "present"
}

// CredentialsPresent describes which credential carriers a request had, never their values.
func CredentialsPresent(r *http.Request) string {
	var p []string
	if v := strings.TrimSpace(r.Header.Get("Authorization")); v != "" {
		scheme := strings.ToLower(strings.SplitN(v, " ", 2)[0])
		switch scheme {
		case "bearer", "basic":
		default:
			scheme = "other"
		}
		p = append(p, "authorization:"+scheme)
	}
	if r.Header.Get("X-API-Key") != "" {
		p = append(p, "x-api-key")
	}
	if r.URL.Query().Get("key") != "" {
		p = append(p, "query-key")
	}
	if len(r.Cookies()) > 0 {
		p = append(p, "cookies")
	}
	return strings.Join(p, ",")
}

// safeQuery lists query parameters whose values are safe to log; every other value is redacted
// (code, state, id_token, access_token, refresh_token, code_verifier, code_challenge, key,
// client_secret, sessionId, next, ...).
var safeQuery = map[string]bool{
	"response_type": true, "client_id": true, "code_challenge_method": true, "scope": true,
	"resource": true, "grant_type": true, "error": true, "response_mode": true, "prompt": true,
}

// RedactQuery renders a query as k=v pairs, keeping only allow-listed values. redirect_uri is
// reduced to its host, and "state" is omitted entirely.
func RedactQuery(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sortStrings(keys)
	var parts []string
	for _, k := range keys {
		lk := strings.ToLower(k)
		switch {
		case lk == "state":
			continue
		case lk == "redirect_uri":
			parts = append(parts, k+"=host:"+HostOf(q.Get(k)))
		case safeQuery[lk]:
			parts = append(parts, k+"="+clip(q.Get(k), 120))
		default:
			parts = append(parts, k+"=[redacted]")
		}
	}
	return strings.Join(parts, "&")
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// redactLocation logs a redirect target as scheme://host/path with a redacted query.
func redactLocation(loc string) string {
	u, err := url.Parse(loc)
	if err != nil {
		return "[unparsable]"
	}
	out := u.Path
	if u.Host != "" {
		out = u.Scheme + "://" + u.Host + u.Path
	}
	if rq := RedactQuery(u.Query()); rq != "" {
		out += "?" + rq
	}
	return out
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// quote makes a value safe for one log line: control characters cannot forge extra lines.
func quote(s string) string {
	plain := s != ""
	for _, r := range s {
		if r <= ' ' || r == '"' || r == '=' || r == '\\' || !unicode.IsPrint(r) {
			plain = false
			break
		}
	}
	if plain {
		return s
	}
	return strconv.Quote(s)
}

// Sanitize strips anything that could carry a secret from an error before it is logged: URLs
// (which may embed credentials or tokens in the query) are replaced by their host.
func Sanitize(err error) string {
	if err == nil {
		return ""
	}
	if ue, ok := err.(*url.Error); ok {
		return ue.Op + " " + HostOf(ue.URL) + ": " + scrubURLs(ue.Err.Error())
	}
	return scrubURLs(err.Error())
}

func scrubURLs(s string) string {
	fields := strings.Fields(s)
	for i, f := range fields {
		if strings.Contains(f, "://") {
			trail := ""
			for len(f) > 0 && strings.ContainsRune(`:,;)"'`, rune(f[len(f)-1])) {
				trail = string(f[len(f)-1]) + trail
				f = f[:len(f)-1]
			}
			fields[i] = HostOf(f) + trail
		}
	}
	return strings.Join(fields, " ")
}

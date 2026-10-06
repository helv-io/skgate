package reqlog

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestLoggedPaths(t *testing.T) {
	for p, want := range map[string]bool{
		"/authorize": true, "/token": true, "/register": true, "/revoke": true, "/.well-known/oauth-authorization-server": true,
		"/.well-known/oauth-protected-resource/mcp/x": true, "/admin/oidc/login": true, "/admin/oidc/callback": true,
		"/mcp": true, "/mcp/alias": true, "/sse": true, "/messages": true,
		"/admin": false, "/admin/keys": false, "/healthz": false, "/v1/models": false, "/": false,
	} {
		if Logged(p) != want {
			t.Errorf("Logged(%q) = %v, want %v", p, !want, want)
		}
	}
}

func TestRedactQuery(t *testing.T) {
	q, _ := url.ParseQuery("code=SECRETCODE&state=STATEVAL&id_token=IDT&access_token=AT&refresh_token=RT&key=sk-abc&client_id=skc-1&redirect_uri=https%3A%2F%2Fapp.example%2Fcb%3Fx%3Dy&code_challenge=CH&code_challenge_method=S256&scope=mcp&sessionId=SID&next=%2Fauthorize%3Fcode%3DZZ")
	got := RedactQuery(q)
	for _, leak := range []string{"SECRETCODE", "STATEVAL", "IDT", "AT&", "RT", "sk-abc", "CH&", "SID", "ZZ", "/cb"} {
		if strings.Contains(got, leak) {
			t.Errorf("redacted query leaks %q: %s", leak, got)
		}
	}
	for _, want := range []string{"client_id=skc-1", "code_challenge_method=S256", "scope=mcp", "redirect_uri=host:app.example", "code=[redacted]", "id_token=[redacted]", "access_token=[redacted]"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "state=") {
		t.Errorf("state must be omitted: %s", got)
	}
}

func run(t *testing.T, l *Logger, h http.HandlerFunc, req *http.Request) string {
	t.Helper()
	var buf bytes.Buffer
	l.SetOutput(&buf)
	rr := httptest.NewRecorder()
	l.Middleware(h).ServeHTTP(rr, req)
	return buf.String()
}

func TestMiddlewareLineFields(t *testing.T) {
	l := New(Info, nil)
	req := httptest.NewRequest("GET", "/authorize?code=SECRETCODE&client_id=skc-9", nil)
	req.Header.Set("User-Agent", "TestAgent/1.0")
	req.Header.Set("Origin", "https://origin.example")
	req.Header.Set("Authorization", "Bearer TOPSECRETTOKEN")
	req.Header.Set("Cookie", "skgate_session=COOKIEVAL")
	out := run(t, l, func(w http.ResponseWriter, r *http.Request) {
		Client(r, "skc-9")
		Redirect(r, "https://app.example/cb?code=LEAK&state=S")
		Reject(r, "bad redirect origin: nope")
		Reject(r, "second reason ignored")
		w.WriteHeader(400)
	}, req)
	for _, want := range []string{"method=GET", "path=/authorize", "status=400", "dur=", "client_id=skc-9", "redirect_host=app.example", "ua=TestAgent/1.0", "origin=https://origin.example", `reason="bad redirect origin: nope"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	for _, leak := range []string{"SECRETCODE", "TOPSECRETTOKEN", "COOKIEVAL", "LEAK", "second reason", "query="} {
		if strings.Contains(out, leak) {
			t.Errorf("info line leaks %q: %s", leak, out)
		}
	}
}

func TestMiddlewareDebugAddsDetailButNeverSecrets(t *testing.T) {
	l := New(Debug, nil)
	req := httptest.NewRequest("POST", "/token?code=SECRETCODE&state=STATEVAL&grant_type=authorization_code", nil)
	req.Header.Set("Authorization", "Bearer TOPSECRETTOKEN")
	req.Header.Set("X-API-Key", "sk-KEYVALUE")
	req.Header.Set("Cookie", "a=COOKIEVAL")
	req.Header.Set("Mcp-Session-Id", "SESSIONVALUE")
	out := run(t, l, func(w http.ResponseWriter, r *http.Request) {
		Note(r, "tokens issued")
		http.Redirect(w, r, "https://app.example/cb?code=LEAKCODE&state=S&iss=x", 302)
	}, req)
	for _, want := range []string{"query=", "grant_type=authorization_code", "code=[redacted]", "credentials=", "authorization:bearer", "x-api-key", "cookies", "mcp_session=present", "notes=", "tokens issued", "location="} {
		if !strings.Contains(out, want) {
			t.Errorf("debug line missing %q: %s", want, out)
		}
	}
	for _, leak := range []string{"SECRETCODE", "STATEVAL", "TOPSECRETTOKEN", "KEYVALUE", "COOKIEVAL", "SESSIONVALUE", "LEAKCODE"} {
		if strings.Contains(out, leak) {
			t.Errorf("debug line leaks %q: %s", leak, out)
		}
	}
}

func TestMiddlewareSkipsOtherPathsAndDefaultReason(t *testing.T) {
	l := New(Info, nil)
	if out := run(t, l, func(w http.ResponseWriter, r *http.Request) {}, httptest.NewRequest("GET", "/admin/keys", nil)); out != "" {
		t.Errorf("non-covered path logged: %q", out)
	}
	out := run(t, l, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }, httptest.NewRequest("GET", "/mcp/x", nil))
	if !strings.Contains(out, "reason=") || !strings.Contains(out, "status=404") {
		t.Errorf("every 4xx needs a reason: %q", out)
	}
}

func TestQuoteBlocksLogInjection(t *testing.T) {
	l := New(Info, nil)
	req := httptest.NewRequest("GET", "/mcp", nil)
	req.Header.Set("User-Agent", "evil\tstatus=200 reason=fine")
	out := run(t, l, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }, req)
	if strings.Count(out, "\n") != 1 || !strings.Contains(out, `ua="evil\tstatus=200 reason=fine"`) {
		t.Errorf("header value must be quoted on one line: %q", out)
	}
}

func TestParseLevelAndSanitize(t *testing.T) {
	if ParseLevel("DEBUG") != Debug || ParseLevel("") != Info || ParseLevel("info") != Info || ParseLevel("bogus") != Info {
		t.Fatal("ParseLevel")
	}
	err := &url.Error{Op: "Post", URL: "https://user:pw@up.example/mcp?token=SECRET", Err: errors.New("dial tcp: connection refused")}
	got := Sanitize(err)
	if strings.Contains(got, "SECRET") || strings.Contains(got, "pw") || !strings.Contains(got, "up.example") || !strings.Contains(got, "connection refused") {
		t.Errorf("Sanitize: %q", got)
	}
	if s := Sanitize(errors.New(`get "https://a.example/x?token=SECRET": boom`)); strings.Contains(s, "SECRET") {
		t.Errorf("Sanitize plain error: %q", s)
	}
}

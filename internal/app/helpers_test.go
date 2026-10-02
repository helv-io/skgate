package app

import (
	"encoding/base64"
	"encoding/json"
	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/oidctest"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func fakeUpstream(t *testing.T) *httptest.Server {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m struct {
			Method string `json:"method"`
			ID     int    `json:"id"`
		}
		_ = json.Unmarshal(b, &m)
		w.Header().Set("Content-Type", "application/json")
		switch m.Method {
		case "initialize":
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"fake","version":"0.1"}}}`)
		case "tools/list":
			io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"list_things","description":"Lists things.\nMore text."}]}}`)
		default:
			w.WriteHeader(202)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func signedIn(t *testing.T, mut func(*config.Config, *oidctest.Provider)) (*App, *httptest.Server, *browser, string) {
	a, ts, idp := newApp(t, mut)
	br := newBrowser(t, ts)
	br.sso(idp, "/admin")
	_, page := br.get("/admin")
	csrf := between(page, `name="csrf" value="`, `"`)
	if csrf == "" {
		t.Fatal("not signed in")
	}
	return a, ts, br, csrf
}

// flashOf decodes the one-shot flash cookie set on a response: kind ("ok"/"bad") and message.
// Empty strings mean no flash was queued.
func flashOf(r *http.Response) (kind, msg string) {
	for _, c := range r.Cookies() {
		if c.Name != "skgate_flash" || c.MaxAge < 0 || c.Value == "" {
			continue
		}
		p, _, _ := strings.Cut(c.Value, ".")
		raw, _ := base64.RawURLEncoding.DecodeString(p)
		var t struct {
			K string `json:"k"`
			M string `json:"m"`
		}
		_ = json.Unmarshal(raw, &t)
		return t.K, t.M
	}
	return "", ""
}

func flashKind(r *http.Response) string { k, _ := flashOf(r); return k }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

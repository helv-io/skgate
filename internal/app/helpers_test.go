package app

import (
	"encoding/base64"
	"fmt"
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
			Method string          `json:"method"`
			ID     json.RawMessage `json:"id"`
			Params map[string]any  `json:"params"`
		}
		_ = json.Unmarshal(b, &m)
		id := m.ID
		if len(id) == 0 {
			id = json.RawMessage("1")
		}
		reply := func(result any) {
			j, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
			w.Header().Set("Content-Type", "application/json")
			w.Write(j)
		}
		switch m.Method {
		case "initialize":
			reply(map[string]any{"protocolVersion": "2025-06-18", "serverInfo": map[string]any{"name": "fake", "version": "0.1"}, "capabilities": map[string]any{}})
		case "notifications/initialized":
			w.WriteHeader(202)
		case "tools/list":
			reply(map[string]any{"tools": []any{map[string]any{
				"name": "list_things", "description": "Lists things.\nMore text.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string", "description": "A filter."}}, "required": []any{"q"}},
			}}})
		case "tools/call":
			name, _ := m.Params["name"].(string)
			args, _ := m.Params["arguments"].(map[string]any)
			reply(map[string]any{"content": []any{map[string]any{"type": "text", "text": "called " + name + " q=" + fmt.Sprint(args["q"])}}, "isError": false})
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

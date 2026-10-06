package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/provider/grok"
)

// The actions that used to load a page save in place too: Create key, Regenerate and Create client show their
// secret once in a dialog over the page; Save on the OpenAPI tools page draws the tool list again in place; Update
// reviews in a dialog and applies in place; Import shows its outcome in a dialog; the Grok sign-in Cancel and the
// sign-in's completion update the card. A browser checks there was no navigation, the scroll stayed, the server has
// the new state, a secret is on the page only while its dialog is open, and Save keeps open groups and the
// unsaved-changes guard, at phone and desktop widths.
func TestResultsShowOnceInADialogInBrowser(t *testing.T) {
	up, _ := modelsUpstream(t, "m")
	a, _, br, csrf, _ := signedInProvider(t, up)

	// an issuer whose device sign-in stays pending until the script grants it
	var granted atomic.Bool
	var iss *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": iss.URL + "/oauth2/authorize", "device_authorization_endpoint": iss.URL + "/oauth2/device/code",
			"token_endpoint": iss.URL + "/oauth2/token", "userinfo_endpoint": iss.URL + "/oauth2/userinfo"})
	})
	mux.HandleFunc("/oauth2/device/code", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"device_code": "DEV", "user_code": "ABCD-EFGH", "verification_uri": iss.URL + "/verify", "expires_in": 600, "interval": 1})
	})
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		if !granted.Load() {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "acc-new-1234", "refresh_token": "ref-new-5678", "expires_in": 3600, "token_type": "Bearer"})
	})
	mux.HandleFunc("/oauth2/userinfo", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"email": "person@example.com"})
	})
	mux.HandleFunc("/grant", func(w http.ResponseWriter, r *http.Request) { granted.Store(true) })
	mux.HandleFunc("/verify", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<!doctype html><title>verify</title>") })
	iss = httptest.NewServer(mux)
	t.Cleanup(iss.Close)
	g := a.Providers.Default().(*grok.Client)
	t.Cleanup(g.CancelDevice)
	g.SignOut()
	g.Issuer = iss.URL

	// a long OpenAPI description whose next read differs each time the script flips it
	var mu sync.Mutex
	v2 := false
	spec := func(host string, two bool) string {
		var p []string
		for i := 0; i < 30; i++ {
			if two && i == 29 {
				continue
			}
			p = append(p, fmt.Sprintf(`"/items%d":{"get":{"operationId":"getItems%d","summary":"Read items %d"}}`, i, i, i))
		}
		p = append(p, `"/notes":{"post":{"operationId":"addNote","summary":"Add a note","requestBody":{"content":{"application/json":{"schema":{"type":"object"}}}}}}`)
		if two {
			p = append(p, `"/extra":{"get":{"operationId":"getExtra"}}`, `"/extra2":{"get":{"operationId":"getExtra2"}}`)
		}
		return `{"openapi":"3.0.0","info":{"title":"Items API","version":"1"},"servers":[{"url":"` + host + `/v1"}],"paths":{` + strings.Join(p, ",") + `}}`
	}
	var sh *httptest.Server
	sh = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/spec.json":
			fmt.Fprint(w, spec(sh.URL, v2))
		case "/flip":
			v2 = !v2
		default:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{}`)
		}
	}))
	t.Cleanup(sh.Close)
	if res, _ := br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"big"}, "enabled": {"1"}, "oa_spec_url": {sh.URL + "/spec.json"}}); flashKind(res) == "bad" {
		_, m := flashOf(res)
		t.Fatal("add the OpenAPI upstream: " + m)
	}

	for i := 0; i < 18; i++ {
		br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {fmt.Sprintf("key %02d", i)}})
	}
	now := time.Now()
	for i := 0; i < 18; i++ {
		seedClient(t, a, fmt.Sprintf("c%02d", i), "admin", now.Add(-time.Hour), now, "https://c.example/cb")
	}
	runBrowserScript(t, "results.js", br.ts.URL, br, iss.URL, sh.URL)
}

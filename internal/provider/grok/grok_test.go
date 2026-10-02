package grok

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/store"
	"github.com/helv-io/skgate/internal/vkeys"
)

func setup(t *testing.T) (*Client, *vkeys.Manager, *config.Config) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := &config.Config{PublicURL: "http://skgate.test"}
	cfg.Bind(db)
	c := New(cfg, db)
	c.ClientID, c.Scopes = "cid", "openid offline_access"
	return c, vkeys.New(db), cfg
}

// fakeIssuer serves discovery, device, and token endpoints.
func fakeIssuer(t *testing.T, tokenHandler http.HandlerFunc) *httptest.Server {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": srv.URL + "/oauth2/authorize", "device_authorization_endpoint": srv.URL + "/oauth2/device/code",
			"token_endpoint": srv.URL + "/oauth2/token", "userinfo_endpoint": srv.URL + "/oauth2/userinfo"})
	})
	mux.HandleFunc("/oauth2/device/code", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.PostFormValue("client_id") != "cid" || r.PostFormValue("scope") == "" {
			w.WriteHeader(400)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"device_code": "DEV", "user_code": "ABCD-1234", "verification_uri": "https://auth.example/device", "expires_in": 60, "interval": 1})
	})
	mux.HandleFunc("/oauth2/userinfo", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"email": "me@example.com"})
	})
	mux.HandleFunc("/oauth2/token", tokenHandler)
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestDeviceFlowAndRefresh(t *testing.T) {
	var polls, refreshes int32
	iss := fakeIssuer(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		switch r.PostFormValue("grant_type") {
		case deviceGrant:
			if r.PostFormValue("device_code") != "DEV" {
				w.WriteHeader(400)
				return
			}
			if atomic.AddInt32(&polls, 1) == 1 {
				w.WriteHeader(400)
				json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "acc-1", "refresh_token": "ref-1", "expires_in": 3600})
		case "refresh_token":
			n := atomic.AddInt32(&refreshes, 1)
			if r.PostFormValue("refresh_token") != "ref-1" {
				w.WriteHeader(400)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
				return
			}
			_ = n
			json.NewEncoder(w).Encode(map[string]any{"access_token": "acc-2", "refresh_token": "ref-2", "expires_in": 3600})
		}
	})
	c, _, _ := setup(t)
	c.Issuer = iss.URL
	d, err := c.StartDevice(context.Background())
	if err != nil || d.UserCode != "ABCD-1234" || d.VerificationURI != "https://auth.example/device" {
		t.Fatalf("start: %v %+v", err, d)
	}
	deadline := time.Now().Add(10 * time.Second)
	for c.Device().State == "pending" && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if c.Device().State != "done" {
		t.Fatalf("device flow state %q err %q", c.Device().State, c.Device().Err)
	}
	st := c.Status()
	if !st.SignedIn || st.ExpiresIn < 50*time.Minute || st.Account != "me@example.com" {
		t.Fatalf("status %+v", st)
	}
	tok, err := c.Token(context.Background())
	if err != nil || tok != "acc-1" {
		t.Fatalf("token %q %v", tok, err)
	}
	// near expiry -> refresh on demand, refresh token rotates
	c.SetTokens("acc-1", "ref-1", time.Now().Add(30*time.Second))
	tok, err = c.Token(context.Background())
	if err != nil || tok != "acc-2" {
		t.Fatalf("on-demand refresh: %q %v", tok, err)
	}
	if v, _ := c.DB.GetSetting("provider.grok.refresh"); v != "ref-2" {
		t.Fatal("refresh token not rotated")
	}
	// invalid_grant clears tokens and asks for re-login
	c.SetTokens("acc-x", "bad", time.Now().Add(-time.Minute))
	if _, err := c.Token(context.Background()); err != ErrReauth {
		t.Fatalf("want ErrReauth, got %v", err)
	}
	if c.Status().SignedIn || c.Status().State != "reauth" {
		t.Fatal("tokens should be cleared with state reauth")
	}
}

// The CLI headers go to grok.com hosts only.
func TestHeadersOnlyForGrokHosts(t *testing.T) {
	c, _, _ := setup(t)
	if c.Headers("https://cli-chat-proxy.grok.com/v1")["x-xai-token-auth"] != "xai-grok-cli" {
		t.Fatal("grok.com hosts need the CLI headers")
	}
	for _, b := range []string{"https://api.x.ai/v1", "https://notgrok.com.evil.example/v1", "http://127.0.0.1:1/v1"} {
		if h := c.Headers(b); len(h) != 0 {
			t.Fatalf("%s got headers %v", b, h)
		}
	}
}

// Token settings of releases before 0.5.0 move to the provider namespace when the client is created.
func TestLegacyTokenKeysMigrate(t *testing.T) {
	db, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	defer db.Close()
	db.SetSetting("xai_access", "a")
	db.SetSetting("xai_refresh", "r")
	New(&config.Config{}, db)
	if v, _ := db.GetSetting("provider.grok.refresh"); v != "r" {
		t.Fatal("refresh token not migrated")
	}
	if _, ok := db.GetSetting("xai_refresh"); ok {
		t.Fatal("old key left behind")
	}
}

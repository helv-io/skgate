package mcp

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// ChatGPT-shaped CIMD: private_key_jwt + JWKS; /token accepts a signed client_assertion.
func TestCIMDPrivateKeyJWT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const kid = "cimd-test"
	jwk := jose.JSONWebKey{Key: &key.PublicKey, KeyID: kid, Algorithm: string(jose.RS256), Use: "sig"}
	jwksBody, _ := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{jwk}})

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=3600")
		w.Write(jwksBody)
	})
	docSrv := httptest.NewTLSServer(mux)
	t.Cleanup(docSrv.Close)
	jwksURL := docSrv.URL + "/oauth/jwks.json"

	e := newEnv(t, nil)
	e.srv.cimd.client = docSrv.Client() // trusts the test TLS cert; still uses our dial rules when unset

	httpsID := "https://chatgpt.example/oauth/client.json"
	c := Client{
		ID: httpsID, Name: "ChatGPT", RedirectURIs: []string{hostedRedirect},
		AuthMethod: "private_key_jwt", JWKSURI: jwksURL, Source: cimdSource,
	}
	if err := e.srv.Clients.PutMetadataClient(c); err != nil {
		t.Fatal(err)
	}
	got, ok := e.srv.Clients.Get(httpsID)
	if !ok || got.AuthMethod != "private_key_jwt" || got.JWKSURI != jwksURL {
		t.Fatalf("stored client: ok=%v %+v", ok, got)
	}
	e.srv.cimd.mu.Lock()
	e.srv.cimd.fresh = map[string]cimdState{httpsID: {until: time.Now().Add(time.Hour)}}
	e.srv.cimd.mu.Unlock()

	if r := e.do("POST", "/token", formHdr, form("grant_type", "authorization_code", "client_id", httpsID, "code", "x", "code_verifier", strings.Repeat("a", 43), "redirect_uri", hostedRedirect)); r.StatusCode != 401 {
		t.Fatalf("no assertion: %d %v", r.StatusCode, readJSON(t, r))
	}
	badKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	bad := mustClientAssertion(t, badKey, kid, httpsID, e.srv.tokenEndpoint())
	if r := e.do("POST", "/token", formHdr, form(
		"grant_type", "authorization_code", "client_id", httpsID,
		"client_assertion_type", clientAssertionType, "client_assertion", bad,
		"code", "x", "code_verifier", strings.Repeat("a", 43), "redirect_uri", hostedRedirect,
	)); r.StatusCode != 401 {
		t.Fatalf("bad sig: %d", r.StatusCode)
	}

	verifier, challenge := pkce()
	code, _, st := e.authorizeCode(httpsID, hostedRedirect, challenge, "")
	if st != 302 || code == "" {
		t.Fatalf("authorize: status=%d code=%q", st, code)
	}
	assertion := mustClientAssertion(t, key, kid, httpsID, e.srv.tokenEndpoint())
	tok := e.do("POST", "/token", formHdr, form(
		"grant_type", "authorization_code", "client_id", httpsID,
		"client_assertion_type", clientAssertionType, "client_assertion", assertion,
		"code", code, "code_verifier", verifier, "redirect_uri", hostedRedirect,
	))
	if tok.StatusCode != 200 {
		t.Fatalf("token: %d %v logs=%s", tok.StatusCode, readJSON(t, tok), e.logs.String())
	}
	body := readJSON(t, tok)
	if body["access_token"] == nil || body["refresh_token"] == nil {
		t.Fatalf("tokens %v", body)
	}

	assertion2 := mustClientAssertion(t, key, kid, httpsID, e.srv.tokenEndpoint())
	ref := e.do("POST", "/token", formHdr, form(
		"grant_type", "refresh_token", "client_id", httpsID,
		"client_assertion_type", clientAssertionType, "client_assertion", assertion2,
		"refresh_token", body["refresh_token"].(string),
	))
	if ref.StatusCode != 200 {
		t.Fatalf("refresh: %d %v", ref.StatusCode, readJSON(t, ref))
	}

	noneID := "https://app.example/client.json"
	if err := e.srv.Clients.PutMetadataClient(Client{
		ID: noneID, Name: "App", RedirectURIs: []string{hostedRedirect}, AuthMethod: "none", Source: cimdSource,
	}); err != nil {
		t.Fatal(err)
	}
	e.srv.cimd.mu.Lock()
	e.srv.cimd.fresh[noneID] = cimdState{until: time.Now().Add(time.Hour)}
	e.srv.cimd.mu.Unlock()
	verifier, challenge = pkce()
	code, _, st = e.authorizeCode(noneID, hostedRedirect, challenge, "")
	if st != 302 || code == "" {
		t.Fatalf("none authorize: %d", st)
	}
	tok = e.do("POST", "/token", formHdr, form(
		"grant_type", "authorization_code", "client_id", noneID,
		"code", code, "code_verifier", verifier, "redirect_uri", hostedRedirect,
	))
	if tok.StatusCode != 200 {
		t.Fatalf("none path: %d %v", tok.StatusCode, readJSON(t, tok))
	}
}

func TestParseChatGPTMetadata(t *testing.T) {
	const id = "https://chatgpt.com/oauth/client.json"
	u, why := ParseMetadataClientID(id)
	if why != "" {
		t.Fatal(why)
	}
	doc := `{"client_id":"https://chatgpt.com/oauth/client.json","client_uri":"https://chatgpt.com/","redirect_uris":["https://chatgpt.com/connector_platform_oauth_redirect"],"token_endpoint_auth_method":"private_key_jwt","token_endpoint_auth_methods_supported":["none","private_key_jwt"],"grant_types":["authorization_code","refresh_token"],"response_types":["code"],"client_name":"ChatGPT","logo_uri":"https://persistent.oaistatic.com/sonic/misc/openai-logo.png","token_endpoint_auth_signing_alg":"RS256","jwks_uri":"https://chatgpt.com/oauth/jwks.json"}`
	c, err := parseMetadata(id, u, []byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if c.AuthMethod != "private_key_jwt" || c.JWKSURI != "https://chatgpt.com/oauth/jwks.json" || c.Name != "ChatGPT" {
		t.Fatalf("%+v", c)
	}
	if len(c.RedirectURIs) != 1 || c.RedirectURIs[0] != "https://chatgpt.com/connector_platform_oauth_redirect" {
		t.Fatalf("redirects %v", c.RedirectURIs)
	}
}

func mustClientAssertion(t *testing.T, key *rsa.PrivateKey, kid, clientID, tokenURL string) string {
	t.Helper()
	opts := (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", kid)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, opts)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cl := jwt.Claims{
		Issuer:   clientID,
		Subject:  clientID,
		Audience: jwt.Audience{tokenURL},
		IssuedAt: jwt.NewNumericDate(now),
		Expiry:   jwt.NewNumericDate(now.Add(2 * time.Minute)),
	}
	raw, err := jwt.Signed(signer).Claims(cl).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

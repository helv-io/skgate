package openapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const keyParamSpec = `{"openapi":"3.0.0","info":{"title":"k"},"paths":{"/a":{"get":{"operationId":"a","parameters":[
 {"name":"X-API-Key","in":"header","required":true,"schema":{"type":"string"}},
 {"name":"api_key","in":"query","required":true,"schema":{"type":"string"}},
 {"name":"q","in":"query","schema":{"type":"string"}}]}}}}`

func TestCredentialParametersAreNotOfferedAndCallsWork(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r; w.Write([]byte("ok")) }))
	defer srv.Close()
	d := mustParse(t, keyParamSpec)
	tl := toolFor(t, d, "GET /a")
	for _, a := range []Auth{{Kind: AuthHeader, Name: "x-api-key", Value: "H"}, {Kind: AuthQuery, Name: "api_key", Value: "Q"}} {
		t2 := tl.WithoutCredential(a)
		b, _ := json.Marshal(t2.InputSchema)
		if strings.Contains(string(b), "X-API-Key") && a.Kind == AuthHeader || strings.Contains(string(b), "api_key") && a.Kind == AuthQuery {
			t.Errorf("%s still offers the credential: %s", a.Kind, b)
		}
		if !strings.Contains(string(b), `"q"`) {
			t.Errorf("other parameters lost: %s", b)
		}
	}
	// The other credential's parameter is still the model's to fill, and still required.
	b, _ := json.Marshal(tl.WithoutCredential(Auth{Kind: AuthHeader, Name: "X-API-Key", Value: "H"}).InputSchema)
	if !strings.Contains(string(b), "api_key") {
		t.Errorf("query parameter dropped for header auth: %s", b)
	}
	c := caller(srv, Auth{Kind: AuthHeader, Name: "X-API-Key", Value: "H"})
	res := c.Call(context.Background(), tl.WithoutCredential(c.Auth), map[string]any{"api_key": "mine"})
	if res.IsError || got.Header.Get("X-API-Key") != "H" {
		t.Errorf("%+v %v", res, got)
	}
	c = caller(srv, Auth{Kind: AuthQuery, Name: "api_key", Value: "Q"})
	res = c.Call(context.Background(), tl.WithoutCredential(c.Auth), map[string]any{"X-API-Key": "mine"})
	if res.IsError || got.URL.Query().Get("api_key") != "Q" {
		t.Errorf("%+v %v", res, got.URL)
	}
}

func TestRedirectsNeverLeaveTheSiteOrDowngrade(t *testing.T) {
	mk := func(raw string) *http.Request { u, _ := url.Parse(raw); return &http.Request{URL: u} }
	via := []*http.Request{mk("https://api.example.com/a")}
	if sameSite(mk("https://api.example.com/b"), via) != nil {
		t.Error("a same-site https redirect was refused")
	}
	for _, to := range []string{"http://api.example.com/b", "https://other.example.com/b"} {
		if sameSite(mk(to), via) != http.ErrUseLastResponse {
			t.Errorf("followed a redirect to %s", to)
		}
	}
	if sameSite(mk("https://api.example.com:8443/b"), via) != http.ErrUseLastResponse {
		t.Error("followed a redirect to another port")
	}
	if err := fetchClient.CheckRedirect(mk("http://spec.example.com/o"), []*http.Request{mk("https://spec.example.com/o")}); err == nil {
		t.Error("the description fetcher followed https to http")
	}
	if err := fetchClient.CheckRedirect(mk("https://cdn.example.com/o"), []*http.Request{mk("https://spec.example.com/o")}); err != nil {
		t.Errorf("a redirect to another https host was refused: %v", err)
	}
}

func TestFetchRefusesUserinfo(t *testing.T) {
	if _, err := Fetch(context.Background(), "https://tok:x@example.com/o.json"); err == nil {
		t.Error("accepted a user name in the address")
	}
}

func TestBaseQueryIsKept(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r; w.Write([]byte("ok")) }))
	defer srv.Close()
	d := mustParse(t, petJSON)
	c := &Caller{Base: srv.URL + "/v1?api-version=2024-01-01&limit=1", Client: srv.Client(), Auth: Auth{Kind: AuthQuery, Name: "k", Value: "s"}}
	c.Call(context.Background(), toolFor(t, d, "GET /pets"), map[string]any{"limit": 7})
	q := got.URL.Query()
	if q.Get("api-version") != "2024-01-01" || q.Get("limit") != "7" || q.Get("k") != "s" {
		t.Errorf("query %v", q)
	}
}

func TestLongNonUTF8BodyIsNotCutAtTheFirstBadByte(t *testing.T) {
	body := strings.Repeat("a", 100) + "\xe9" + strings.Repeat("b", MaxResultBytes)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(body))
	}))
	defer srv.Close()
	res := caller(srv, Auth{}).Call(context.Background(), toolFor(t, mustParse(t, petJSON), "GET /pets"), nil)
	if strings.Count(res.Text, "b") < MaxResultBytes-110 {
		t.Errorf("only %d of %d bytes shown", strings.Count(res.Text, "b"), MaxResultBytes)
	}
	// A multi-byte character split by the cut goes whole, never as a stray byte.
	body = strings.Repeat("a", MaxResultBytes-1) + "é"
	res = caller(srv, Auth{}).Call(context.Background(), toolFor(t, mustParse(t, petJSON), "GET /pets"), nil)
	if strings.Contains(res.Text, "\ufffd") || strings.Contains(res.Text, "?\n[truncated") {
		t.Errorf("a split character leaked: %q", res.Text[len(res.Text)-80:])
	}
}

func TestToolNamesDoNotChangeWhenAnotherToolIsSwitchedOff(t *testing.T) {
	d := mustParse(t, `{"openapi":"3.0.0","info":{"title":"n"},"paths":{"/a":{"get":{"operationId":"list"}},"/b":{"get":{"operationId":"list"}}}}`)
	both := Tools(d.Operations(), Selection{Enabled: map[string]bool{"GET /a": true, "GET /b": true}})
	only := Tools(d.Operations(), Selection{Enabled: map[string]bool{"GET /b": true}})
	if len(both) != 2 || len(only) != 1 || both[1].Name != only[0].Name {
		t.Errorf("names %q %q vs %q", both[0].Name, both[1].Name, only[0].Name)
	}
}

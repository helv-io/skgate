package mcp

import (
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestHealthFollowsCallsToRemoteUpstreams(t *testing.T) {
	e := newEnv(t, nil)
	f := newAuthMCP(t, "none", false)
	e.srv.Upstreams.Create(Upstream{Alias: "up", URL: f.URL, AuthKind: AuthNone, Enabled: true})
	if _, ok := e.srv.HealthOf("up"); ok {
		t.Fatal("nothing has been called yet")
	}
	if res := e.srv.Test(t.Context(), "up"); !res.OK {
		t.Fatalf("%+v", res)
	}
	h, ok := e.srv.HealthOf("up")
	if !ok || !h.OK || h.LastOK.IsZero() || h.LastErr != "" {
		t.Fatalf("after a good call: %+v", h)
	}

	// the same alias now points at nothing: the error is short and has no address
	addr := deadAddr(t)
	u, _ := e.srv.Upstreams.Get("up")
	u.URL = "http://" + addr + "/mcp"
	e.srv.Upstreams.Update(u, true)
	e.srv.Test(t.Context(), "up")
	h, _ = e.srv.HealthOf("up")
	host, port, _ := net.SplitHostPort(addr)
	if h.OK || h.LastErr != "connection refused" || strings.Contains(h.LastErr, host) || strings.Contains(h.LastErr, port) || h.LastOK.IsZero() || h.LastErrAt.IsZero() {
		t.Fatalf("after a refused call: %+v", h)
	}

	// credentials rejected, and a server error
	deny := newAuthMCP(t, "bearer:right", false)
	e.srv.Upstreams.Create(Upstream{Alias: "denied", URL: deny.URL, AuthKind: AuthBearer, AuthValue: "wrong", Enabled: true})
	e.srv.Test(t.Context(), "denied")
	if h, _ := e.srv.HealthOf("denied"); h.OK || !strings.Contains(h.LastErr, "HTTP 401") || strings.Contains(h.LastErr, "wrong") {
		t.Errorf("401: %+v", h)
	}
	boom := newAuthMCP(t, "none", false)
	boom.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) })
	e.srv.Upstreams.Create(Upstream{Alias: "boom", URL: boom.URL, AuthKind: AuthNone, Enabled: true})
	e.srv.Test(t.Context(), "boom")
	if h, _ := e.srv.HealthOf("boom"); h.OK || h.LastErr != "HTTP 502" {
		t.Errorf("502: %+v", h)
	}

	// managed upstreams have no health of this kind; a forgotten alias starts over
	e.srv.noteCall(t.Context(), Upstream{Alias: "m", Kind: KindStdio, Command: "x"}, &http.Response{StatusCode: 200}, nil)
	if _, ok := e.srv.HealthOf("m"); ok {
		t.Error("a managed upstream must not get a health record")
	}
	e.srv.ForgetHealth("up")
	if _, ok := e.srv.HealthOf("up"); ok {
		t.Error("forgotten")
	}
}

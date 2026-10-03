package mcp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func deadAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestNetFailNamesTheKindAndNoAddress(t *testing.T) {
	addr := deadAddr(t)
	ctx := context.Background()
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/mcp", nil)
	_, err := http.DefaultClient.Do(req)
	e := upstreamErr(ctx, err, false)
	nf, ok := AsNetFail(e)
	if !ok || nf.Kind != NetRefused {
		t.Fatalf("want refused, got %v (%T)", e, e)
	}
	host, port, _ := net.SplitHostPort(addr)
	if strings.Contains(e.Error(), host) || strings.Contains(e.Error(), port) || e.Error() != "upstream unreachable: connection refused" {
		t.Errorf("the public text must carry no address: %q", e.Error())
	}
	if !strings.Contains(nf.Detail, port) {
		t.Errorf("the detail is for the admin screen and keeps the address: %q", nf.Detail)
	}

	req, _ = http.NewRequestWithContext(ctx, "POST", "http://no-such-host.invalid/mcp", nil)
	_, err = http.DefaultClient.Do(req)
	if nf, ok := AsNetFail(upstreamErr(ctx, err, false)); !ok || nf.Kind != NetDNS || strings.Contains(nf.Error(), "invalid") {
		t.Errorf("dns: %+v", nf)
	}

	tctx, cancel := context.WithTimeout(ctx, time.Millisecond)
	defer cancel()
	<-tctx.Done()
	if nf, ok := AsNetFail(upstreamErr(tctx, context.DeadlineExceeded, true)); !ok || nf.Kind != NetTimeout || nf.Error() != "reading upstream response: timed out" {
		t.Errorf("timeout: %+v", nf)
	}

	// an error skgate wrote itself keeps its text
	own := upstreamErr(ctx, errors.New("upstream redirected to another host, which skgate does not follow"), false)
	if _, ok := AsNetFail(own); ok || !strings.Contains(own.Error(), "another host") {
		t.Errorf("own error: %v", own)
	}
}

func TestTestSuggestsThePortThatAnswersMCP(t *testing.T) {
	e := newEnv(t, nil)
	f := newAuthMCP(t, "none", false)
	u, _ := url.Parse(f.URL)
	realPort, _ := strconv.Atoi(u.Port())
	old := candidatePorts
	candidatePorts = []int{realPort}
	t.Cleanup(func() { candidatePorts = old })

	_, deadPort, _ := net.SplitHostPort(deadAddr(t))
	e.srv.Upstreams.Create(Upstream{Alias: "wrongport", URL: "http://127.0.0.1:" + deadPort + u.Path, AuthKind: AuthNone, Enabled: true})
	res := e.srv.Test(t.Context(), "wrongport")
	if res.OK || res.Error != "upstream unreachable: connection refused" {
		t.Fatalf("result: %+v", res)
	}
	if strings.Contains(res.Error, "127.0.0.1") || strings.Contains(res.Error, deadPort) {
		t.Errorf("the error carries the address: %q", res.Error)
	}
	if !strings.Contains(res.Detail, deadPort) {
		t.Errorf("the admin detail should keep the address: %q", res.Detail)
	}
	if res.Diag == nil || res.Diag.Suggested() != realPort {
		t.Fatalf("diagnosis: %+v", res.Diag)
	}
	for _, want := range []string{"Nothing is listening on port " + deadPort, strconv.Itoa(realPort), "answers MCP", "localhost inside skgate's container"} {
		if !strings.Contains(res.Hint, want) {
			t.Errorf("hint lacks %q: %s", want, res.Hint)
		}
	}
	if short := res.Diag.Hint(false); strings.Contains(short, "127.0.0.1") {
		t.Errorf("the short hint names the host: %s", short)
	}

	// a name that does not resolve
	e.srv.Upstreams.Create(Upstream{Alias: "noname", URL: "http://no-such-container.invalid:3000/mcp", AuthKind: AuthNone, Enabled: true})
	res = e.srv.Test(t.Context(), "noname")
	if res.OK || res.Diag == nil || res.Diag.Kind != NetDNS || !strings.Contains(res.Hint, "share a Docker network") || strings.Contains(res.Error, "invalid") {
		t.Errorf("dns: %+v", res)
	}
}

func TestPortsAreNotLookedForOnPublicHosts(t *testing.T) {
	if privateHost(t.Context(), "8.8.8.8") || privateHost(t.Context(), "") {
		t.Error("a public address is not private")
	}
	for _, h := range []string{"10.1.2.3", "172.18.0.5", "192.168.1.9", "127.0.0.1", "localhost", "mcp-thing", "::1"} {
		if !privateHost(t.Context(), h) {
			t.Errorf("%s should count as private", h)
		}
	}
}

func TestCheckReachable(t *testing.T) {
	e := newEnv(t, nil)
	f := newAuthMCP(t, "none", false)
	if d := e.srv.CheckReachable(t.Context(), Upstream{URL: f.URL}); d != nil {
		t.Errorf("reachable upstream: %+v", d)
	}
	if d := e.srv.CheckReachable(t.Context(), Upstream{URL: "http://" + deadAddr(t) + "/mcp"}); d == nil || d.Kind != NetRefused {
		t.Errorf("dead upstream: %+v", d)
	}
	if d := e.srv.CheckReachable(t.Context(), Upstream{Kind: KindStdio, URL: ""}); d != nil {
		t.Errorf("managed: %+v", d)
	}
}

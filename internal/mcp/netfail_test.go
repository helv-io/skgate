package mcp

import (
	"context"
	"errors"
	"net"
	"net/http"
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

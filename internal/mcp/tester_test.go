package mcp

import (
	"fmt"
	"strings"
	"testing"
)

func TestTestActionJSONAndSSE(t *testing.T) {
	for _, sse := range []bool{false, true} {
		t.Run(fmt.Sprint("sse=", sse), func(t *testing.T) {
			e := newEnv(t, nil)
			f := newAuthMCP(t, "header:HV", sse)
			e.srv.Upstreams.Create(Upstream{Alias: "u", URL: f.URL, AuthKind: AuthAuto, AuthName: "X-Api-Key", AuthValue: "HV", Enabled: true})
			res := e.srv.Test(t.Context(), "u")
			if !res.OK || res.Error != "" {
				t.Fatalf("test failed: %+v", res)
			}
			if res.Auth != "header" || res.Detected != "header" || res.Status != 200 || res.Latency <= 0 {
				t.Errorf("result: %+v", res)
			}
			if !res.SessionID || res.Server != "fake 1.2" || res.Protocol != "2025-06-18" {
				t.Errorf("server info: %+v", res)
			}
			if len(res.Tools) != 2 || res.Tools[0].Name != "alpha" || res.Tools[0].Desc != "Does alpha." || res.Tools[1].Desc != "" {
				t.Errorf("tools: %+v", res.Tools)
			}
			log := f.callLog()
			for _, want := range []string{"POST initialize auth=header:HV", "POST notifications/initialized auth=header:HV", "POST tools/list auth=header:HV", "DELETE"} {
				if !strings.Contains(log, want) {
					t.Errorf("missing call %q:\n%s", want, log)
				}
			}
		})
	}
}

func TestTestActionErrorsAndCap(t *testing.T) {
	e := newEnv(t, nil)
	// rejected credentials
	f := newAuthMCP(t, "bearer:RIGHT", false)
	e.srv.Upstreams.Create(Upstream{Alias: "bad", URL: f.URL, AuthKind: AuthBearer, AuthValue: "WRONG", Enabled: true})
	res := e.srv.Test(t.Context(), "bad")
	if res.OK || res.Status != 401 || !strings.Contains(res.Error, "HTTP 401") || strings.Contains(res.Error, "WRONG") {
		t.Errorf("401: %+v", res)
	}
	// oauth upstream
	fo := newAuthMCP(t, "oauth", false)
	e.srv.Upstreams.Create(Upstream{Alias: "oa", URL: fo.URL, AuthKind: AuthAuto, Enabled: true})
	res = e.srv.Test(t.Context(), "oa")
	if res.OK || !strings.Contains(res.Error, "advertises OAuth") || res.Detected != DetectedOAuth {
		t.Errorf("oauth: %+v", res)
	}
	// unreachable
	e.srv.Upstreams.Create(Upstream{Alias: "dead", URL: "http://127.0.0.1:1/mcp", AuthKind: AuthNone, Enabled: true})
	res = e.srv.Test(t.Context(), "dead")
	if res.OK || !strings.Contains(res.Error, "unreachable") || res.Status != 0 {
		t.Errorf("unreachable: %+v", res)
	}
	// unknown alias
	if res = e.srv.Test(t.Context(), "nope"); res.OK || res.Error == "" {
		t.Errorf("unknown: %+v", res)
	}
	// tool list cap
	fc := newAuthMCP(t, "none", false)
	var tools []string
	for i := 0; i < maxTestTools+25; i++ {
		tools = append(tools, fmt.Sprintf(`{"name":"t%d","description":"d"}`, i))
	}
	fc.tools = "[" + strings.Join(tools, ",") + "]"
	e.srv.Upstreams.Create(Upstream{Alias: "many", URL: fc.URL, AuthKind: AuthNone, Enabled: true})
	res = e.srv.Test(t.Context(), "many")
	if !res.OK || len(res.Tools) != maxTestTools || res.ToolTotal != maxTestTools+25 || !res.ToolsCapped {
		t.Errorf("cap: ok=%v n=%d total=%d capped=%v", res.OK, len(res.Tools), res.ToolTotal, res.ToolsCapped)
	}
}

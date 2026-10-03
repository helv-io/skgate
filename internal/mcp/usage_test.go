package mcp

import (
	"strings"
	"testing"
)

// Authenticated MCP requests count per key (by Bearer or X-API-Key); failed auth and OAuth tokens do not.
func TestMCPRequestsCountedPerKey(t *testing.T) {
	e := newEnv(t, nil)
	key, k, _ := e.keys.Create("t")
	body := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	e.do("POST", "/mcp", map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json"}, body).Body.Close()
	e.do("POST", "/mcp", map[string]string{"X-API-Key": key, "Content-Type": "application/json"}, body).Body.Close()
	last := "x"
	if strings.HasSuffix(key, last) { // the forged key must differ from the real one
		last = "y"
	}
	e.do("POST", "/mcp", map[string]string{"Authorization": "Bearer sk-" + key[3:len(key)-1] + last}, body).Body.Close()
	ks, _ := e.keys.List()
	for _, c := range ks {
		if c.ID == k.ID && (c.Usage.MCPRequests != 2 || c.Usage.Requests != 0 || c.Usage.TotalTokens != 0) {
			t.Fatalf("usage: %+v", c.Usage)
		}
	}
}

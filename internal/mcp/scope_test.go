package mcp

import "testing"

// A refresh never widens the scope of the token it rotates (review SEC-07).
func TestNarrowScope(t *testing.T) {
	for _, c := range []struct{ granted, req, want string }{
		{"mcp", "mcp", "mcp"},
		{"mcp", "mcp admin", "mcp"},
		{"mcp", "admin", "mcp"},
		{"mcp offline_access", "offline_access", "offline_access"},
		{"mcp offline_access", "mcp mcp", "mcp"},
	} {
		if got := narrowScope(c.granted, c.req); got != c.want {
			t.Errorf("narrowScope(%q, %q) = %q, want %q", c.granted, c.req, got, c.want)
		}
	}
}

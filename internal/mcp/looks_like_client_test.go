package mcp

import (
	"net/http"
	"testing"
)

func TestLooksLikeClient(t *testing.T) {
	cases := []struct {
		name   string
		method string
		hdr    map[string]string
		want   bool
	}{
		{"browser html", "GET", map[string]string{"Accept": "text/html,application/xhtml+xml"}, false},
		{"empty", "GET", nil, false},
		{"accept json", "GET", map[string]string{"Accept": "application/json"}, true},
		{"accept sse", "GET", map[string]string{"Accept": "text/event-stream"}, true},
		{"accept both", "POST", map[string]string{"Accept": "application/json, text/event-stream"}, true},
		{"ct json", "POST", map[string]string{"Content-Type": "application/json"}, true},
		{"ct json charset", "POST", map[string]string{"Content-Type": "application/json; charset=utf-8"}, true},
		{"ct form", "POST", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, false},
		{"cors preflight", "OPTIONS", map[string]string{"Access-Control-Request-Method": "POST"}, true},
		{"options bare", "OPTIONS", nil, false},
	}
	for _, c := range cases {
		r, _ := http.NewRequest(c.method, "http://x/", nil)
		for k, v := range c.hdr {
			r.Header.Set(k, v)
		}
		if got := LooksLikeClient(r); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

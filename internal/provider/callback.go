package provider

import (
	"net/url"
	"strings"
)

// ParseCallback accepts a full callback URL, a raw query, or a bare code.
func ParseCallback(s string) (code, state string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	q := s
	if i := strings.Index(s, "?"); i >= 0 {
		q = s[i+1:]
	}
	if strings.Contains(q, "=") {
		v, err := url.ParseQuery(q)
		if err == nil {
			return v.Get("code"), v.Get("state")
		}
	}
	return s, ""
}

// Package mcp implements the MCP reverse proxy, skgate's OAuth 2.1 authorization server for
// MCP clients, and the upstream/client registries backing them.
package mcp

import (
	"net"
	"net/url"
	"strings"
)

// IsLoopbackHost reports whether host (no port) is 127.0.0.1, localhost or ::1.
func IsLoopbackHost(host string) bool {
	host = strings.ToLower(host)
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() && (host == "127.0.0.1" || host == "::1")
}

// RedirectAllowed reports whether uri may be used as an OAuth redirect_uri. skgate accepts any
// https origin (there is no origin allowlist): the URI must be absolute, without userinfo,
// fragment, whitespace or backslash, and use https, or http on a loopback host (any port). At
// /authorize it must additionally match one of the client's registered redirect_uris exactly.
func RedirectAllowed(uri string) bool {
	_, ok := RedirectCheck(uri)
	return ok
}

// RedirectCheck is RedirectAllowed with an explicit reason for a refusal (for logs and errors).
func RedirectCheck(uri string) (reason string, ok bool) {
	if uri == "" || strings.ContainsAny(uri, "\\ \t\r\n") {
		return "redirect_uri is empty or contains whitespace or backslashes", false
	}
	u, err := url.Parse(uri)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || strings.Contains(uri, "#") {
		return "redirect_uri is not an absolute URL without userinfo and fragment", false
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "", true
	case "http":
		if IsLoopbackHost(u.Hostname()) {
			return "", true
		}
		return "redirect_uri uses plain http on a non-loopback host", false
	}
	return "redirect_uri scheme must be https (or http on loopback)", false
}

// loopbackEquivalent reports whether a and b are loopback http redirect URIs that differ at most in port.
func loopbackEquivalent(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	if err1 != nil || err2 != nil || ua.Scheme != "http" || ub.Scheme != "http" {
		return false
	}
	return IsLoopbackHost(ua.Hostname()) && ua.Hostname() == ub.Hostname() && ua.Path == ub.Path && ua.RawQuery == ub.RawQuery
}

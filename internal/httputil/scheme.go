package httputil

import (
	"errors"
	"net"
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// privateSuffixes end the names of machines on a home or company network. They never have a public TLD.
var privateSuffixes = []string{".local", ".lan", ".internal", ".home.arpa", ".localhost", ".localdomain"}

// PublicHost reports whether host (a name or an IP, with or without a port) is on the internet: a name that ends
// in a public top-level domain. Single-label names (a Docker service), IP addresses, localhost and the usual
// home and company suffixes are not.
func PublicHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.Trim(strings.TrimSuffix(host, "."), "[]"))
	if host == "" || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return false
	}
	for _, s := range privateSuffixes {
		if strings.HasSuffix(host, s) {
			return false
		}
	}
	tld := host[strings.LastIndex(host, ".")+1:]
	_, icann := publicsuffix.PublicSuffix(tld)
	return icann
}

// ErrNeedHTTPS is the plain sentence for an address on the internet that does not use https.
var ErrNeedHTTPS = errors.New("use an https address for a server on the internet")

// CheckScheme accepts an absolute http(s) address and applies one rule: plain http only for a host that is not
// on the internet, https for every other.
func CheckScheme(u *url.URL) error {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("the address must be an absolute http(s) URL")
	}
	if u.Scheme == "http" && PublicHost(u.Host) {
		return ErrNeedHTTPS
	}
	return nil
}

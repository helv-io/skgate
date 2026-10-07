package httputil

import (
	"net/url"
	"testing"
)

func TestPublicHost(t *testing.T) {
	for host, want := range map[string]bool{
		"application": false, "application:8080": false, "localhost": false, "localhost:8080": false, "127.0.0.1": false, "10.0.0.5:80": false,
		"[::1]:80": false, "nas.local": false, "box.lan": false, "api.corp.internal": false, "x.home.arpa": false, "foo.localhost": false,
		"application.example": false, "box.test": false, "svc.cluster.local": false,
		"example.com": true, "api.example.com:8443": true, "Example.COM.": true, "foo.co.uk": true, "x.github.io": true, "a.dev": true, "a.app": true,
	} {
		if got := PublicHost(host); got != want {
			t.Errorf("PublicHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestCheckScheme(t *testing.T) {
	for raw, ok := range map[string]bool{
		"http://application:8080/openapi.json": true, "http://192.168.1.2": true, "http://nas.local/x": true, "http://localhost:3000": true,
		"https://example.com": true, "https://application:8080": true,
		"http://example.com": false, "http://api.example.com:8080/v1": false, "ftp://application": false, "application": false,
	} {
		u, _ := url.Parse(raw)
		if err := CheckScheme(u); (err == nil) != ok {
			t.Errorf("CheckScheme(%q) = %v, want ok=%v", raw, err, ok)
		}
	}
	u, _ := url.Parse("http://example.com")
	if CheckScheme(u) != ErrNeedHTTPS {
		t.Error("a public host over http should give ErrNeedHTTPS")
	}
}

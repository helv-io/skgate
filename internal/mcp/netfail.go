package mcp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"strings"
	"syscall"

	"github.com/helv-io/skgate/internal/reqlog"
)

// Kinds of transport failure, as far as skgate can tell them apart.
const (
	NetRefused = "refused"
	NetDNS     = "dns"
	NetTimeout = "timeout"
	NetTLS     = "tls"
	NetReset   = "reset"
	NetOther   = "other"
)

// NetFail is a failed attempt to reach an upstream. Error() names the kind of failure and never an
// address, so it is safe in anything a client or a non-admin screen shows. Detail holds what the Go
// network stack said (it can name the resolved IP and port) and is for the admin Test screen only.
type NetFail struct {
	Kind   string
	Detail string
	// Reading marks a failure after the connection was made (while reading the reply).
	Reading bool
}

var netFailText = map[string]string{
	NetRefused: "connection refused",
	NetDNS:     "host name not found",
	NetTimeout: "timed out",
	NetTLS:     "TLS handshake failed",
	NetReset:   "connection reset",
	NetOther:   "network error",
}

// Public is the failure in a few words, with no host, address or port.
func (e *NetFail) Public() string { return netFailText[e.Kind] }

func (e *NetFail) Error() string {
	if e.Reading {
		return "reading upstream response: " + e.Public()
	}
	return "upstream unreachable: " + e.Public()
}

// classifyNet turns a transport error into a NetFail.
func classifyNet(ctx context.Context, err error, reading bool) *NetFail {
	kind := NetOther
	var dns *net.DNSError
	var ne net.Error
	var uae x509.UnknownAuthorityError
	var hne x509.HostnameError
	var cie x509.CertificateInvalidError
	var rhe tls.RecordHeaderError
	switch {
	case ctx.Err() == context.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded):
		kind = NetTimeout
	case errors.As(err, &dns):
		kind = NetDNS
		if dns.IsTimeout {
			kind = NetTimeout
		}
	case errors.Is(err, syscall.ECONNREFUSED):
		kind = NetRefused
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE):
		kind = NetReset
	case errors.As(err, &uae), errors.As(err, &hne), errors.As(err, &cie), errors.As(err, &rhe),
		strings.Contains(err.Error(), "tls:"), strings.Contains(err.Error(), "x509:"):
		kind = NetTLS
	case errors.As(err, &ne) && ne.Timeout():
		kind = NetTimeout
	}
	return &NetFail{Kind: kind, Detail: reqlog.Sanitize(err), Reading: reading}
}

// upstreamErr is the error for a failed upstream call: a *NetFail for transport failures. Errors skgate
// wrote itself (a refused redirect, for example) name no address and are kept as they are.
func upstreamErr(ctx context.Context, err error, reading bool) error {
	var ue *url.Error
	var ne net.Error
	if !errors.As(err, &ue) && !errors.As(err, &ne) && ctx.Err() == nil && !errors.Is(err, context.DeadlineExceeded) {
		if reading {
			return errors.New("reading upstream response: " + reqlog.Sanitize(err))
		}
		return errors.New("upstream unreachable: " + reqlog.Sanitize(err))
	}
	return classifyNet(ctx, err, reading)
}

// AsNetFail returns the NetFail inside err, if any.
func AsNetFail(err error) (*NetFail, bool) {
	var nf *NetFail
	if errors.As(err, &nf) {
		return nf, true
	}
	return nil, false
}

// hostPort returns the host and port an upstream URL connects to (the default port of the scheme
// when the URL names none).
func hostPort(raw string) (host, port string) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", ""
	}
	port = u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return u.Hostname(), port
}

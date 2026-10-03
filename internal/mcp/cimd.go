package mcp

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/helv-io/skgate/internal/reqlog"
)

// Client ID Metadata Documents: a client may use an https URL as its client_id. skgate fetches the
// JSON document at that URL and treats it as the registration. The URL is chosen by whoever opens
// /authorize, so the fetch is hardened: public addresses only (checked on the address actually
// connected to, so DNS rebinding cannot help), no redirects, no proxy, a short time and a small size,
// a cache, and a cap on concurrent fetches.
const (
	cimdMaxBody       = 64 << 10
	cimdTimeout       = 5 * time.Second
	cimdMinTTL        = 5 * time.Minute
	cimdDefaultTTL    = time.Hour
	cimdMaxTTL        = 24 * time.Hour
	cimdFailTTL       = time.Minute // a failed fetch is not repeated for this long
	cimdMaxInflight   = 4
	cimdMaxClientIDLn = 2048
	cimdKeep          = 500 // client rows kept, newest first
)

// cimdSource is Client.Source of a client learned from a metadata document.
const cimdSource = "cimd"

// ParseMetadataClientID reports whether id is an https URL usable as a client ID metadata document
// location, and why not when it is not: https, a host, a path other than "/", no userinfo, fragment
// or dot segments.
func ParseMetadataClientID(id string) (*url.URL, string) {
	if !strings.HasPrefix(strings.ToLower(id), "https://") {
		return nil, "not an https URL"
	}
	if len(id) > cimdMaxClientIDLn || strings.IndexFunc(id, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\\' }) >= 0 {
		return nil, "client_id is too long or has whitespace"
	}
	u, err := url.Parse(id)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.Contains(id, "#") || u.Opaque != "" {
		return nil, "client_id is not an https URL without userinfo and fragment"
	}
	if u.Path == "" || u.Path == "/" {
		return nil, "client_id needs a path"
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." {
			return nil, "client_id has dot segments"
		}
	}
	return u, ""
}

// isPublicIP reports whether ip is a globally routable unicast address. Private, loopback,
// link-local, shared, documentation, benchmarking, reserved and multicast ranges are all refused, and
// so are IPv6 forms that embed an IPv4 address (mapped, NAT64, 6to4) when that address is refused.
func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		return publicV4(v4)
	}
	if len(ip) != net.IPv6len || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, n := range blockedV6 {
		if n.Contains(ip) {
			// NAT64 and 6to4 are fine when they point at a public IPv4 address.
			if n == nat64 {
				return publicV4(ip[12:16])
			}
			if n == sixToFour {
				return publicV4(ip[2:6])
			}
			return false
		}
	}
	return true
}

func publicV4(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, n := range blockedV4 {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

func cidr(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

var (
	blockedV4 = []*net.IPNet{cidr("0.0.0.0/8"), cidr("100.64.0.0/10"), cidr("192.0.0.0/24"), cidr("192.0.2.0/24"), cidr("192.88.99.0/24"),
		cidr("198.18.0.0/15"), cidr("198.51.100.0/24"), cidr("203.0.113.0/24"), cidr("240.0.0.0/4")}
	nat64     = cidr("64:ff9b::/96")
	sixToFour = cidr("2002::/16")
	blockedV6 = []*net.IPNet{nat64, sixToFour, cidr("64:ff9b:1::/48"), cidr("100::/64"), cidr("2001::/23"), cidr("2001:db8::/32"), cidr("3fff::/20"), cidr("5f00::/16")}
)

// errNotPublic is the dialer's refusal. Its text names no address.
var errNotPublic = errors.New("the host resolves to a non-public address")

// publicDialer resolves the host, refuses when any answer is not public, and connects to one of the
// resolved addresses itself, so the address checked is the address used (no second lookup that a
// rebinding DNS server could answer differently).
type publicDialer struct {
	lookup func(ctx context.Context, host string) ([]net.IP, error)
	dial   func(ctx context.Context, network, addr string) (net.Conn, error)
}

func systemDialer() publicDialer {
	return publicDialer{
		lookup: func(ctx context.Context, host string) ([]net.IP, error) {
			as, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			ips := make([]net.IP, len(as))
			for i, a := range as {
				ips[i] = a.IP
			}
			return ips, err
		},
		dial: (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
	}
}

func (p publicDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else if ips, err = p.lookup(ctx, host); err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("no address")
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return nil, errNotPublic
		}
	}
	var last error
	for _, ip := range ips {
		c, err := p.dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return c, nil
		}
		last = err
	}
	return nil, last
}

func newCIMDClient() *http.Client {
	return &http.Client{
		Timeout: cimdTimeout,
		Transport: &http.Transport{
			Proxy:                 nil, // never through an environment proxy: it would see and reach what the dialer refuses
			DialContext:           systemDialer().DialContext,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout:   cimdTimeout,
			ResponseHeaderTimeout: cimdTimeout,
			DisableKeepAlives:     true,
			ForceAttemptHTTP2:     true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// cimdBook is the freshness of fetched documents and the fetches in progress. The documents
// themselves are stored as client rows.
type cimdBook struct {
	mu       sync.Mutex
	fresh    map[string]cimdState
	inflight map[string]*cimdFlight
	client   *http.Client // tests replace it
}

type cimdState struct {
	until time.Time
	err   error // the last fetch failed; until is when to try again
}

type cimdFlight struct {
	done chan struct{}
	err  error
}

// Fetch errors are shown to nobody but the log: the authorization page says the client is unknown.
var errCIMDBusy = errors.New("too many metadata document fetches in progress")

// lookupClient finds a client by ID: a registered one (DCR or admin), or one described by a client ID
// metadata document. The error says why a document client was refused (for the log).
func (s *Server) lookupClient(ctx context.Context, id string) (Client, error) {
	c, ok := s.Clients.Get(id)
	if ok && c.Source != cimdSource {
		return c, nil
	}
	if _, why := ParseMetadataClientID(id); why != "" {
		if ok { // cannot happen for rows we wrote; keep what is stored
			return c, nil
		}
		return Client{}, errors.New("client_id is not registered")
	}
	b := &s.cimd
	b.mu.Lock()
	if b.fresh == nil {
		b.fresh, b.inflight = map[string]cimdState{}, map[string]*cimdFlight{}
	}
	st, known := b.fresh[id]
	now := time.Now()
	if known && now.Before(st.until) {
		b.mu.Unlock()
		if ok { // fresh, or failing and the stored copy is all there is
			return c, nil
		}
		return Client{}, st.err
	}
	if f := b.inflight[id]; f != nil {
		b.mu.Unlock()
		<-f.done
		if f.err != nil && !ok {
			return Client{}, f.err
		}
		c, ok = s.Clients.Get(id)
		if !ok {
			return Client{}, f.err
		}
		return c, nil
	}
	if len(b.inflight) >= cimdMaxInflight {
		b.mu.Unlock()
		if ok {
			return c, nil
		}
		return Client{}, errCIMDBusy
	}
	f := &cimdFlight{done: make(chan struct{})}
	b.inflight[id] = f
	b.mu.Unlock()

	ttl, err := s.fetchMetadata(ctx, id)
	b.mu.Lock()
	if err != nil {
		b.fresh[id] = cimdState{until: time.Now().Add(cimdFailTTL), err: err}
	} else {
		b.fresh[id] = cimdState{until: time.Now().Add(ttl)}
	}
	if len(b.fresh) > 4*cimdKeep { // bound memory: forget everything, the rows remain
		b.fresh = map[string]cimdState{}
	}
	delete(b.inflight, id)
	f.err = err
	b.mu.Unlock()
	close(f.done)

	if err != nil {
		if ok { // keep serving the stored copy while the document is unreachable
			return c, nil
		}
		return Client{}, err
	}
	c, ok = s.Clients.Get(id)
	if !ok {
		return Client{}, errors.New("could not store client")
	}
	return c, nil
}

// fetchMetadata downloads and validates the document of id and stores the client. It returns how long
// the copy may be used.
func (s *Server) fetchMetadata(ctx context.Context, id string) (time.Duration, error) {
	u, why := ParseMetadataClientID(id)
	if why != "" {
		return 0, errors.New(why)
	}
	hc := s.cimd.client
	if hc == nil {
		hc = newCIMDClient()
	}
	ctx, cancel := context.WithTimeout(ctx, cimdTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, errors.New("invalid client_id URL")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "skgate (client id metadata)")
	resp, err := hc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetching the client metadata document failed: %s", describeFetchErr(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		if resp.StatusCode/100 == 3 {
			return 0, errors.New("the client metadata document location redirects, which skgate does not follow")
		}
		return 0, fmt.Errorf("the client metadata document answered HTTP %d", resp.StatusCode)
	}
	if ct := strings.ToLower(resp.Header.Get("Content-Type")); !strings.Contains(ct, "json") {
		return 0, errors.New("the client metadata document is not JSON (Content-Type)")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, cimdMaxBody+1))
	if err != nil {
		return 0, fmt.Errorf("reading the client metadata document failed: %s", describeFetchErr(err))
	}
	if len(body) > cimdMaxBody {
		return 0, fmt.Errorf("the client metadata document is larger than %d KB", cimdMaxBody>>10)
	}
	c, err := parseMetadata(id, u, body)
	if err != nil {
		return 0, err
	}
	if err := s.Clients.PutMetadataClient(c); err != nil {
		return 0, errors.New("could not store client")
	}
	return cacheTTL(resp.Header.Get("Cache-Control")), nil
}

func describeFetchErr(err error) string {
	if errors.Is(err, errNotPublic) {
		return errNotPublic.Error()
	}
	return classifyNet(context.Background(), err, false).Public()
}

// parseMetadata validates a document and returns the client it describes.
func parseMetadata(id string, u *url.URL, body []byte) (Client, error) {
	var doc struct {
		ClientID      string          `json:"client_id"`
		ClientName    string          `json:"client_name"`
		RedirectURIs  []string        `json:"redirect_uris"`
		AuthMethod    string          `json:"token_endpoint_auth_method"`
		GrantTypes    []string        `json:"grant_types"`
		ResponseTypes []string        `json:"response_types"`
		Secret        json.RawMessage `json:"client_secret"`
		SecretExpires json.RawMessage `json:"client_secret_expires_at"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return Client{}, errors.New("the client metadata document is not a JSON object")
	}
	if doc.ClientID != id {
		return Client{}, errors.New("the document's client_id is not the URL it was fetched from")
	}
	if len(doc.Secret) > 0 || len(doc.SecretExpires) > 0 {
		return Client{}, errors.New("the document carries a client secret, which is not allowed")
	}
	if doc.AuthMethod != "" && doc.AuthMethod != "none" {
		return Client{}, errors.New("the document asks for token_endpoint_auth_method " + truncate(doc.AuthMethod, 40) + "; only none is supported for metadata document clients")
	}
	if len(doc.GrantTypes) > 0 && len(supported(doc.GrantTypes, dcrGrants, nil)) == 0 {
		return Client{}, errors.New("none of the document's grant_types is supported")
	}
	if len(doc.ResponseTypes) > 0 && len(supported(doc.ResponseTypes, dcrResponses, nil)) == 0 {
		return Client{}, errors.New("none of the document's response_types is supported")
	}
	if len(doc.RedirectURIs) == 0 || len(doc.RedirectURIs) > 10 {
		return Client{}, errors.New("the document must list 1 to 10 redirect_uris")
	}
	var uris []string
	for _, r := range doc.RedirectURIs {
		if _, ok := RedirectCheck(r); ok {
			uris = append(uris, r)
		}
	}
	if len(uris) == 0 {
		return Client{}, errors.New("none of the document's redirect_uris is usable")
	}
	name := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(doc.ClientName))
	name = truncate(name, 100)
	if name == "" {
		name = u.Hostname()
	}
	return Client{ID: id, Name: name, RedirectURIs: uris, AuthMethod: "none", Source: cimdSource}, nil
}

// cacheTTL reads max-age from Cache-Control, within the bounds. no-store and no-cache get the minimum.
func cacheTTL(cc string) time.Duration {
	ttl := cimdDefaultTTL
	for _, d := range strings.Split(strings.ToLower(cc), ",") {
		d = strings.TrimSpace(d)
		switch {
		case d == "no-store" || d == "no-cache":
			return cimdMinTTL
		case strings.HasPrefix(d, "max-age="):
			if n, err := strconv.Atoi(strings.Trim(d[len("max-age="):], `"`)); err == nil && n >= 0 {
				ttl = time.Duration(n) * time.Second
			}
		}
	}
	if ttl < cimdMinTTL {
		return cimdMinTTL
	}
	if ttl > cimdMaxTTL {
		return cimdMaxTTL
	}
	return ttl
}

// ClientHost is the host of a metadata document client ID, for the consent page; empty for others.
func ClientHost(c Client) string {
	if c.Source != cimdSource {
		return ""
	}
	return reqlog.HostOf(c.ID)
}

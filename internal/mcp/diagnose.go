package mcp

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// candidatePorts are the ports an MCP server in a container usually listens on. They are only tried
// on the host of an upstream that could not be reached, and only when that host is on a private
// network (see privateHost), so this is a look around the upstream's own machine and not a port scan.
var candidatePorts = []int{80, 3000, 3001, 3002, 4000, 5000, 5001, 6000, 7000, 7777, 8000, 8001, 8002, 8008, 8080, 8081, 8082, 8090, 8100, 8787, 8888, 9000, 9001, 9090, 9999}

const (
	portDialTimeout  = 400 * time.Millisecond
	portProbeTimeout = 3 * time.Second
)

// PortGuess is a port found open on the upstream's host.
type PortGuess struct {
	Port   int
	Answer string // what it said to an MCP initialize on the configured path, in words
	MCP    bool   // a valid initialize result
}

// Diagnosis explains why an upstream could not be reached and what might fix it.
type Diagnosis struct {
	Kind     string
	Host     string
	Port     string
	Loopback bool
	Ports    []PortGuess
}

// CheckReachable opens a TCP connection to the upstream's host and port and, when that fails, returns
// a Diagnosis (nil when something answers). Managed upstreams have no network address to check.
func (s *Server) CheckReachable(ctx context.Context, up Upstream) *Diagnosis {
	if up.Managed() {
		return nil
	}
	host, port := hostPort(up.URL)
	if host == "" {
		return nil
	}
	d := net.Dialer{Timeout: 3 * time.Second}
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err == nil {
		c.Close()
		return nil
	}
	nf := classifyNet(ctx, err, false)
	if nf.Kind != NetRefused && nf.Kind != NetDNS {
		return nil
	}
	return s.Diagnose(ctx, up, nf.Kind)
}

// Diagnose builds the Diagnosis for a failure of the given kind. For a refused connection on a
// private host it looks for ports that are open there.
func (s *Server) Diagnose(ctx context.Context, up Upstream, kind string) *Diagnosis {
	host, port := hostPort(up.URL)
	d := &Diagnosis{Kind: kind, Host: host, Port: port}
	if ip := net.ParseIP(host); (ip != nil && ip.IsLoopback()) || strings.EqualFold(host, "localhost") {
		d.Loopback = true
	}
	if kind != NetRefused || !privateHost(ctx, host) {
		return d
	}
	d.Ports = s.findPorts(ctx, up, host, port, d.Loopback)
	return d
}

// privateHost reports whether host is a single-label name (a container or service name), a
// loopback, private or link-local address, or a name that resolves only to such addresses.
func privateHost(ctx context.Context, host string) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return privateIP(ip)
	}
	if !strings.Contains(host, ".") {
		return true
	}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(rctx, host)
	if err != nil || len(addrs) == 0 {
		return false
	}
	for _, a := range addrs {
		if !privateIP(a.IP) {
			return false
		}
	}
	return true
}

func privateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

func (s *Server) findPorts(ctx context.Context, up Upstream, host, configured string, loopback bool) []PortGuess {
	skip := map[int]bool{}
	if n, err := strconv.Atoi(configured); err == nil {
		skip[n] = true
	}
	if loopback && s.Cfg != nil { // skgate's own listener is not the upstream
		if _, p, err := net.SplitHostPort(s.Cfg.Listen); err == nil {
			if n, err := strconv.Atoi(p); err == nil {
				skip[n] = true
			}
		}
	}
	var (
		mu   sync.Mutex
		open []int
		wg   sync.WaitGroup
	)
	for _, p := range candidatePorts {
		if skip[p] {
			continue
		}
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			dl := net.Dialer{Timeout: portDialTimeout}
			c, err := dl.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(p)))
			if err != nil {
				return
			}
			c.Close()
			mu.Lock()
			open = append(open, p)
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	sort.Ints(open)
	out := make([]PortGuess, len(open))
	for i, p := range open {
		wg.Add(1)
		go func(i, p int) {
			defer wg.Done()
			out[i] = s.askPort(ctx, up, p)
		}(i, p)
	}
	wg.Wait()
	sort.SliceStable(out, func(i, j int) bool { return out[i].MCP && !out[j].MCP })
	return out
}

// askPort sends an MCP initialize to the configured path on another port, without credentials.
func (s *Server) askPort(ctx context.Context, up Upstream, port int) PortGuess {
	g := PortGuess{Port: port, Answer: "open"}
	u, err := url.Parse(up.URL)
	if err != nil {
		return g
	}
	u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(port))
	probe := Upstream{URL: u.String(), AuthKind: AuthNone, HostOverride: up.HostOverride}
	rep, err := s.rpcPost(ctx, probe, initBody(), 1, "", "", portProbeTimeout)
	switch {
	case rep == nil:
		g.Answer = "open, no HTTP answer"
	case rep.Status == 401 || rep.Status == 403:
		g.Answer = fmt.Sprintf("answers HTTP %d, it wants credentials", rep.Status)
		g.MCP = true
	case rep.Status/100 == 2 && err == nil && rep.Err == nil && len(rep.Result) > 0 && string(rep.Result) != "null":
		g.Answer, g.MCP = "answers MCP at the configured path", true
	case rep.Status/100 == 2:
		g.Answer = "answers HTTP 200 but not MCP at the configured path"
	default:
		g.Answer = fmt.Sprintf("answers HTTP %d at the configured path", rep.Status)
	}
	return g
}

// Hint is the advice shown with a failed test. withHost names the host (the admin Test screen); the
// short form is for places that must not name internal hosts.
func (d *Diagnosis) Hint(withHost bool) string {
	where := "the upstream"
	if withHost {
		where = d.Host
	}
	switch d.Kind {
	case NetDNS:
		return "The name " + where + " does not resolve from skgate. For a Docker container, skgate and the container must share a Docker network, and the name must be the container or service name."
	case NetRefused:
		var b strings.Builder
		fmt.Fprintf(&b, "Nothing is listening on port %s of %s.", d.Port, where)
		if d.Loopback {
			b.WriteString(" localhost inside skgate's container is skgate itself: use the container name on a shared Docker network instead.")
		}
		switch len(d.Ports) {
		case 0:
			if !d.Loopback {
				b.WriteString(" Check the port the server listens on (a container's published port is not the port inside it).")
			}
		default:
			b.WriteString(" Ports open there:")
			for _, g := range d.Ports {
				fmt.Fprintf(&b, " %d (%s);", g.Port, g.Answer)
			}
			b.WriteString(" try the one that answers MCP.")
		}
		return b.String()
	}
	return ""
}

// Suggested is the port most likely to be the right one: the first that answers MCP, or 0.
func (d *Diagnosis) Suggested() int {
	for _, g := range d.Ports {
		if g.MCP {
			return g.Port
		}
	}
	return 0
}

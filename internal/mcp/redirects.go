package mcp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// maxUpstreamHops is how many upstream redirects skgate follows itself for one request.
const maxUpstreamHops = 5

// doFollow sends req to the upstream and follows redirects server-side, so an upstream redirect
// (for example 307 from /mcp/ to /mcp) is never handed to the client, which could not follow it
// (the Location usually holds an internal hostname). The method and body are kept on every hop,
// which is what 307 and 308 require and what an MCP JSON-RPC POST needs on 301, 302 and 303 as
// well (body is the buffered request body, nil for none).
//
// Only redirects that stay on the upstream's own host are followed, so the outbound credentials
// are never sent to another host; a Location that names the host override counts as the same
// host. Anything else, and more than maxUpstreamHops hops, is an error whose text names no URL.
func (s *Server) doFollow(req *http.Request, body []byte) (*http.Response, error) {
	origin := req.URL.Host
	override := req.Host
	for hop := 0; ; hop++ {
		if body != nil {
			req.Body = io.NopCloser(bytes.NewReader(body))
			req.ContentLength = int64(len(body))
		}
		resp, err := s.HTTP.Do(req)
		if err != nil {
			return nil, err
		}
		switch resp.StatusCode {
		case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		default:
			return resp, nil
		}
		loc := resp.Header.Get("Location")
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if loc == "" {
			return nil, fmt.Errorf("upstream answered HTTP %d without a Location", resp.StatusCode)
		}
		if hop >= maxUpstreamHops {
			return nil, fmt.Errorf("upstream redirected more than %d times", maxUpstreamHops)
		}
		next, err := req.URL.Parse(loc)
		if err != nil {
			return nil, errors.New("upstream sent an invalid redirect")
		}
		sameHost := next.Host == origin || (override != "" && next.Host == override)
		httpsUpgrade := req.URL.Scheme == "http" && next.Scheme == "https" && strings.EqualFold(next.Hostname(), req.URL.Hostname())
		if !sameHost && !httpsUpgrade {
			return nil, fmt.Errorf("upstream redirected (HTTP %d) to another host, which skgate does not follow", resp.StatusCode)
		}
		if next.Scheme != "http" && next.Scheme != "https" {
			return nil, errors.New("upstream redirected to an unsupported scheme")
		}
		if sameHost {
			next.Host = origin // a Location naming the override host still connects to the real one
		}
		nreq := req.Clone(req.Context())
		nreq.URL = next
		method := req.Method
		if resp.StatusCode == http.StatusSeeOther && method != http.MethodGet && method != http.MethodHead {
			method, body = http.MethodGet, nil
			nreq.Body, nreq.ContentLength = nil, 0
			nreq.Header.Del("Content-Type")
		}
		nreq.Method = method
		req = nreq
		origin = next.Host
	}
}

// doUpstream sends req (built for up.URL) and follows redirects like doFollow. When the
// attempt fails at the transport level, or the upstream answers 404 or 405 (the usual symptoms of a
// trailing-slash mismatch, next to the redirects doFollow already absorbs), it retries once with the
// trailing slash of the path toggled. When that works, the stored URL itself is corrected to the
// spelling that works (no separate state, nothing shown in the UI), so later calls go straight there. The first outcome is returned when the retry does
// not do better, so a genuine 404 is reported unchanged. Timeouts and cancellations are not retried.
func (s *Server) doUpstream(up Upstream, req *http.Request, body []byte) (*http.Response, error) {
	resp, err := s.doFollow(req, body)
	var ne net.Error
	timedOut := errors.As(err, &ne) && ne.Timeout()
	bad := err != nil && req.Context().Err() == nil && !timedOut || err == nil && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed)
	if !bad {
		return resp, err
	}
	alt := req.Clone(req.Context())
	altURL := ToggleSlash(req.URL.String())
	if alt.URL, _ = url.Parse(altURL); alt.URL == nil {
		return resp, err
	}
	retry, rerr := s.doFollow(alt, body)
	if rerr != nil || retry.StatusCode/100 != 2 {
		if retry != nil {
			io.Copy(io.Discard, io.LimitReader(retry.Body, 4096))
			retry.Body.Close()
		}
		return resp, err
	}
	if resp != nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
	}
	// The retry worked: persist the working spelling into the URL itself.
	s.correctURL(up, altURL)
	return retry, nil
}

// correctURL stores the working spelling of an upstream's URL. altURL is the request URL that
// worked; the stored URL gets the same trailing-slash form (its query, fragment and the rest stay as
// configured). It only applies to a stored remote upstream whose URL has not been edited meanwhile.
func (s *Server) correctURL(up Upstream, altURL string) {
	if up.Alias == "" || up.Managed() {
		return
	}
	fixed := ToggleSlash(up.URL)
	if !slashOf(fixed, altURL) {
		return
	}
	if changed, err := s.Upstreams.SetWorkingURL(up.Alias, up.URL, fixed); err == nil && changed {
		s.Log.Printf("upstream_url alias=%s corrected=true", up.Alias)
	}
}

// slashOf reports whether a and b agree on the trailing slash of the path.
func slashOf(a, b string) bool {
	ua, e1 := url.Parse(a)
	ub, e2 := url.Parse(b)
	return e1 == nil && e2 == nil && strings.HasSuffix(ua.Path, "/") == strings.HasSuffix(ub.Path, "/")
}

// rewriteLocation stops a Location header from leaking an upstream address. doFollow already
// consumes every redirect status, so this only matters for other responses that carry one: an
// absolute Location is dropped, a relative one is mapped onto skgate's own /mcp/{alias} URL.
func rewriteLocation(h http.Header, up Upstream, publicBase string) {
	loc := h.Get("Location")
	if loc == "" {
		return
	}
	if l, err := url.Parse(loc); err != nil || l.Host != "" || l.Scheme != "" {
		h.Del("Location")
		return
	}
	h.Set("Location", strings.TrimRight(publicBase, "/")+"/mcp/"+up.Alias)
}

package openapi

import (
	"context"
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/helv-io/skgate/internal/httputil"
)

// SpecPaths are the places an API usually serves its description, tried in this order after the address as
// entered. This is the one list: nothing else in skgate names a place.
var SpecPaths = []string{
	"/openapi.json", "/openapi.yaml", "/openapi.yml", "/openapi.toml",
	"/swagger.json", "/swagger.yaml", "/swagger.yml",
	"/api/openapi.json", "/api/openapi.yaml", "/api/openapi.yml",
	"/api/swagger.json", "/api/swagger.yaml", "/api/swagger.yml",
	"/v1/api-docs", "/v2/api-docs", "/v3/api-docs", "/v3/api-docs.yaml",
	"/api-docs", "/api-docs.json",
	"/docs/openapi.json", "/docs/openapi.yaml", "/docs/openapi.yml",
	"/api/docs/openapi.json", "/api/docs/openapi.yaml", "/api/docs/openapi.yml",
}

// DocPages are the pages that show the documentation of an API and name the description it reads.
var DocPages = []string{"/", "/docs", "/redoc", "/swagger", "/api/docs", "/swagger-ui/"}

// Limits of a search. A search asks with GET only, and only the host that was entered.
const (
	probeTimeout   = 6 * time.Second
	searchBudget   = 25 * time.Second
	maxProbes      = 80
	maxScriptReads = 3
)

// Plain sentences for the two ways a search ends without a description.
var (
	ErrNoSpec      = errors.New("skgate found no description at that address; paste it instead")
	ErrUnreachable = errors.New("skgate cannot reach that address")
)

// Found is the description a search settled on.
type Found struct {
	Doc *Doc
	Raw []byte
	// URL is where the description was read, after redirects.
	URL string
	// Base is the address entered, up to its path prefix: the server to use when the description names none.
	Base string
	// Direct is true when the address entered was the description itself.
	Direct bool
}

// Discover finds the description for an address: the description itself, or a base address or documentation page
// from which the usual places are tried. It saves nothing and tells nothing about the places it tried.
func Discover(ctx context.Context, raw string) (*Found, error) {
	starts, err := entryURLs(raw)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, searchBudget)
	defer cancel()
	lastErr := ErrNoSpec
	for _, u := range starts {
		f, err := newSearch(ctx, u).run()
		if f != nil {
			return f, nil
		}
		if err != nil && !errors.Is(err, ErrNoSpec) {
			lastErr = err
		}
	}
	return nil, lastErr
}

// entryURLs turns what a person typed into the addresses to start from. An address without a scheme is a host
// (mealie:9000): a host on the internet is reached over https, any other first over http.
func entryURLs(raw string) ([]*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("give the address of the API or paste its description")
	}
	if strings.ContainsAny(raw, " \t\r\n") {
		return nil, errors.New("the address must not contain spaces")
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return nil, errors.New("the address must be an absolute http(s) URL")
		}
		if u.User != nil {
			return nil, errors.New("the address must not carry a user name or password")
		}
		if err := httputil.CheckScheme(u); err != nil {
			return nil, err
		}
		u.Fragment = ""
		return []*url.URL{u}, nil
	}
	u, err := url.Parse("http://" + raw)
	if err != nil || u.Host == "" || u.User != nil || u.Hostname() == "" {
		return nil, errors.New("the address must be an absolute http(s) URL")
	}
	u.Fragment = ""
	if httputil.PublicHost(u.Host) {
		u.Scheme = "https"
		return []*url.URL{u}, nil
	}
	s := *u
	s.Scheme = "https"
	return []*url.URL{u, &s}, nil
}

type search struct {
	ctx    context.Context
	start  *url.URL
	host   string // the host entered: nothing else is asked
	client *http.Client
	tried  map[string]bool
	n      int
	// reached is set when any request got an answer.
	reached bool
}

func newSearch(ctx context.Context, start *url.URL) *search {
	s := &search{ctx: ctx, start: start, host: strings.ToLower(start.Hostname()), tried: map[string]bool{}}
	s.client = &http.Client{Timeout: probeTimeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return errors.New("too many redirects")
		}
		if strings.ToLower(req.URL.Hostname()) != s.host || (via[0].URL.Scheme == "https" && req.URL.Scheme != "https") {
			return http.ErrUseLastResponse // another host never gets asked
		}
		return nil
	}}
	return s
}

// get requests one address and returns the body of a 200 answer. A false ok means an answer that is no use.
func (s *search) get(u string) (body []byte, final *url.URL, ok bool, err error) {
	if s.tried[u] || s.n >= maxProbes || s.ctx.Err() != nil {
		return nil, nil, false, nil
	}
	s.tried[u] = true
	s.n++
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, false, nil
	}
	req.Header.Set("Accept", "application/json, application/yaml, text/yaml, application/toml, text/html;q=0.8, */*;q=0.5")
	req.Header.Set("User-Agent", "skgate-openapi")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil, false, err
	}
	defer resp.Body.Close()
	s.reached = true
	if resp.StatusCode != http.StatusOK || strings.ToLower(resp.Request.URL.Hostname()) != s.host {
		return nil, nil, false, nil
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxSpecBytes+1))
	if err != nil || len(b) > MaxSpecBytes {
		return nil, nil, false, nil
	}
	return b, resp.Request.URL, true, nil
}

// try reads an address and accepts it when its content is a description, whatever the name or content type.
func (s *search) try(u string) (*Found, []byte, *url.URL, error) {
	b, final, ok, err := s.get(u)
	if err != nil || !ok {
		return nil, nil, nil, err
	}
	if d, err := Parse(b); err == nil {
		return &Found{Doc: d, Raw: b, URL: final.String()}, nil, nil, nil
	}
	return nil, b, final, nil
}

func (s *search) run() (*Found, error) {
	finish := func(f *Found, direct bool, prefix string) *Found {
		f.Direct = direct
		b := *s.start
		b.Path, b.RawQuery, b.RawPath = prefix, "", ""
		f.Base = strings.TrimRight(b.String(), "/")
		return f
	}
	f, page, final, err := s.try(s.start.String())
	if err != nil {
		return nil, ErrUnreachable
	}
	if f != nil {
		return finish(f, true, ""), nil
	}
	var linked []string
	if page != nil {
		linked = s.links(page, final)
	}
	for _, l := range linked {
		if f, _, _, _ := s.try(l); f != nil {
			return finish(f, false, prefixOf(s.start)), nil
		}
	}
	for _, prefix := range prefixes(s.start) {
		for _, p := range SpecPaths {
			if f, _, _, _ := s.try(s.origin() + prefix + p); f != nil {
				return finish(f, false, prefix), nil
			}
		}
	}
	for _, prefix := range prefixes(s.start) {
		for _, p := range DocPages {
			_, pg, fin, _ := s.try(s.origin() + prefix + p)
			if pg == nil {
				continue
			}
			for _, l := range s.links(pg, fin) {
				if f, _, _, _ := s.try(l); f != nil {
					return finish(f, false, prefix), nil
				}
			}
		}
	}
	if !s.reached {
		return nil, ErrUnreachable
	}
	return nil, ErrNoSpec
}

func (s *search) origin() string { return s.start.Scheme + "://" + s.start.Host }

// prefixOf is the path of the address entered without a file name: the part of the address the API lives under.
func prefixOf(u *url.URL) string {
	p := strings.TrimRight(u.Path, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 && strings.Contains(p[i+1:], ".") {
		p = p[:i]
	}
	return p
}

// prefixes are the paths the usual places are tried under: the one entered, then the root.
func prefixes(u *url.URL) []string {
	if p := prefixOf(u); p != "" {
		return []string{p, ""}
	}
	return []string{""}
}

var (
	linkURLRE   = regexp.MustCompile(`(?i)\burl\s*:\s*["']([^"'\s]+)["']`)
	specURLRE   = regexp.MustCompile(`(?i)\b(?:spec-url|specurl)\s*[=:]\s*["']([^"'\s]+)["']`)
	redocInitRE = regexp.MustCompile(`(?i)Redoc\.init\(\s*["']([^"'\s]+)["']`)
	linkTagRE   = regexp.MustCompile(`(?is)<link\b[^>]*>`)
	attrRE      = regexp.MustCompile(`(?is)\b([a-z-]+)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	scriptRE    = regexp.MustCompile(`(?is)<script\b[^>]*\bsrc\s*=\s*["']([^"']+)["']`)
)

// links lists the same-host addresses a documentation page names as its description: the url of Swagger UI,
// the spec-url of Redoc and link tags that say they point at a description. Settings kept in the script that
// starts Swagger UI are read too.
func (s *search) links(page []byte, at *url.URL) []string {
	text := string(page)
	if len(text) > 1<<20 {
		text = text[:1<<20]
	}
	var raws []string
	collect := func(t string) {
		for _, re := range []*regexp.Regexp{linkURLRE, specURLRE, redocInitRE} {
			for _, m := range re.FindAllStringSubmatch(t, -1) {
				raws = append(raws, m[1])
			}
		}
	}
	collect(text)
	for _, tag := range linkTagRE.FindAllString(text, -1) {
		attrs := map[string]string{}
		for _, m := range attrRE.FindAllStringSubmatch(tag, -1) {
			attrs[strings.ToLower(m[1])] = m[2] + m[3]
		}
		rel, typ, href := strings.ToLower(attrs["rel"]), strings.ToLower(attrs["type"]), attrs["href"]
		hl := strings.ToLower(href)
		switch {
		case href == "":
		case strings.Contains(rel, "service-desc"), strings.Contains(rel, "openapi"), strings.Contains(rel, "describedby"):
			raws = append(raws, href)
		case strings.Contains(typ, "openapi"), strings.Contains(typ, "swagger"):
			raws = append(raws, href)
		case strings.Contains(rel, "alternate") && (strings.Contains(hl, "openapi") || strings.Contains(hl, "swagger")):
			raws = append(raws, href)
		}
	}
	reads := 0
	for _, m := range scriptRE.FindAllStringSubmatch(text, -1) {
		name := strings.ToLower(path.Base(strings.SplitN(m[1], "?", 2)[0]))
		if reads >= maxScriptReads || !(strings.Contains(name, "initializer") || strings.Contains(name, "swagger-config")) {
			continue
		}
		reads++
		if abs := resolve(at, m[1]); abs != "" {
			if b, _, ok, _ := s.get(abs); ok {
				t := string(b)
				if len(t) > 1<<20 {
					t = t[:1<<20]
				}
				collect(t)
			}
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, r := range raws {
		abs := resolve(at, html.UnescapeString(r))
		if abs == "" || seen[abs] {
			continue
		}
		if u, err := url.Parse(abs); err != nil || strings.ToLower(u.Hostname()) != s.host {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	return out
}

// resolve makes ref absolute against the page it was read from. Only http(s) results are returned.
func resolve(base *url.URL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(ref, "data:") || strings.HasPrefix(ref, "javascript:") {
		return ""
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	abs := base.ResolveReference(r)
	if abs.Scheme != "http" && abs.Scheme != "https" || abs.Host == "" {
		return ""
	}
	abs.Fragment = ""
	return abs.String()
}

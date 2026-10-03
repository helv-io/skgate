package suggest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Limits on what is read and later shown to the model.
const (
	maxFile  = 48 << 10
	maxTotal = 160 << 10
)

// File is one fetched document.
type File struct{ Name, Text string }

// Context is what the model gets to see about a source.
type Context struct {
	Kind    Kind   // git, npm or pypi (resolved)
	Name    string // package name or owner/repo
	Version string // latest or requested version, when the registry says
	Files   []File
}

// Summary lists the fetched files with their sizes, for logs.
func (c Context) Summary() string {
	var l []string
	for _, f := range c.Files {
		l = append(l, fmt.Sprintf("%s(%d)", f.Name, len(f.Text)))
	}
	return strings.Join(l, ",")
}

// Fetcher reads READMEs and manifests server-side. Redirects are never followed (so a token cannot be
// carried to another host) and link-local addresses are refused.
type Fetcher struct {
	Client       *http.Client
	NPM          string // registry base, default https://registry.npmjs.org
	PyPI         string // default https://pypi.org
	GitHubAPI    string // default https://api.github.com
	BitbucketAPI string // default https://api.bitbucket.org
}

// NewFetcher returns a Fetcher with the public registries.
func NewFetcher() *Fetcher {
	d := &net.Dialer{Timeout: 10 * time.Second, Control: func(_, addr string, _ syscall.RawConn) error {
		host, _, _ := net.SplitHostPort(addr)
		if ip := net.ParseIP(host); ip != nil && (ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()) {
			return errors.New("address not allowed")
		}
		return nil
	}}
	tr := &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second, Proxy: http.ProxyFromEnvironment}
	return &Fetcher{Client: &http.Client{Transport: tr, Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		NPM: "https://registry.npmjs.org", PyPI: "https://pypi.org", GitHubAPI: "https://api.github.com", BitbucketAPI: "https://api.bitbucket.org"}
}

// get returns the status and up to max bytes. Errors never contain the request headers.
func (f *Fetcher) get(ctx context.Context, u string, hdr map[string]string, max int) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	req.Header.Set("User-Agent", "skgate")
	resp, err := f.Client.Do(req)
	if err != nil {
		host := ""
		if pu, e := url.Parse(u); e == nil {
			host = pu.Host
		}
		return 0, nil, fmt.Errorf("cannot reach %s", host)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, int64(max)))
	return resp.StatusCode, b, nil
}

// Fetch reads the source. token is used for git hosts only and travels in request headers.
func (f *Fetcher) Fetch(ctx context.Context, s Source, token string) (Context, error) {
	switch s.Kind {
	case KindGit:
		return f.fetchGit(ctx, s, token)
	case KindNPM:
		return f.fetchNPM(ctx, s)
	case KindPyPI:
		return f.fetchPyPI(ctx, s)
	case KindPackage:
		c, err := f.fetchNPM(ctx, Source{Kind: KindNPM, Name: s.Name})
		if err == nil {
			return c, nil
		}
		if c2, err2 := f.fetchPyPI(ctx, Source{Kind: KindPyPI, Name: s.Name}); err2 == nil {
			return c2, nil
		}
		return Context{}, fmt.Errorf("%q was not found on npm or PyPI", s.Name)
	}
	return Context{}, errors.New("unsupported source")
}

func clip(b []byte, n int) string {
	if len(b) > n {
		b = b[:n]
	}
	return strings.ToValidUTF8(string(b), "")
}

type budget struct{ left int }

func (c *Context) add(b *budget, name string, text string) {
	if text = strings.TrimSpace(text); text == "" || b.left <= 0 {
		return
	}
	if len(text) > maxFile {
		text = text[:maxFile]
	}
	if len(text) > b.left {
		text = text[:b.left]
	}
	b.left -= len(text)
	c.Files = append(c.Files, File{Name: name, Text: strings.ToValidUTF8(text, "")})
}

func (f *Fetcher) fetchNPM(ctx context.Context, s Source) (Context, error) {
	ver := s.Version
	if ver == "" {
		ver = "latest"
	}
	name := strings.Replace(s.Name, "/", "%2F", 1)
	st, body, err := f.get(ctx, f.NPM+"/"+name+"/"+url.PathEscape(ver), map[string]string{"Accept": "application/json"}, 2<<20)
	if err != nil {
		return Context{}, err
	}
	if st != 200 {
		return Context{}, fmt.Errorf("npm: %s not found (HTTP %d)", s.Name, st)
	}
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return Context{}, errors.New("npm: unreadable response")
	}
	c := Context{Kind: KindNPM, Name: s.Name}
	c.Version, _ = m["version"].(string)
	readme, _ := m["readme"].(string)
	repo := ""
	switch r := m["repository"].(type) {
	case map[string]any:
		repo, _ = r["url"].(string)
	case string:
		repo = r
	}
	keep := map[string]any{}
	for _, k := range []string{"name", "version", "description", "bin", "main", "type", "engines", "mcpName", "repository"} {
		if v, ok := m[k]; ok {
			keep[k] = v
		}
	}
	if sc, ok := m["scripts"].(map[string]any); ok {
		ks := map[string]any{}
		for _, k := range []string{"start", "build", "prepare"} {
			if v, ok := sc[k]; ok {
				ks[k] = v
			}
		}
		keep["scripts"] = ks
	}
	mb, _ := json.MarshalIndent(keep, "", " ")
	b := &budget{maxTotal}
	c.add(b, "package.json (npm registry)", string(mb))
	if len(strings.TrimSpace(readme)) < 200 && repo != "" { // the registry often lacks a real README
		if rs, err := ParseSource(strings.TrimPrefix(strings.TrimSuffix(repo, ".git"), "git+")); err == nil && rs.Kind == KindGit {
			if gc, err := f.fetchGit(ctx, rs, ""); err == nil {
				for _, fl := range gc.Files {
					c.add(b, fl.Name, fl.Text)
				}
				return c, nil
			}
		}
	}
	c.add(b, "README (npm registry)", readme)
	return c, nil
}

func (f *Fetcher) fetchPyPI(ctx context.Context, s Source) (Context, error) {
	u := f.PyPI + "/pypi/" + url.PathEscape(s.Name)
	if s.Version != "" {
		u += "/" + url.PathEscape(s.Version)
	}
	st, body, err := f.get(ctx, u+"/json", map[string]string{"Accept": "application/json"}, 4<<20)
	if err != nil {
		return Context{}, err
	}
	if st != 200 {
		return Context{}, fmt.Errorf("PyPI: %s not found (HTTP %d)", s.Name, st)
	}
	var m struct {
		Info struct {
			Name, Version, Summary, Description string
			RequiresPython                      string            `json:"requires_python"`
			RequiresDist                        []string          `json:"requires_dist"`
			ProjectURLs                         map[string]string `json:"project_urls"`
		} `json:"info"`
	}
	if json.Unmarshal(body, &m) != nil || m.Info.Name == "" {
		return Context{}, errors.New("PyPI: unreadable response")
	}
	c := Context{Kind: KindPyPI, Name: s.Name, Version: m.Info.Version}
	meta := fmt.Sprintf("name: %s\nversion: %s\nsummary: %s\nrequires_python: %s\n", m.Info.Name, m.Info.Version, m.Info.Summary, m.Info.RequiresPython)
	for i, d := range m.Info.RequiresDist {
		if i >= 30 {
			break
		}
		meta += "requires: " + d + "\n"
	}
	b := &budget{maxTotal}
	c.add(b, "metadata (PyPI)", meta)
	c.add(b, "README (PyPI)", m.Info.Description)
	return c, nil
}

var readmeNames = []string{"README.md", "readme.md", "README.rst", "README.txt", "README"}
var manifestNames = []string{"package.json", "pyproject.toml", "server.json", "smithery.yaml", "requirements.txt", "Cargo.toml", "go.mod", "Dockerfile"}

// forge describes how to read raw files from a git host.
type forge struct {
	url     func(path string) string
	headers map[string]string
}

func (f *Fetcher) forgeFor(s Source, token string) forge {
	ref := s.Ref
	if ref == "" {
		ref = "HEAD"
	}
	full := func(p string) string {
		if s.Subdir != "" {
			return s.Subdir + "/" + p
		}
		return p
	}
	esc := func(p string) string { return strings.ReplaceAll(url.PathEscape(p), "%2F", "/") }
	switch {
	case s.Host == "github.com":
		h := map[string]string{"Accept": "application/vnd.github.raw+json"}
		if token != "" {
			h["Authorization"] = "Bearer " + token
		}
		return forge{func(p string) string {
			return f.GitHubAPI + "/repos/" + s.Path + "/contents/" + esc(full(p)) + "?ref=" + url.QueryEscape(ref)
		}, h}
	case strings.Contains(s.Host, "gitlab"):
		h := map[string]string{}
		if token != "" {
			h["PRIVATE-TOKEN"] = token
		}
		return forge{func(p string) string {
			return "https://" + s.Host + "/api/v4/projects/" + url.PathEscape(s.Path) + "/repository/files/" + url.PathEscape(full(p)) + "/raw?ref=" + url.QueryEscape(ref)
		}, h}
	case s.Host == "bitbucket.org":
		h := map[string]string{}
		if token != "" {
			h["Authorization"] = "Bearer " + token
		}
		return forge{func(p string) string {
			return f.BitbucketAPI + "/2.0/repositories/" + s.Path + "/src/" + url.PathEscape(ref) + "/" + esc(full(p))
		}, h}
	}
	h := map[string]string{}
	if token != "" {
		h["Authorization"] = "token " + token
	}
	return forge{func(p string) string { // Gitea, Forgejo and compatible
		return "https://" + s.Host + "/api/v1/repos/" + s.Path + "/raw/" + esc(full(p)) + "?ref=" + url.QueryEscape(ref)
	}, h}
}

func (f *Fetcher) fetchGit(ctx context.Context, s Source, token string) (Context, error) {
	fg := f.forgeFor(s, token)
	c := Context{Kind: KindGit, Name: s.Path}
	b := &budget{maxTotal}
	reached, denied := false, false
	for _, n := range readmeNames {
		st, body, err := f.get(ctx, fg.url(n), fg.headers, maxFile)
		if err != nil {
			return Context{}, err
		}
		if st == 200 {
			reached = true
			c.add(b, n, clip(body, maxFile))
			break
		}
		if st == 401 || st == 403 {
			denied = true
		}
	}
	for _, n := range manifestNames {
		st, body, err := f.get(ctx, fg.url(n), fg.headers, maxFile)
		if err != nil {
			return Context{}, err
		}
		if st == 200 {
			reached = true
			c.add(b, n, clip(body, maxFile))
		}
		if st == 401 || st == 403 {
			denied = true
		}
	}
	if !reached {
		switch {
		case token == "" && denied:
			return Context{}, errors.New("the repository needs authentication: add an access token")
		case token == "":
			return Context{}, errors.New("no README or manifest found; for a private repository add an access token")
		case denied:
			return Context{}, errors.New("the access token was refused by the host")
		}
		return Context{}, errors.New("no README or manifest found in the repository (check the URL, ref and token)")
	}
	return c, nil
}

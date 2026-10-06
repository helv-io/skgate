package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/reqlog"
	"github.com/helv-io/skgate/internal/timefmt"
	"github.com/helv-io/skgate/internal/vkeys"
)

// maxBody bounds request bodies that are buffered so a failed attempt can be retried.
const maxBody = 32 << 20

// maxList bounds a buffered model list.
const maxList = 8 << 20

// Backend is what the proxy needs from a provider.
type Backend interface {
	ID() string
	DefaultBase() string
	DefaultFallback() string
	Headers(base string) map[string]string
	Token(ctx context.Context) (string, error)
	ForceRefresh(ctx context.Context) (string, error)
}

// Proxy is the OpenAI-compatible reverse proxy for one provider. It accepts the API under /v1,
// /api/v1, /api or no prefix at all (see Normalize), rewrites model aliases and injects them into
// the model list.
type Proxy struct {
	Backend Backend   // the first provider: serves what no other provider claims
	Others  []Backend // further providers (see Add); a request goes to one of them by its model
	Set     Settings
	Keys    *vkeys.Manager
	Client  *http.Client
	Models  *ModelCache
}

// NewProxy returns a Proxy.
func NewProxy(b Backend, set Settings, k *vkeys.Manager) *Proxy {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 5 * time.Minute
	return &Proxy{Backend: b, Set: set, Keys: k, Models: NewModelCache(),
		Client: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Add registers a further provider behind the proxy. Requests reach it through an alias that points at one of its
// models, or by naming a model only it lists (see route).
func (p *Proxy) Add(b Backend) { p.Others = append(p.Others, b) }

// Pool is every provider behind the proxy, the first one first.
func (p *Proxy) Pool() []Backend { return append([]Backend{p.Backend}, p.Others...) }

// apiRoots are the first path segments of the OpenAI-style API. They are served without a prefix.
// "messages" is left out: /messages belongs to the MCP SSE bridge, use /v1/messages.
var apiRoots = []string{"chat", "completions", "models", "responses", "embeddings", "images", "audio", "videos",
	"moderations", "files", "batches", "fine_tuning", "uploads", "assistants", "threads", "vector_stores",
	"realtime", "tokenize-text", "language-models", "image-generation-models", "embedding-models", "api-key"}

// Patterns lists the mux patterns the proxy serves: /v1, /api, /api/v1 and the bare API roots.
// Nothing else is claimed, so /mcp, /admin, /authorize, /token and /.well-known stay untouched.
func Patterns() []string {
	ps := []string{"/v1", "/v1/", "/api", "/api/"}
	for _, r := range apiRoots {
		ps = append(ps, "/"+r, "/"+r+"/")
	}
	return ps
}

// Normalize strips an optional leading /api and then an optional /v1, so /chat/completions,
// /v1/chat/completions, /api/chat/completions and /api/v1/chat/completions are the same request.
// The result always starts with "/" and is joined to the provider's base URL (which carries its own
// version segment).
func Normalize(p string) string {
	for _, pre := range []string{"/api", "/v1"} {
		if p == pre {
			p = ""
		} else if strings.HasPrefix(p, pre+"/") {
			p = p[len(pre):]
		}
	}
	if p == "" {
		return "/"
	}
	return p
}

func errJSON(w http.ResponseWriter, status int, typ, msg string) {
	httputil.JSON(w, status, map[string]any{"error": map[string]any{"message": msg, "type": typ}})
}

// ServeHTTP authenticates the caller with a virtual key and proxies to the provider.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tok := httputil.BearerToken(r)
	if tok == "" {
		tok = r.Header.Get("X-API-Key")
	}
	key, ok := p.Keys.Verify(tok)
	if !ok {
		if at, late := p.Keys.ExpiredAt(tok); late {
			msg := vkeys.ExpiredMessage(at)
			log.Printf("provider %s: virtual key expired %s (key ending %s) rejected %s %s", p.Backend.ID(), timefmt.RFC3339(at), httputil.Mask(tok), r.Method, r.URL.Path)
			reqlog.Reject(r, "virtual key expired %s (key ending %s)", timefmt.RFC3339(at), httputil.Mask(tok))
			w.Header().Set("WWW-Authenticate", `Bearer realm="skgate", error="invalid_token", error_description="API key expired"`)
			httputil.JSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"message": msg, "type": "invalid_api_key", "code": "api_key_expired"}})
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="skgate"`)
		errJSON(w, http.StatusUnauthorized, "invalid_api_key", "missing or invalid skgate API key")
		return
	}
	if rej := p.Keys.Check(key); rej != nil {
		log.Printf("provider %s: key id=%d label=%q rejected %s %s: %s", p.Backend.ID(), key.ID, key.Label, r.Method, r.URL.Path, rej.Code)
		reqlog.Reject(r, "key %d: %s", key.ID, rej.Code)
		w.Header().Set("Retry-After", strconv.Itoa(rej.RetryAfterSeconds()))
		httputil.JSON(w, http.StatusTooManyRequests, map[string]any{"error": map[string]any{"message": rej.Message, "type": "rate_limit_error", "code": rej.Code}})
		return
	}
	var body []byte
	if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			errJSON(w, http.StatusRequestEntityTooLarge, "invalid_request_error", "request body too large or unreadable")
			return
		}
		body = b
	}
	rest := Normalize(r.URL.EscapedPath())
	list := r.Method == http.MethodGet && strings.TrimRight(rest, "/") == "/models"
	be, rest, body := p.route(r.Method, rest, body)
	resp, err := p.sendTo(r.Context(), be, r.Method, rest, r.URL.RawQuery, r.Header, body)
	if err != nil {
		var ae authError
		if errors.As(err, &ae) {
			errJSON(w, http.StatusServiceUnavailable, "upstream_auth_unavailable", ae.Error()+" (sign in via the skgate admin page)")
			return
		}
		errJSON(w, http.StatusBadGateway, "upstream_error", "upstream request failed")
		return
	}
	defer resp.Body.Close()
	if list && resp.StatusCode == http.StatusOK {
		if raw, err := io.ReadAll(io.LimitReader(resp.Body, maxList)); err == nil {
			if f, ok := be.(ListFilter); ok {
				raw = f.FilterModels(raw)
			}
			ids := ModelIDs(raw)
			p.Models.Set(be.ID(), ids)
			p.adoptHelper(be.ID(), ids)
			raw = prefixModelList(raw, p.Set.Prefix(be.ID()))
			resp.Body = io.NopCloser(bytes.NewReader(injectAliases(raw, p.allAliases())))
			httputil.CopyResponse(w, resp, "Content-Length", "Content-Encoding")
			return
		}
	}
	tap := tapUsage(resp)
	httputil.CopyResponse(w, resp)
	p.recordUsage(key.ID, r.Method, resp.StatusCode, tap)
}

// recordUsage adds one call to the key's totals: a request for every successful call that is not a
// read (model lists and the like), and the tokens the response reported, if any. It runs after the
// response was relayed and only touches memory (see vkeys.Manager.Record).
func (p *Proxy) recordUsage(keyID int64, method string, status int, tap *usageTap) {
	var u vkeys.Usage
	if status/100 == 2 && method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		u.Requests = 1
	}
	if t, ok := tap.Result(); ok && status/100 == 2 {
		u.PromptTokens, u.CompletionTokens, u.TotalTokens = t.Prompt, t.Completion, t.Total
	}
	p.Keys.Record(keyID, u)
}

// authError marks a failure to obtain a provider token.
type authError struct{ error }

// send forwards one request to the provider the model routes to (see route).
func (p *Proxy) send(ctx context.Context, method, rest, rawQuery string, hdr http.Header, body []byte) (*http.Response, error) {
	be, rest, body := p.route(method, rest, body)
	return p.sendTo(ctx, be, method, rest, rawQuery, hdr, body)
}

// sendTo forwards one request to be with its credential. A 401 refreshes the token and retries once (not for a
// fixed API key); a 402/403 retries once against the fallback base. A provider whose API is not OpenAI compatible
// answers the request itself (Transport).
func (p *Proxy) sendTo(ctx context.Context, be Backend, method, rest, rawQuery string, hdr http.Header, body []byte) (*http.Response, error) {
	if t, ok := be.(Transport); ok {
		return t.RoundTrip(ctx, method, rest, rawQuery, hdr, body)
	}
	access, err := be.Token(ctx)
	if err != nil {
		return nil, authError{err}
	}
	id := be.ID()
	base := p.Set.Base(be)
	fallback := p.Set.Fallback(be)
	do := func(b string) (*http.Response, error) {
		return p.do(ctx, be, method, b, rest, rawQuery, hdr, body, access)
	}
	resp, err := do(base)
	if err == nil && resp.StatusCode == http.StatusUnauthorized && !isStatic(be) {
		resp.Body.Close()
		if na, rerr := be.ForceRefresh(ctx); rerr == nil {
			access = na
		}
		resp, err = do(base)
	}
	if err == nil && (resp.StatusCode == 402 || resp.StatusCode == 403) && fallback != "" && fallback != base {
		resp.Body.Close()
		log.Printf("provider %s: %s returned %d, retrying via fallback base", id, hostOf(base), resp.StatusCode)
		resp, err = do(fallback)
	}
	return resp, err
}

func hostOf(u string) string {
	if pu, err := url.Parse(u); err == nil {
		return pu.Host
	}
	return u
}

func (p *Proxy) do(ctx context.Context, be Backend, method, base, rest, rawQuery string, hdr http.Header, body []byte, access string) (*http.Response, error) {
	target := strings.TrimRight(base, "/") + rest
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return nil, err
	}
	for k, vv := range hdr {
		switch http.CanonicalHeaderKey(k) {
		case "Authorization", "X-Api-Key", "Host", "Content-Length", "Accept-Encoding", "Connection",
			"Proxy-Connection", "Keep-Alive", "Te", "Trailer", "Transfer-Encoding", "Upgrade", "Cookie",
			"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-Ip":
			continue
		}
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	if access != "" { // a local server needs no key
		req.Header.Set("Authorization", "Bearer "+access)
	}
	for k, v := range be.Headers(base) {
		req.Header.Set(k, v)
	}
	return p.Client.Do(req)
}

// FetchModels lists the model ids of the first provider's account and refreshes the cache.
func (p *Proxy) FetchModels(ctx context.Context) ([]string, error) {
	return p.FetchModelsOf(ctx, p.Backend.ID())
}

// FetchModelsOf lists the model ids of the provider with the given id and refreshes the cache. An unknown id is an error.
func (p *Proxy) FetchModelsOf(ctx context.Context, id string) ([]string, error) {
	be, ok := p.backend(id)
	if !ok {
		return nil, errors.New("unknown provider")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	resp, err := p.sendTo(ctx, be, http.MethodGet, "/models", "", http.Header{"Accept": {"application/json"}}, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxList))
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("model list: HTTP " + strconv.Itoa(resp.StatusCode) + " " + http.StatusText(resp.StatusCode))
	}
	if f, ok := be.(ListFilter); ok {
		raw = f.FilterModels(raw)
	}
	ids := ModelIDs(raw)
	if len(ids) == 0 {
		return nil, errors.New("model list is empty or unreadable")
	}
	p.Models.Set(id, ids)
	p.adoptHelper(id, ids)
	return ids, nil
}

// adoptHelper rewrites a helper that names one of this provider's raw model ids to prefix_model.
// An alias name is left alone, and so is a model the unprefixed provider (Grok) also lists.
func (p *Proxy) adoptHelper(id string, ids []string) {
	prefix := p.Set.Prefix(id)
	if prefix == "" || p.Backend == nil {
		return
	}
	owner := p.Backend.ID()
	helper := p.Set.Model(owner)
	if helper == "" {
		return
	}
	if _, ok := Bare(prefix, helper); ok {
		return
	}
	for _, b := range p.Pool() {
		if _, ok := findAlias(p.Set.Aliases(b.ID()), helper); ok {
			return
		}
	}
	if !contains(ids, helper) {
		return
	}
	for _, b := range p.Pool() {
		if p.Set.Prefix(b.ID()) != "" {
			continue
		}
		if got, _, ok := p.Models.Get(b.ID()); ok && contains(got, helper) {
			return
		}
	}
	_ = p.Set.SetModel(owner, Expose(prefix, helper))
}

// prefixModelList rewrites each model id in an OpenAI-style list to prefix_id.
func prefixModelList(raw []byte, prefix string) []byte {
	if prefix == "" {
		return raw
	}
	var top map[string]json.RawMessage
	var data []map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil || json.Unmarshal(top["data"], &data) != nil {
		return raw
	}
	for _, d := range data {
		var id string
		if json.Unmarshal(d["id"], &id) == nil && id != "" {
			d["id"], _ = json.Marshal(Expose(prefix, id))
		}
	}
	top["data"], _ = json.Marshal(data)
	out, err := json.Marshal(top)
	if err != nil {
		return raw
	}
	return out
}

// Post sends a JSON request to a provider endpoint (for example /chat/completions) and returns the
// status and body. The provider is chosen as for a /v1 request: a "model" that names a skgate alias is resolved to
// its provider and target, so the helper may be set to an alias.
func (p *Proxy) Post(ctx context.Context, rest string, body []byte) (int, []byte, error) {
	resp, err := p.send(ctx, http.MethodPost, rest, "", http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json"}}, body)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxList))
	return resp.StatusCode, raw, err
}

// ModelIDs extracts the model ids of an OpenAI-style list ({"data":[{"id":...}]}).
func ModelIDs(raw []byte) []string {
	var l struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &l) != nil {
		return nil
	}
	var out []string
	for _, d := range l.Data {
		if d.ID != "" {
			out = append(out, d.ID)
		}
	}
	return out
}

// injectAliases puts one entry per alias at the front of a model list. An alias entry copies its
// target's entry (with the alias as id) when the target is listed. Real entries that share an alias
// name are dropped so ids stay unique. Lists in any other shape pass through.
func injectAliases(raw []byte, aliases []Alias) []byte {
	if len(aliases) == 0 {
		return raw
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return raw
	}
	var data []map[string]json.RawMessage
	if json.Unmarshal(top["data"], &data) != nil {
		return raw
	}
	idOf := func(e map[string]json.RawMessage) string {
		var s string
		_ = json.Unmarshal(e["id"], &s)
		return s
	}
	byID := map[string]map[string]json.RawMessage{}
	for _, e := range data {
		byID[idOf(e)] = e
	}
	out := make([]map[string]json.RawMessage, 0, len(data)+len(aliases))
	for _, a := range aliases {
		e := map[string]json.RawMessage{}
		if t, ok := byID[a.Target]; ok {
			for k, v := range t {
				e[k] = v
			}
		} else {
			e["object"], e["created"], e["owned_by"] = json.RawMessage(`"model"`), json.RawMessage(`0`), json.RawMessage(`"skgate"`)
		}
		e["id"], _ = json.Marshal(a.Name)
		out = append(out, e)
	}
	for _, e := range data {
		if _, dup := findAlias(aliases, idOf(e)); !dup {
			out = append(out, e)
		}
	}
	top["data"], _ = json.Marshal(out)
	nb, err := json.Marshal(top)
	if err != nil {
		return raw
	}
	return nb
}

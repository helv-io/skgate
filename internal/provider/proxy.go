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
	Backend Backend
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
		w.Header().Set("WWW-Authenticate", `Bearer realm="skgate"`)
		errJSON(w, http.StatusUnauthorized, "invalid_api_key", "missing or invalid skgate API key")
		return
	}
	if rej := p.Keys.Check(key); rej != nil {
		log.Printf("provider %s: key id=%d label=%q rejected %s %s: %s", p.Backend.ID(), key.ID, key.Label, r.Method, r.URL.Path, rej.Code)
		reqlog.Reject(r, "key %d: %s", key.ID, rej.Code)
		typ := "rate_limit_error"
		if rej.Code == "key_hard_stop" {
			typ = "insufficient_quota"
		} else {
			w.Header().Set("Retry-After", strconv.Itoa(rej.RetryAfterSeconds()))
		}
		httputil.JSON(w, http.StatusTooManyRequests, map[string]any{"error": map[string]any{"message": rej.Message, "type": typ, "code": rej.Code}})
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
	aliases := p.Set.Aliases(p.Backend.ID())
	list := r.Method == http.MethodGet && strings.TrimRight(rest, "/") == "/models"
	if len(aliases) > 0 {
		if id, ok := strings.CutPrefix(rest, "/models/"); ok && r.Method == http.MethodGet {
			if name, err := url.PathUnescape(id); err == nil {
				if a, ok := findAlias(aliases, name); ok {
					rest = "/models/" + url.PathEscape(a.Target)
				}
			}
		}
		if len(body) > 0 {
			body = rewriteModel(body, aliases)
		}
	}
	resp, err := p.send(r.Context(), r.Method, rest, r.URL.RawQuery, r.Header, body)
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
			p.Models.Set(p.Backend.ID(), ModelIDs(raw))
			if al := ModelAliases(raw); al != nil {
				p.Models.SetAliases(p.Backend.ID(), al)
			}
			resp.Body = io.NopCloser(bytes.NewReader(injectAliases(raw, aliases)))
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

// send forwards one request to the provider with its bearer token. A 401 refreshes the token and
// retries once; a 402/403 retries once against the fallback base.
func (p *Proxy) send(ctx context.Context, method, rest, rawQuery string, hdr http.Header, body []byte) (*http.Response, error) {
	access, err := p.Backend.Token(ctx)
	if err != nil {
		return nil, authError{err}
	}
	id := p.Backend.ID()
	base := p.Set.Base(p.Backend)
	fallback := p.Set.Fallback(p.Backend)
	do := func(b string) (*http.Response, error) { return p.do(ctx, method, b, rest, rawQuery, hdr, body, access) }
	resp, err := do(base)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		if na, rerr := p.Backend.ForceRefresh(ctx); rerr == nil {
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

func (p *Proxy) do(ctx context.Context, method, base, rest, rawQuery string, hdr http.Header, body []byte, access string) (*http.Response, error) {
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
	req.Header.Set("Authorization", "Bearer "+access)
	for k, v := range p.Backend.Headers(base) {
		req.Header.Set(k, v)
	}
	return p.Client.Do(req)
}

// FetchModels lists the model ids of the signed-in account and refreshes the cache.
func (p *Proxy) FetchModels(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	resp, err := p.send(ctx, http.MethodGet, "/models", "", http.Header{"Accept": {"application/json"}}, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxList))
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("model list: HTTP " + http.StatusText(resp.StatusCode))
	}
	ids := ModelIDs(raw)
	if len(ids) == 0 {
		return nil, errors.New("model list is empty or unreadable")
	}
	p.Models.Set(p.Backend.ID(), ids)
	aliases := ModelAliases(raw)
	if al, ok := p.Backend.(AliasLister); ok && aliases == nil { // best effort: the plain list is what matters
		if rich := p.fetchList(ctx, al.AliasesPath()); rich != nil {
			aliases = ModelAliases(rich)
		}
	}
	p.Models.SetAliases(p.Backend.ID(), aliases)
	return ids, nil
}

// fetchList reads one more model list; any failure gives nil.
func (p *Proxy) fetchList(ctx context.Context, rest string) []byte {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := p.send(ctx, http.MethodGet, rest, "", http.Header{"Accept": {"application/json"}}, nil)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxList))
	return raw
}

// Post sends a JSON request to a provider endpoint (for example /chat/completions) and returns the
// status and body. The caller's aliases are not applied: pass a real model id.
func (p *Proxy) Post(ctx context.Context, rest string, body []byte) (int, []byte, error) {
	resp, err := p.send(ctx, http.MethodPost, rest, "", http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json"}}, body)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxList))
	return resp.StatusCode, raw, err
}

// rewriteModel replaces a top-level "model" that names an alias with its target. Other bodies are
// returned unchanged, byte for byte.
func rewriteModel(body []byte, aliases []Alias) []byte {
	if len(body) == 0 || body[0] != '{' {
		return body
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return body
	}
	var name string
	if json.Unmarshal(m["model"], &name) != nil {
		return body
	}
	a, ok := findAlias(aliases, name)
	if !ok {
		return body
	}
	m["model"], _ = json.Marshal(a.Target)
	nb, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return nb
}

// ModelAliases extracts the provider's own aliases from a model list: every entry may carry an "aliases" array of
// names that select it. The result maps alias -> model id. An alias that is itself a model id, or that two models
// claim, is dropped, so a name always means one model. Lists without aliases give nil.
func ModelAliases(raw []byte) map[string]string {
	var l struct {
		Data []struct {
			ID      string   `json:"id"`
			Aliases []string `json:"aliases"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &l) != nil {
		return nil
	}
	ids := map[string]bool{}
	for _, d := range l.Data {
		ids[d.ID] = true
	}
	out, claimed := map[string]string{}, map[string]int{}
	for _, d := range l.Data {
		for _, a := range d.Aliases {
			a = strings.TrimSpace(a)
			if a == "" || ids[a] {
				continue
			}
			claimed[a]++
			out[a] = d.ID
		}
	}
	for a, n := range claimed {
		if n > 1 {
			delete(out, a)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// AliasLister is implemented by a provider whose plain model list leaves out the aliases but which has a richer
// list that carries them (the path is relative to the API base, like "/models").
type AliasLister interface{ AliasesPath() string }

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

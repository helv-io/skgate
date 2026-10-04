package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// Optional abilities of a Backend.
type (
	// Readier says whether the provider can take requests (signed in, or an API key saved). A backend without it is always ready.
	Readier interface{ Ready() bool }
	// Static marks a provider whose credential is a fixed API key: a 401 is final, there is nothing to refresh.
	Static interface{ StaticKey() bool }
	// ListFilter tidies the provider's model list before skgate reads or serves it (Gemini names ids "models/…").
	ListFilter interface{ FilterModels(raw []byte) []byte }
	// Transport answers an OpenAI-style request itself, for an API that is not OpenAI compatible (Anthropic).
	Transport interface {
		RoundTrip(ctx context.Context, method, rest, rawQuery string, hdr http.Header, body []byte) (*http.Response, error)
	}
)

func isReady(b Backend) bool {
	if r, ok := b.(Readier); ok {
		return r.Ready()
	}
	return true
}

func isStatic(b Backend) bool {
	s, ok := b.(Static)
	return ok && s.StaticKey()
}

// backend returns the provider with the given id.
func (p *Proxy) backend(id string) (Backend, bool) {
	for _, b := range p.Pool() {
		if b.ID() == id {
			return b, true
		}
	}
	return nil, false
}

// Has reports whether a provider with this id is behind the proxy.
func (p *Proxy) Has(id string) bool { _, ok := p.backend(id); return ok }

// IsReady reports whether the provider with this id can take requests now.
func (p *Proxy) IsReady(id string) bool {
	b, ok := p.backend(id)
	return ok && isReady(b)
}

// firstReady is the provider that serves a request which names no model: the first one that can take it (Grok when
// it is signed in), else the first provider, whose sign-in error then explains what is missing.
func (p *Proxy) firstReady() Backend {
	for _, b := range p.Pool() {
		if isReady(b) {
			return b
		}
	}
	return p.Backend
}

// AliasTarget is an alias together with the provider it points into.
type AliasTarget struct {
	Alias
	Provider string
}

// AllAliases lists the aliases of every provider, in provider order: alias = provider + model.
func (p *Proxy) AllAliases() []AliasTarget {
	var out []AliasTarget
	for _, b := range p.Pool() {
		for _, a := range p.Set.Aliases(b.ID()) {
			out = append(out, AliasTarget{a, b.ID()})
		}
	}
	return out
}

func (p *Proxy) allAliases() []Alias {
	var out []Alias
	for _, a := range p.AllAliases() {
		out = append(out, a.Alias)
	}
	return out
}

// resolve finds where a model name goes: an alias goes to the provider and model it points at; a model that only one
// ready provider lists goes to that provider (the first, when several list it); anything else to the first ready
// provider unchanged.
func (p *Proxy) resolve(name string) (Backend, string) {
	for _, b := range p.Pool() {
		if a, ok := findAlias(p.Set.Aliases(b.ID()), name); ok {
			return b, a.Target
		}
	}
	for _, b := range p.Pool() {
		if !isReady(b) {
			continue
		}
		if ids, _, ok := p.Models.Get(b.ID()); ok && contains(ids, name) {
			return b, name
		}
	}
	return p.firstReady(), name
}

// route picks the provider for a request and rewrites the model in it: the "model" of a JSON body, or the id in
// GET /models/{id}. Bodies without a model, and every other path, go to the first ready provider untouched.
func (p *Proxy) route(method, rest string, body []byte) (Backend, string, []byte) {
	if id, ok := strings.CutPrefix(rest, "/models/"); ok && method == http.MethodGet {
		if name, err := url.PathUnescape(id); err == nil {
			be, target := p.resolve(name)
			return be, "/models/" + url.PathEscape(target), body
		}
	}
	if len(body) > 0 && body[0] == '{' {
		var m map[string]json.RawMessage
		var name string
		if json.Unmarshal(body, &m) == nil && json.Unmarshal(m["model"], &name) == nil && name != "" {
			be, target := p.resolve(name)
			if target != name {
				m["model"], _ = json.Marshal(target)
				if nb, err := json.Marshal(m); err == nil {
					body = nb
				}
			}
			return be, rest, body
		}
	}
	return p.firstReady(), rest, body
}

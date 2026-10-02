package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/reqlog"
)

// The bare /mcp endpoint (and the legacy /sse root) is an aggregating MCP server. It answers
// initialize with skgate's own capabilities, and fans tools/list (and resources and prompts) out
// to every upstream flagged "Include in /mcp" that is enabled. Every name is prefixed with
// "<alias>-" (tools, prompts, resource names) so a tools/call can be routed back to the right
// upstream with that upstream's own auth, detected kind and host override. Resource URIs are
// prefixed "<alias>+" (the alias alphabet has no "+", so the split is unambiguous).
// /mcp/<alias> is unchanged: a transparent, unprefixed proxy to one upstream.

const aggMaxPages = 20 // follow at most this many nextCursor pages per upstream and list

var aggProtocols = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// JSON-RPC error codes.
const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
	rpcInternalError  = -32603
)

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func (m rpcMsg) hasID() bool { return len(m.ID) > 0 && string(m.ID) != "null" }

func rpcResult(id json.RawMessage, result any) []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	return b
}

func rpcFail(id json.RawMessage, code int, msg string) []byte {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
	return b
}

// serveAggregate handles /mcp (Streamable HTTP) after inbound auth succeeded.
func (s *Server) serveAggregate(w http.ResponseWriter, r *http.Request) {
	reqlog.Upstream(r, "(aggregate)", "", 0, "")
	switch r.Method {
	case http.MethodPost:
	case http.MethodDelete:
		// skgate keeps no per-client session for the aggregator, so ending one is a no-op.
		httputil.SetCORS(w)
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		reqlog.Reject(r, "method %s not allowed on the aggregated /mcp endpoint (POST only)", r.Method)
		w.Header().Set("Allow", "POST, DELETE, OPTIONS")
		jsonErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "the aggregated /mcp endpoint accepts POST (JSON-RPC); it has no server-initiated stream")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMCPBody))
	if err != nil {
		reqlog.Reject(r, "request body too large or unreadable")
		jsonErr(w, http.StatusRequestEntityTooLarge, "invalid_request", "request body too large or unreadable")
		return
	}
	out, ok := s.aggregateBody(r.Context(), r, body)
	httputil.SetCORS(w)
	if !ok {
		reqlog.Reject(r, "body is not a JSON-RPC message")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(rpcFail(nil, rpcParseError, "body must be a JSON-RPC 2.0 message or batch"))
		return
	}
	if out == nil { // only notifications or responses
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// aggregateBody handles one JSON-RPC message or batch. It returns the response body (nil when
// nothing is to be sent back) and false when the body is not JSON-RPC at all. r may be nil.
func (s *Server) aggregateBody(ctx context.Context, r *http.Request, body []byte) ([]byte, bool) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || !json.Valid(body) {
		return nil, false
	}
	if body[0] == '[' {
		var batch []json.RawMessage
		if json.Unmarshal(body, &batch) != nil || len(batch) == 0 {
			return nil, false
		}
		var outs []json.RawMessage
		for _, one := range batch {
			if o := s.aggregateOne(ctx, r, one); o != nil {
				outs = append(outs, o)
			}
		}
		if len(outs) == 0 {
			return nil, true
		}
		b, _ := json.Marshal(outs)
		return b, true
	}
	return s.aggregateOne(ctx, r, body), true
}

func (s *Server) aggregateOne(ctx context.Context, r *http.Request, raw []byte) []byte {
	var m rpcMsg
	if json.Unmarshal(raw, &m) != nil {
		return rpcFail(nil, rpcInvalidRequest, "invalid JSON-RPC message")
	}
	if m.Method == "" { // a response to a server request: skgate sends none
		return nil
	}
	if r != nil {
		reqlog.Note(r, "rpc=%s", clipText(m.Method, 60))
	}
	if !m.hasID() { // notification
		return nil
	}
	switch m.Method {
	case "initialize":
		return rpcResult(m.ID, s.aggInitialize(m.Params))
	case "ping":
		return rpcResult(m.ID, map[string]any{})
	case "tools/list":
		return rpcResult(m.ID, map[string]any{"tools": s.aggList(ctx, r, "tools/list", "tools", "name", "-")})
	case "prompts/list":
		return rpcResult(m.ID, map[string]any{"prompts": s.aggList(ctx, r, "prompts/list", "prompts", "name", "-")})
	case "resources/list":
		return rpcResult(m.ID, map[string]any{"resources": s.aggList(ctx, r, "resources/list", "resources", "uri", "+")})
	case "resources/templates/list":
		return rpcResult(m.ID, map[string]any{"resourceTemplates": s.aggList(ctx, r, "resources/templates/list", "resourceTemplates", "uriTemplate", "+")})
	case "tools/call":
		return s.aggRoute(ctx, r, m, "name", "-", "tool")
	case "prompts/get":
		return s.aggRoute(ctx, r, m, "name", "-", "prompt")
	case "resources/read":
		return s.aggRoute(ctx, r, m, "uri", "+", "resource")
	default:
		return rpcFail(m.ID, rpcMethodNotFound, "method not supported by the aggregated /mcp endpoint: "+clipText(m.Method, 60))
	}
}

func (s *Server) aggInitialize(params json.RawMessage) map[string]any {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)
	proto := aggProtocols[0]
	for _, v := range aggProtocols {
		if v == p.ProtocolVersion {
			proto = v
		}
	}
	return map[string]any{
		"protocolVersion": proto,
		"capabilities": map[string]any{
			"tools":     map[string]any{"listChanged": false},
			"resources": map[string]any{"listChanged": false, "subscribe": false},
			"prompts":   map[string]any{"listChanged": false},
		},
		"serverInfo":   map[string]any{"name": "skgate", "version": config.Version},
		"instructions": "skgate MCP aggregator. Every tool, prompt and resource name is prefixed with the alias of the upstream it comes from, for example <alias>-<tool_name>.",
	}
}

// aggUpstreams returns the included and enabled upstreams (ordered by alias).
func (s *Server) aggUpstreams() []Upstream {
	ups, err := s.Upstreams.Included()
	if err != nil {
		s.Log.Printf("aggregate reason=%q", "cannot read the upstream list: "+reqlog.Sanitize(err))
	}
	return ups
}

// aggList fans a *_list method out to all included upstreams and merges the arrays under key,
// prefixing the identifying field (field) with "<alias><sep>". A failing upstream is skipped and the
// reason logged, the others are still returned.
func (s *Server) aggList(ctx context.Context, r *http.Request, method, key, field, sep string) []json.RawMessage {
	ups := s.aggUpstreams()
	parts := make([][]json.RawMessage, len(ups))
	var wg sync.WaitGroup
	for i, up := range ups {
		wg.Add(1)
		go func(i int, up Upstream) {
			defer wg.Done()
			items, err := s.fetchAll(ctx, up, method, key)
			if err != nil {
				s.Log.Printf("aggregate alias=%s method=%s skipped=true reason=%q", up.Alias, method, err.Error())
				if r != nil {
					reqlog.Note(r, "skipped %s (%s): %s", up.Alias, method, err.Error())
				}
				return
			}
			for _, it := range items {
				if p, ok := prefixField(it, field, up.Alias+sep); ok {
					parts[i] = append(parts[i], p)
				}
			}
		}(i, up)
	}
	wg.Wait()
	out := []json.RawMessage{}
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// fetchAll reads every page of a list method from one upstream. An upstream that does not
// implement the method (JSON-RPC method not found) simply contributes nothing.
func (s *Server) fetchAll(ctx context.Context, up Upstream, method, key string) ([]json.RawMessage, error) {
	var all []json.RawMessage
	cursor := ""
	for page := 0; page < aggMaxPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		res, rerr, err := s.up.call(s, ctx, up, method, params, aggTimeout)
		if err != nil {
			return nil, err
		}
		if rerr != nil {
			if rerr.Code == rpcMethodNotFound {
				return all, nil
			}
			return nil, fmt.Errorf("%s: JSON-RPC error %d: %s", method, rerr.Code, clipText(rerr.Message, 200))
		}
		var pg map[string]json.RawMessage
		if json.Unmarshal(res, &pg) != nil {
			return nil, fmt.Errorf("%s: the result is not a valid list", method)
		}
		var items []json.RawMessage
		if v, ok := pg[key]; ok {
			if json.Unmarshal(v, &items) != nil {
				return nil, fmt.Errorf("%s: %q is not an array", method, key)
			}
		}
		all = append(all, items...)
		next := ""
		if v, ok := pg["nextCursor"]; ok {
			_ = json.Unmarshal(v, &next)
		}
		if next == "" || next == cursor {
			return all, nil
		}
		cursor = next
	}
	return all, nil
}

// prefixField prefixes one string field of a JSON object (other fields are kept byte for byte).
// A resource's display name is prefixed too when the identifying field is its uri.
func prefixField(item json.RawMessage, field, prefix string) (json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(item, &obj) != nil {
		return nil, false
	}
	var v string
	if json.Unmarshal(obj[field], &v) != nil || v == "" {
		return nil, false
	}
	obj[field], _ = json.Marshal(prefix + v)
	if field == "uri" || field == "uriTemplate" {
		alias := strings.TrimSuffix(prefix, "+")
		var name string
		if json.Unmarshal(obj["name"], &name) == nil && name != "" {
			obj["name"], _ = json.Marshal(alias + "-" + name)
		}
	}
	b, err := json.Marshal(obj)
	return b, err == nil
}

// splitPrefixed finds the included upstream a prefixed identifier belongs to. For "-" the longest
// matching alias wins (aliases may contain dashes); for "+" the alias is everything before the
// first "+".
func splitPrefixed(ups []Upstream, id, sep string) (Upstream, string, bool) {
	if sep == "+" {
		i := strings.Index(id, "+")
		if i <= 0 {
			return Upstream{}, "", false
		}
		for _, u := range ups {
			if u.Alias == id[:i] {
				return u, id[i+1:], true
			}
		}
		return Upstream{}, "", false
	}
	sorted := append([]Upstream(nil), ups...)
	sort.Slice(sorted, func(a, b int) bool { return len(sorted[a].Alias) > len(sorted[b].Alias) })
	for _, u := range sorted {
		if strings.HasPrefix(id, u.Alias+"-") && len(id) > len(u.Alias)+1 {
			return u, id[len(u.Alias)+1:], true
		}
	}
	return Upstream{}, "", false
}

// aggRoute handles tools/call, prompts/get and resources/read: it strips the alias prefix from
// params[field], calls the right upstream with its own auth and returns that upstream's result
// (or JSON-RPC error) unchanged, apart from re-prefixing resource URIs in the contents.
func (s *Server) aggRoute(ctx context.Context, r *http.Request, m rpcMsg, field, sep, what string) []byte {
	var params map[string]json.RawMessage
	if json.Unmarshal(m.Params, &params) != nil || params == nil {
		return rpcFail(m.ID, rpcInvalidParams, "params must be an object with a "+field)
	}
	var id string
	if json.Unmarshal(params[field], &id) != nil || id == "" {
		return rpcFail(m.ID, rpcInvalidParams, "missing "+what+" "+field)
	}
	up, orig, ok := splitPrefixed(s.aggUpstreams(), id, sep)
	if !ok {
		s.Log.Printf("aggregate method=%s reason=%q", m.Method, "unknown "+what+": no included upstream matches the prefix of "+clipText(id, 80))
		return rpcFail(m.ID, rpcInvalidParams, "unknown "+what+" "+clipText(id, 80)+": names are prefixed with the alias of an upstream included in /mcp")
	}
	params[field], _ = json.Marshal(orig)
	if r != nil {
		reqlog.Upstream(r, up.Alias, up.URL, 0, up.EffectiveKind())
	}
	res, rerr, err := s.up.call(s, ctx, up, m.Method, params, aggCallTimeout)
	if err != nil {
		s.Log.Printf("aggregate alias=%s method=%s reason=%q", up.Alias, m.Method, err.Error())
		if r != nil {
			reqlog.Reject(r, "aggregate: upstream %s failed: %s", up.Alias, err.Error())
		}
		return rpcFail(m.ID, rpcInternalError, "upstream "+up.Alias+" failed: "+err.Error())
	}
	if rerr != nil {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": rerr})
		return b
	}
	if m.Method == "resources/read" {
		res = prefixContentURIs(res, up.Alias+"+")
	}
	return rpcResult(m.ID, res)
}

func prefixContentURIs(res json.RawMessage, prefix string) json.RawMessage {
	var obj map[string]json.RawMessage
	if json.Unmarshal(res, &obj) != nil {
		return res
	}
	var contents []map[string]json.RawMessage
	if json.Unmarshal(obj["contents"], &contents) != nil {
		return res
	}
	for _, c := range contents {
		var u string
		if json.Unmarshal(c["uri"], &u) == nil && u != "" {
			c["uri"], _ = json.Marshal(prefix + u)
		}
	}
	obj["contents"], _ = json.Marshal(contents)
	b, err := json.Marshal(obj)
	if err != nil {
		return res
	}
	return b
}

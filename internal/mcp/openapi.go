package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/openapi"
	"github.com/helv-io/skgate/internal/reqlog"
)

// An OpenAPI upstream is a REST API described by an OpenAPI document. skgate itself serves it as an MCP server:
// every enabled operation is a tool, and tools/call becomes an HTTP request made by skgate (no process, so it works
// in the slim image too). The upstream row holds the base URL and the static credential; upstream_openapi holds the
// description and which operations are exposed.

// IsOpenAPI reports whether this upstream is an OpenAPI description served by skgate.
func (u Upstream) IsOpenAPI() bool { return u.Kind == KindOpenAPI }

// Tool counts above which models do worse: the admin UI colors by these and never blocks anything.
const (
	ToolsGood = 15 // up to here: fine
	ToolsWarn = 30 // up to here: a lot; above: too many
)

// ToolLevel names the level of a tool count: ok, warn or bad.
func ToolLevel(n int) string {
	switch {
	case n <= ToolsGood:
		return "ok"
	case n <= ToolsWarn:
		return "warn"
	}
	return "bad"
}

var queryNameRE = regexp.MustCompile(`^[A-Za-z0-9_.\-\[\]]{1,100}$`)

func (u *Upstream) validateOpenAPI() error {
	if !aliasRE.MatchString(u.Alias) {
		return errors.New("alias must be 1-63 chars of a-z, 0-9 and dash")
	}
	if u.Command != "" || len(u.Args) > 0 || len(u.Env) > 0 || u.GitURL != "" || u.Install != "" || u.WorkDir != "" || len(u.Headers) > 0 || u.HostOverride != "" {
		return errors.New("an OpenAPI upstream takes no process, headers or host override")
	}
	pu, err := url.Parse(u.URL)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" || pu.User != nil {
		return errors.New("the base URL must be http or https, without a user name")
	}
	if err := httputil.CheckScheme(pu); err != nil {
		return err
	}
	switch u.AuthKind {
	case "":
		u.AuthKind = AuthNone
	case AuthNone:
	case AuthAuto: // one key: the way to send it comes from the description, or is found by trying
		if u.AuthValue == "" {
			return errors.New("enter the key")
		}
	case AuthBearer:
		if u.AuthValue == "" {
			return errors.New("bearer needs a key")
		}
	case AuthHeader:
		if u.AuthValue == "" || !validHeaderName(u.AuthName) {
			return errors.New("header needs a name and a key")
		}
	case AuthQuery:
		if u.AuthValue == "" || !queryNameRE.MatchString(u.AuthName) {
			return errors.New("query needs a name and a key")
		}
	case AuthBasic:
		if u.AuthName == "" || u.AuthValue == "" || len(u.AuthName) > 200 || containsAny(u.AuthName, ":\r\n") {
			return errors.New("basic needs a user name and a password")
		}
	default:
		return errors.New("auth must be none, bearer, header, query or basic")
	}
	if u.AuthKind == AuthNone || u.AuthKind == AuthBearer || u.AuthKind == AuthAuto {
		u.AuthName = ""
	}
	u.Lifecycle = "always"
	return nil
}

func containsAny(s, chars string) bool {
	for _, c := range s {
		for _, d := range chars {
			if c == d {
				return true
			}
		}
	}
	return false
}

// OpenAPIConfig is what is stored for an OpenAPI upstream besides its row.
type OpenAPIConfig struct {
	Spec      string // the normalized description, JSON
	SpecURL   string // where it came from, if a URL (for the admin to refetch; never fetched at run time)
	FetchedAt int64  // unix time the description was last read from SpecURL (0: never, or pasted)
	SpecHash  string // SHA-256 of the text fetched then ("" when not recorded)
	Selection openapi.Selection
}

// OAState is a stored OpenAPI upstream, parsed and ready to serve.
type OAState struct {
	Config OpenAPIConfig
	Doc    *openapi.Doc
	Ops    []openapi.Op
	Tools  []openapi.Tool
	// Scheme is the way the description says a key is sent (Kind "" when it says nothing).
	Scheme openapi.Scheme
}

// OpenAPI returns the stored description of an OpenAPI upstream, parsed. The result is cached until it changes.
func (s *Upstreams) OpenAPI(alias string) (*OAState, error) {
	if v, ok := s.oaCache.Load(alias); ok {
		return v.(*OAState), nil
	}
	gen := s.oaGen.Load()
	var cfg OpenAPIConfig
	var sel string
	err := s.db.QueryRow(`SELECT spec,spec_url,selection,fetched_at,spec_hash FROM upstream_openapi WHERE alias=?`, alias).Scan(&cfg.Spec, &cfg.SpecURL, &sel, &cfg.FetchedAt, &cfg.SpecHash)
	if err != nil {
		return nil, errors.New("no OpenAPI description is stored for this upstream")
	}
	if err := json.Unmarshal([]byte(sel), &cfg.Selection); err != nil {
		return nil, errors.New("the tool selection is unreadable, save the tools page again")
	}
	st, err := buildState(cfg)
	if err != nil {
		return nil, err
	}
	if s.oaGen.Load() == gen { // nothing changed while this was read: safe to keep
		s.oaCache.Store(alias, st)
	}
	return st, nil
}

func buildState(cfg OpenAPIConfig) (*OAState, error) {
	doc, err := openapi.ParseStored([]byte(cfg.Spec))
	if err != nil {
		return nil, fmt.Errorf("the stored description is unreadable: %w", err)
	}
	ops := doc.Operations()
	return &OAState{Config: cfg, Doc: doc, Ops: ops, Tools: openapi.Tools(ops, cfg.Selection), Scheme: doc.Scheme()}, nil
}

// SetOpenAPI stores the description and selection of an OpenAPI upstream.
func (s *Upstreams) SetOpenAPI(alias string, cfg OpenAPIConfig) error {
	if _, err := buildState(cfg); err != nil {
		return err
	}
	sel, _ := json.Marshal(cfg.Selection)
	_, err := s.db.Exec(`INSERT INTO upstream_openapi(alias,spec,spec_url,selection,fetched_at,spec_hash) VALUES(?,?,?,?,?,?)
		ON CONFLICT(alias) DO UPDATE SET spec=excluded.spec,spec_url=excluded.spec_url,selection=excluded.selection,
		fetched_at=excluded.fetched_at,spec_hash=excluded.spec_hash`, alias, cfg.Spec, cfg.SpecURL, string(sel), cfg.FetchedAt, cfg.SpecHash)
	s.oaCache.Delete(alias)
	s.oaGen.Add(1)
	return err
}

// OpenAPIToolCount is how many tools an OpenAPI upstream exposes (0 when its description cannot be read).
func (s *Upstreams) OpenAPIToolCount(alias string) int {
	st, err := s.OpenAPI(alias)
	if err != nil {
		return 0
	}
	return len(st.Tools)
}

// oaAuth is the credential an OpenAPI upstream sends, given the way its description says a key is sent. A single
// key (auto) goes the way that was found to work, else the way the description names, else as a bearer token.
func (u Upstream) oaAuth(sch openapi.Scheme) openapi.Auth {
	if u.AuthKind == AuthAuto {
		if u.AuthValue == "" {
			return openapi.Auth{Kind: AuthNone}
		}
		w := openapi.Way(u.DetectedKind)
		if !w.Valid() {
			w = sch.Way()
		}
		return w.Auth(u.AuthValue)
	}
	return openapi.Auth{Kind: u.AuthKind, Name: u.AuthName, Value: u.AuthValue}
}

// caller makes the HTTP calls of an OpenAPI upstream.
func (u Upstream) caller(sch openapi.Scheme) *openapi.Caller {
	return &openapi.Caller{Base: u.URL, Auth: u.oaAuth(sch), UA: "skgate-openapi/" + config.Version}
}

func oaToolJSON(t openapi.Tool) map[string]any {
	ann := map[string]any{"title": t.Title, "readOnlyHint": t.ReadOnly, "openWorldHint": true}
	if !t.ReadOnly {
		ann["destructiveHint"] = t.Destructive
		ann["idempotentHint"] = t.Op.Method == "PUT" || t.Op.Method == "DELETE"
	}
	return map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.InputSchema, "annotations": ann}
}

// oaDispatch answers one MCP request for an OpenAPI upstream: tools/list and tools/call, nothing else.
func (s *Server) oaDispatch(ctx context.Context, up Upstream, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "tools/list":
		st, err := s.Upstreams.OpenAPI(up.Alias)
		if err != nil {
			return nil, &rpcError{Code: rpcInternalError, Message: err.Error()}
		}
		tools := make([]any, 0, len(st.Tools))
		for _, t := range st.Tools {
			tools = append(tools, oaToolJSON(t.WithoutCredential(up.caller(st.Scheme).Auth)))
		}
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if json.Unmarshal(params, &p) != nil || p.Name == "" {
			return nil, &rpcError{Code: rpcInvalidParams, Message: "params must have a tool name and an arguments object"}
		}
		st, err := s.Upstreams.OpenAPI(up.Alias)
		if err != nil {
			return nil, &rpcError{Code: rpcInternalError, Message: err.Error()}
		}
		for _, t := range st.Tools {
			if t.Name != p.Name {
				continue
			}
			if up.SecretErr {
				return toolText("the stored key cannot be decrypted, set it again", true), nil
			}
			cl := up.caller(st.Scheme)
			res := cl.Call(ctx, t.WithoutCredential(cl.Auth), p.Arguments)
			if openapi.Refused(res.Status) && s.findKeyWay(ctx, &up, st) { // first use: the usual ways are tried, once
				if t.Op.Method == http.MethodGet || t.Op.Method == http.MethodHead {
					cl = up.caller(st.Scheme)
					res = cl.Call(ctx, t.WithoutCredential(cl.Auth), p.Arguments)
				}
			}
			return toolText(res.Text, res.IsError), nil
		}
		return nil, &rpcError{Code: rpcInvalidParams, Message: "unknown tool " + clipText(p.Name, 80)}
	case "prompts/list":
		return map[string]any{"prompts": []any{}}, nil
	case "resources/list":
		return map[string]any{"resources": []any{}}, nil
	case "resources/templates/list":
		return map[string]any{"resourceTemplates": []any{}}, nil
	case "ping":
		return map[string]any{}, nil
	}
	return nil, &rpcError{Code: rpcMethodNotFound, Message: "method not supported: " + clipText(method, 60)}
}

func toolText(text string, isErr bool) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": isErr}
}

// callOpenAPI is upClient.call for an OpenAPI upstream (used by the aggregated /mcp).
func (c *upClient) callOpenAPI(s *Server, ctx context.Context, up Upstream, method string, params any) (json.RawMessage, *rpcError, error) {
	raw, _ := json.Marshal(params)
	res, rerr := s.oaDispatch(ctx, up, method, raw)
	if rerr != nil {
		return nil, rerr, nil
	}
	b, _ := json.Marshal(res)
	return b, nil, nil
}

const maxOAEnvelope = 4 << 20

// serveOpenAPI answers /mcp/{alias} for an OpenAPI upstream. Streamable HTTP without sessions or streams: every
// POST is answered at once.
func (s *Server) serveOpenAPI(w http.ResponseWriter, r *http.Request, up Upstream) {
	reqlog.Upstream(r, up.Alias, "", 0, "openapi")
	httputil.SetCORS(w)
	switch r.Method {
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
		return
	case http.MethodPost:
	default:
		w.Header().Set("Allow", "POST, DELETE, OPTIONS")
		jsonErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "this server answers POST requests; it has no event stream")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxOAEnvelope))
	if err != nil {
		jsonErr(w, http.StatusRequestEntityTooLarge, "invalid_request", "request body too large or unreadable")
		return
	}
	out, ok := s.oaBody(r.Context(), r, up, body)
	if !ok {
		jsonErr(w, http.StatusBadRequest, "invalid_request", "the body is not a JSON-RPC message")
		return
	}
	if out == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

func (s *Server) oaBody(ctx context.Context, r *http.Request, up Upstream, body []byte) ([]byte, bool) {
	if !json.Valid(body) || len(body) == 0 {
		return nil, false
	}
	if body[0] == '[' {
		var batch []json.RawMessage
		if json.Unmarshal(body, &batch) != nil || len(batch) == 0 {
			return nil, false
		}
		var outs []json.RawMessage
		for _, one := range batch {
			if o := s.oaOne(ctx, r, up, one); o != nil {
				outs = append(outs, o)
			}
		}
		if len(outs) == 0 {
			return nil, true
		}
		b, _ := json.Marshal(outs)
		return b, true
	}
	return s.oaOne(ctx, r, up, body), true
}

func (s *Server) oaOne(ctx context.Context, r *http.Request, up Upstream, raw []byte) []byte {
	var m rpcMsg
	if json.Unmarshal(raw, &m) != nil {
		return rpcFail(nil, rpcInvalidRequest, "invalid JSON-RPC message")
	}
	if m.Method == "" || !m.hasID() { // a response, or a notification
		return nil
	}
	if r != nil {
		reqlog.Note(r, "rpc=%s", clipText(m.Method, 60))
	}
	if m.Method == "initialize" {
		return rpcResult(m.ID, s.oaInitialize(up, m.Params))
	}
	res, rerr := s.oaDispatch(ctx, up, m.Method, m.Params)
	if rerr != nil {
		return rpcFail(m.ID, rerr.Code, rerr.Message)
	}
	return rpcResult(m.ID, res)
}

func (s *Server) oaInitialize(up Upstream, params json.RawMessage) map[string]any {
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
	title := up.Alias
	if st, err := s.Upstreams.OpenAPI(up.Alias); err == nil && st.Doc.Title() != "" {
		title = st.Doc.Title()
	}
	return map[string]any{
		"protocolVersion": proto,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		"serverInfo":      map[string]any{"name": clipText(title, 80), "version": config.Version},
	}
}

// testOpenAPI is Test for an OpenAPI upstream: the description must be readable and the base URL well formed.
// Nothing is requested: an operation could change data, and a bare request could not tell a wrong credential apart.
func (s *Server) testOpenAPI(ctx context.Context, up Upstream) (tr TestResult) {
	st, err := s.Upstreams.OpenAPI(up.Alias)
	if err != nil {
		tr.AuthMode, tr.Auth = up.AuthKind, up.AuthKind
		tr.Error = err.Error()
		return tr
	}
	tr = s.testOpenAPIState(ctx, up, st)
	if tr.Way != "" && tr.Way != up.DetectedKind && up.AuthKind == AuthAuto {
		_ = s.Upstreams.SetDetected(up.Alias, tr.Way, "")
	}
	return tr
}

// TestOpenAPIDraft tests an OpenAPI upstream that is not stored yet: its description and tool choice are cfg.
func (s *Server) TestOpenAPIDraft(ctx context.Context, up Upstream, cfg OpenAPIConfig) TestResult {
	st, err := buildState(cfg)
	if err != nil {
		return TestResult{Error: err.Error()}
	}
	return s.testOpenAPIState(ctx, up, st)
}

// QueryKeyWarning is shown when the key travels in the web address.
const QueryKeyWarning = "the key travels in the web address and can show up in logs"

func (s *Server) testOpenAPIState(ctx context.Context, up Upstream, st *OAState) (tr TestResult) {
	tr.AuthMode = up.AuthKind
	if up.AuthKind != AuthAuto {
		tr.Auth = up.AuthKind
	}
	tr.ToolTotal = len(st.Tools)
	for i, t := range st.Tools {
		if i >= maxTestTools {
			tr.ToolsCapped = true
			break
		}
		tr.Tools = append(tr.Tools, ToolInfo{Name: clipText(t.Name, 100), Desc: firstLine(t.Description), More: restAfterFirstLine(t.Description)})
	}
	tr.Server = clipText(st.Doc.Title(), 80)
	if n := len(st.Tools); n > ToolsWarn {
		tr.Warnings = append(tr.Warnings, fmt.Sprintf("%d tools is too many", n))
	}
	if n := len(st.Tools); n == 0 {
		tr.Warnings = append(tr.Warnings, "no tools on")
	}
	if up.SecretErr {
		tr.Error = "the stored key cannot be read, enter it again"
		return tr
	}
	if op, ok := st.Doc.ProbeOp(st.Ops); ok { // one safe read tells whether the server takes the key
		ua := "skgate-openapi/" + config.Version
		auth := up.oaAuth(st.Scheme)
		status := 0
		switch {
		case up.AuthKind == AuthAuto:
			ways := openapi.Ways(st.Scheme, up.AuthValue)
			if d := openapi.Way(up.DetectedKind); d.Valid() {
				ways = append([]openapi.Way{d}, ways...)
			}
			got := openapi.TryKey(ctx, up.URL, op, up.AuthValue, ways, ua)
			status = got.Status
			if got.OK {
				tr.Way = string(got.Way)
				auth = got.Way.Auth(up.AuthValue)
			}
		default:
			status = openapi.TryAuth(ctx, up.URL, op, auth, ua)
		}
		switch {
		case status == 0:
			tr.Error = "cannot reach the server"
			return tr
		case openapi.Refused(status) && auth.Kind != AuthNone:
			tr.Error = "the server refused the key"
			return tr
		case openapi.Refused(status):
			tr.Warnings = append(tr.Warnings, "the server wants a key")
		}
		if auth.Kind == AuthQuery {
			tr.Warnings = append(tr.Warnings, QueryKeyWarning)
		}
	}
	tr.OK = true
	return tr
}

// findKeyWay tries the usual ways to send the key of up after the server refused it, when the description says
// nothing about it and nothing was found before. It stores the way that works and updates up. At most once a minute
// per upstream.
func (s *Server) findKeyWay(ctx context.Context, up *Upstream, st *OAState) bool {
	if up.AuthKind != AuthAuto || up.DetectedKind != "" || st.Scheme.Kind != "" || up.AuthValue == "" || !s.Upstreams.allowKeyProbe(up.Alias) {
		return false
	}
	op, ok := st.Doc.ProbeOp(st.Ops)
	if !ok {
		return false
	}
	got := openapi.TryKey(ctx, up.URL, op, up.AuthValue, openapi.Ways(st.Scheme, up.AuthValue), "skgate-openapi/"+config.Version)
	if !got.OK {
		return false
	}
	up.DetectedKind = string(got.Way)
	_ = s.Upstreams.SetDetected(up.Alias, up.DetectedKind, "")
	return true
}

// allowKeyProbe lets one search for the way to send a key run per minute and upstream.
func (s *Upstreams) allowKeyProbe(alias string) bool {
	now := time.Now()
	if v, ok := s.keyTried.Load(alias); ok && now.Sub(v.(time.Time)) < time.Minute {
		return false
	}
	s.keyTried.Store(alias, now)
	return true
}

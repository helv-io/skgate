package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/reqlog"
)

// Legacy HTTP+SSE transport (protocol 2024-11-05) bridged onto a Streamable HTTP upstream:
//
//	GET  /sse[/{alias}]        opens the event stream; first event is "endpoint" with the POST URL
//	POST /messages?sessionId=  forwards the JSON-RPC message upstream, and the upstream reply is
//	                           pushed onto the event stream; the POST itself returns 202.
//
// Upstreams that only speak legacy SSE are not supported.
type sseSession struct {
	id       string
	up       Upstream
	agg      bool // bare /sse: served by the aggregator instead of one upstream
	events   chan []byte
	mu       sync.Mutex // serializes upstream requests until response headers arrive
	upstream string     // upstream Mcp-Session-Id
	done     chan struct{}
	once     sync.Once
}

func (ss *sseSession) close() { ss.once.Do(func() { close(ss.done) }) }

func (ss *sseSession) push(data []byte) {
	var buf bytes.Buffer
	if json.Compact(&buf, data) != nil {
		return
	}
	select {
	case ss.events <- buf.Bytes():
	case <-ss.done:
	}
}

func (s *Server) serveSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		httputil.SetCORS(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		reqlog.Reject(r, "method %s not allowed on /sse (GET only)", r.Method)
		jsonErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	alias := strings.Trim(strings.TrimPrefix(r.URL.Path, "/sse"), "/")
	if strings.Contains(alias, "/") {
		reqlog.Reject(r, "unknown path")
		jsonErr(w, http.StatusNotFound, "not_found", "unknown path")
		return
	}
	authPath := "/sse"
	if alias != "" {
		authPath += "/" + alias
	}
	if ok, presented := s.authenticate(r, authPath); !ok {
		s.unauthorized(w, r, authPath, presented) // this path's own protected-resource document
		return
	}
	var up Upstream
	if alias == "" {
		reqlog.Upstream(r, "(aggregate)", "", 0, "")
	} else {
		var msg string
		var status int
		up, msg, status = s.resolve(alias)
		if status != 0 {
			reqlog.Reject(r, "unknown upstream: %s (alias %q)", msg, alias)
			jsonErr(w, status, "not_found", msg)
			return
		}
		if up.IsOpenAPI() {
			jsonErr(w, http.StatusNotFound, "not_found", "an OpenAPI upstream is served on /mcp/"+alias+" only (Streamable HTTP)")
			return
		}
		if up.Managed() {
			reqlog.Upstream(r, up.Alias, "", 0, "managed")
		} else {
			up = s.ensureDetected(r.Context(), up)
			reqlog.Upstream(r, up.Alias, up.URL, 0, up.EffectiveKind())
		}
	}
	rc := http.NewResponseController(w)
	ss := &sseSession{id: httputil.RandString(32), up: up, agg: alias == "", events: make(chan []byte, 64), done: make(chan struct{})}
	s.sessMu.Lock()
	if len(s.sessions) >= 256 {
		s.sessMu.Unlock()
		reqlog.Reject(r, "too many open SSE sessions")
		jsonErr(w, http.StatusServiceUnavailable, "busy", "too many open SSE sessions")
		return
	}
	s.sessions[ss.id] = ss
	s.sessMu.Unlock()
	defer func() {
		ss.close()
		s.sessMu.Lock()
		delete(s.sessions, ss.id)
		s.sessMu.Unlock()
	}()

	httputil.SetCORS(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	endpoint := "/messages?sessionId=" + url.QueryEscape(ss.id)
	if q := r.URL.Query().Get("key"); q != "" {
		if key, ok := s.Keys.Verify(q); ok && key.URLKey { // only a key that may be in the URL is passed on in it
			endpoint += "&key=" + url.QueryEscape(q)
		}
	}
	_, _ = io.WriteString(w, "event: endpoint\ndata: "+endpoint+"\n\n")
	_ = rc.Flush()
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ss.events:
			if _, err := io.WriteString(w, "event: message\ndata: "+string(ev)+"\n\n"); err != nil {
				return
			}
			_ = rc.Flush()
		case <-tick.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			_ = rc.Flush()
		}
	}
}

func (s *Server) serveMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		httputil.SetCORS(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		reqlog.Reject(r, "method %s not allowed on /messages (POST only)", r.Method)
		jsonErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	if ok, presented := s.authenticate(r, "/messages"); !ok {
		s.unauthorized(w, r, "/sse", presented) // the POST half of /sse
		return
	}
	s.sessMu.Lock()
	ss := s.sessions[r.URL.Query().Get("sessionId")]
	s.sessMu.Unlock()
	if ss == nil {
		reqlog.Reject(r, "unknown or closed SSE session")
		jsonErr(w, http.StatusNotFound, "unknown_session", "unknown or closed SSE session")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMCPBody))
	if err != nil || !json.Valid(body) {
		reqlog.Reject(r, "body must be one JSON-RPC message")
		jsonErr(w, http.StatusBadRequest, "invalid_request", "body must be one JSON-RPC message")
		return
	}
	httputil.SetCORS(w)
	w.WriteHeader(http.StatusAccepted)
	_, _ = io.WriteString(w, "Accepted")
	go s.bridge(ss, r, body)
}

func rpcID(body []byte) json.RawMessage {
	var m struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(body, &m) == nil && len(m.ID) > 0 && string(m.ID) != "null" {
		return m.ID
	}
	return nil
}

func (s *Server) bridge(ss *sseSession, in *http.Request, body []byte) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-ss.done:
			cancel()
		case <-ctx.Done():
		}
	}()
	fail := func(msg string) {
		if id := rpcID(body); id != nil {
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32603, "message": msg}})
			ss.push(b)
		}
	}
	if ss.agg {
		if out, ok := s.aggregateBody(ctx, nil, body); !ok {
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": rpcParseError, "message": "body must be a JSON-RPC 2.0 message"}})
			ss.push(b)
		} else if out != nil {
			ss.push(out)
		}
		return
	}
	if ss.up.Managed() {
		s.bridgeManaged(ctx, ss, body, fail)
		return
	}
	hdr := http.Header{}
	hdr.Set("Content-Type", "application/json")
	hdr.Set("Accept", "application/json, text/event-stream")
	for _, h := range []string{"Mcp-Protocol-Version", "User-Agent"} {
		if v := in.Header.Get(h); v != "" {
			hdr.Set(h, v)
		}
	}
	ss.mu.Lock()
	if ss.upstream != "" {
		hdr.Set("Mcp-Session-Id", ss.upstream)
	}
	req := in.Clone(ctx)
	resp, err := s.forward(req, ss.up, http.MethodPost, body, hdr)
	if err == nil {
		if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
			ss.upstream = sid
		}
	}
	ss.mu.Unlock()
	if err != nil {
		s.Log.Printf("sse_bridge alias=%s reason=%q", ss.up.Alias, "upstream unreachable: "+reqlog.Sanitize(err))
		fail("MCP upstream request failed")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		return
	}
	if resp.StatusCode/100 != 2 {
		s.Log.Printf("sse_bridge alias=%s upstream_status=%d reason=%q", ss.up.Alias, resp.StatusCode, "upstream error: HTTP "+http.StatusText(resp.StatusCode))
		fail("MCP upstream returned HTTP " + http.StatusText(resp.StatusCode))
		return
	}
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64*1024), maxMCPBody)
		var data []string
		flush := func() {
			if len(data) > 0 {
				ss.push([]byte(strings.Join(data, "\n")))
				data = nil
			}
		}
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				flush()
			case strings.HasPrefix(line, "data:"):
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
		flush()
		return
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxMCPBody))
	if err == nil && len(bytes.TrimSpace(b)) > 0 {
		ss.push(b)
	}
}

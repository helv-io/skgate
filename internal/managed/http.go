package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
)

// MaxBody caps one request body.
const MaxBody = 16 << 20

const keepAlive = 15 * time.Second

// Fail writes an error response; the embedding server supplies it so errors look like the rest of
// its API.
type Fail func(w http.ResponseWriter, status int, code, desc string)

func acceptsSSE(r *http.Request) (sse, json bool) {
	for _, h := range r.Header.Values("Accept") {
		for _, part := range strings.Split(h, ",") {
			mt, _, _ := mime.ParseMediaType(strings.TrimSpace(part))
			switch mt {
			case "text/event-stream":
				sse = true
			case "application/json", "*/*", "application/*":
				json = true
			}
		}
	}
	return
}

// ServeHTTP implements the Streamable HTTP transport (POST, GET, DELETE) on top of the process.
// The caller has authenticated the request and set CORS headers.
func (p *Proc) ServeHTTP(w http.ResponseWriter, r *http.Request, fail Fail) {
	switch r.Method {
	case http.MethodPost:
		p.servePost(w, r, fail)
	case http.MethodGet:
		p.serveGet(w, r, fail)
	case http.MethodDelete:
		id := r.Header.Get("Mcp-Session-Id")
		if id == "" {
			fail(w, http.StatusBadRequest, "invalid_request", "missing Mcp-Session-Id")
			return
		}
		if !p.EndSession(id) {
			fail(w, http.StatusNotFound, "not_found", "unknown session")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, POST, DELETE, OPTIONS")
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST, GET or DELETE")
	}
}

func (p *Proc) unavailable(w http.ResponseWriter, err error, fail Fail) {
	fail(w, http.StatusBadGateway, "upstream_error", "managed process unavailable: "+clipText(err.Error(), 300))
}

func (p *Proc) servePost(w http.ResponseWriter, r *http.Request, fail Fail) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBody))
	if err != nil {
		fail(w, http.StatusRequestEntityTooLarge, "invalid_request", "request body too large or unreadable")
		return
	}
	body = bytes.TrimSpace(body)
	var msgs []rpcMsg
	batch := len(body) > 0 && body[0] == '['
	if batch {
		if json.Unmarshal(body, &msgs) != nil || len(msgs) == 0 {
			fail(w, http.StatusBadRequest, "invalid_request", "invalid JSON-RPC batch")
			return
		}
	} else {
		var m rpcMsg
		if json.Unmarshal(body, &m) != nil || m.kind() == 0 {
			writeJSON(w, http.StatusBadRequest, errorResponse(nil, -32600, "invalid JSON-RPC message"))
			return
		}
		msgs = []rpcMsg{m}
	}
	sid := r.Header.Get("Mcp-Session-Id")
	if !batch && msgs[0].kind() == 'q' && msgs[0].Method == "initialize" {
		p.serveInitialize(w, r, msgs[0], fail)
		return
	}
	var sess *Session
	if sid != "" {
		if sess = p.session(sid); sess == nil {
			fail(w, http.StatusNotFound, "not_found", "unknown or expired session")
			return
		}
	} else {
		sess = &Session{ID: "", internal: false, caps: map[string]bool{}} // sessionless client
	}
	p.Touch()
	hasReq := false
	for _, m := range msgs {
		switch m.kind() {
		case 'q':
			hasReq = true
		case 'r':
			p.answerServerRequest(sess, m)
		case 'n':
			switch m.Method {
			case "notifications/initialized":
			case "notifications/cancelled":
				p.clientCancel(sess, m.Params)
			default:
				p.forwardNotification(m)
			}
		}
	}
	if !hasReq {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if err := p.Ensure(r.Context(), startHint{}); err != nil {
		if r.Context().Err() == nil {
			p.unavailable(w, err, fail)
		}
		return
	}
	sseOK, jsonOK := acceptsSSE(r)
	stream := sseOK && (!jsonOK || (!batch && p.wantsStream(sess, msgs[0])))
	if batch {
		p.serveBatch(w, r, sess, msgs, stream, fail)
		return
	}
	p.serveOne(w, r, sess, msgs[0], stream, fail)
}

// wantsStream: answer as an event stream when the request can produce messages besides the response.
func (p *Proc) wantsStream(s *Session, m rpcMsg) bool {
	if s.caps["roots"] || s.caps["sampling"] || s.caps["elicitation"] {
		return true
	}
	if len(m.Params) == 0 {
		return false
	}
	var top struct {
		Meta struct {
			Token json.RawMessage `json:"progressToken"`
		} `json:"_meta"`
	}
	return json.Unmarshal(m.Params, &top) == nil && len(top.Meta.Token) > 0
}

func (p *Proc) forwardNotification(m rpcMsg) {
	p.mu.Lock()
	c := p.cur
	p.mu.Unlock()
	if c != nil {
		_ = c.send(mustJSON(rpcMsg{JSONRPC: "2.0", Method: m.Method, Params: m.Params}))
	}
}

func writeJSON(w http.ResponseWriter, status int, b []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func (p *Proc) serveInitialize(w http.ResponseWriter, r *http.Request, m rpcMsg, fail Fail) {
	hello := parseHello(m.Params)
	if err := p.Ensure(r.Context(), startHint{caps: hello.caps, proto: hello.proto}); err != nil {
		if r.Context().Err() == nil {
			p.unavailable(w, err, fail)
		}
		return
	}
	res := p.InitResult()
	if len(res) == 0 {
		fail(w, http.StatusBadGateway, "upstream_error", "managed process is not ready")
		return
	}
	s := p.newSession(hello)
	w.Header().Set("Mcp-Session-Id", s.ID)
	out := mustJSON(rpcMsg{JSONRPC: "2.0", ID: m.ID, Result: res})
	if sseOK, jsonOK := acceptsSSE(r); sseOK && !jsonOK {
		startSSE(w)
		writeEvent(w, out)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (p *Proc) serveOne(w http.ResponseWriter, r *http.Request, s *Session, m rpcMsg, stream bool, fail Fail) {
	if m.Method == "ping" {
		out := mustJSON(rpcMsg{JSONRPC: "2.0", ID: m.ID, Result: json.RawMessage("{}")})
		if stream {
			startSSE(w)
			writeEvent(w, out)
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	pc, err := p.startCall(s, m, stream)
	if err != nil {
		p.unavailable(w, err, fail)
		return
	}
	if !stream {
		select {
		case raw := <-pc.done:
			writeJSON(w, http.StatusOK, relayResponse(raw, m.ID))
		case <-r.Context().Done():
			p.abandon(pc, "client disconnected")
		}
		return
	}
	startSSE(w)
	tick := time.NewTicker(keepAlive)
	defer tick.Stop()
	for {
		select {
		case ev := <-pc.events:
			writeEvent(w, ev)
		case raw := <-pc.done:
			for drained := false; !drained; { // events queued before the response go first
				select {
				case ev := <-pc.events:
					writeEvent(w, ev)
				default:
					drained = true
				}
			}
			writeEvent(w, relayResponse(raw, m.ID))
			return
		case <-tick.C:
			writeComment(w, "keepalive")
		case <-r.Context().Done():
			p.abandon(pc, "client disconnected")
			return
		}
	}
}

func (p *Proc) serveBatch(w http.ResponseWriter, r *http.Request, s *Session, msgs []rpcMsg, stream bool, fail Fail) {
	var mu sync.Mutex
	var out []json.RawMessage
	var wg sync.WaitGroup
	ctx := r.Context()
	for _, m := range msgs {
		if m.kind() != 'q' {
			continue
		}
		m := m
		wg.Add(1)
		go func() {
			defer wg.Done()
			var resp []byte
			if m.Method == "ping" {
				resp = mustJSON(rpcMsg{JSONRPC: "2.0", ID: m.ID, Result: json.RawMessage("{}")})
			} else if pc, err := p.startCall(s, m, false); err != nil {
				resp = errorResponse(m.ID, -32603, err.Error())
			} else {
				select {
				case raw := <-pc.done:
					resp = relayResponse(raw, m.ID)
				case <-ctx.Done():
					p.abandon(pc, "client disconnected")
					return
				}
			}
			mu.Lock()
			out = append(out, resp)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return
	}
	b := mustJSON(out)
	if stream {
		startSSE(w)
		writeEvent(w, b)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (p *Proc) serveGet(w http.ResponseWriter, r *http.Request, fail Fail) {
	if sseOK, _ := acceptsSSE(r); !sseOK {
		fail(w, http.StatusNotAcceptable, "not_acceptable", "Accept: text/event-stream is required")
		return
	}
	id := r.Header.Get("Mcp-Session-Id")
	if id == "" {
		fail(w, http.StatusBadRequest, "invalid_request", "missing Mcp-Session-Id")
		return
	}
	s := p.session(id)
	if s == nil {
		fail(w, http.StatusNotFound, "not_found", "unknown or expired session")
		return
	}
	ch, release := p.openStream(s)
	defer release()
	startSSE(w)
	writeComment(w, "ok")
	tick := time.NewTicker(keepAlive)
	defer tick.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			writeEvent(w, ev)
		case <-tick.C:
			writeComment(w, "keepalive")
			p.session(id) // keeps the session alive while the stream is open
		case <-r.Context().Done():
			return
		}
	}
}

func startSSE(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flush(w)
}

func writeEvent(w http.ResponseWriter, data []byte) {
	_, _ = w.Write([]byte("event: message\ndata: "))
	_, _ = w.Write(data)
	_, _ = w.Write([]byte("\n\n"))
	flush(w)
}

func writeComment(w http.ResponseWriter, s string) {
	_, _ = w.Write([]byte(": " + s + "\n\n"))
	flush(w)
}

func flush(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// Ready waits until the process has been started and initialized (used by Test).
func (p *Proc) Ready(ctx context.Context) error { return p.Ensure(ctx, startHint{}) }

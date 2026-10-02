package managed

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

// latestProtocol is offered to a child when the first client did not name a version.
const latestProtocol = "2025-06-18"

// exitedMessage is the JSON-RPC error text used for calls that were in flight when the process ended.
const exitedMessage = "the managed process exited"

const (
	sessionTTL     = time.Hour
	serverReqTTL   = 2 * time.Minute
	streamBuffer   = 256
	serverReqIDPfx = "skgate-s"
	progressPfx    = "skgate-p"
)

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return "JSON-RPC error " + strconv.Itoa(e.Code) + ": " + e.Message }

// rpcMsg is any JSON-RPC message; which fields are set tells the kind.
type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

func (m rpcMsg) hasID() bool { return len(m.ID) > 0 && string(m.ID) != "null" }

// kind: 'q' request, 'n' notification, 'r' response, 0 invalid.
func (m rpcMsg) kind() byte {
	switch {
	case m.Method != "" && m.hasID():
		return 'q'
	case m.Method != "":
		return 'n'
	case m.hasID() && (len(m.Result) > 0 || m.Error != nil):
		return 'r'
	}
	return 0
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func errorResponse(id json.RawMessage, code int, text string) []byte {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return mustJSON(rpcMsg{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: text}})
}

// pendingCall is a request that was sent to the child and awaits its response.
type pendingCall struct {
	childID      int64
	sess         *Session
	clientID     json.RawMessage
	done         chan []byte // the child's raw response (or a synthesized error)
	events       chan []byte // notifications/requests tied to this call, when the client streams
	token        string      // mapped progress token (JSON), "" if none
	origToken    json.RawMessage
	internalCall bool
}

// serverReq is a request from the child that was forwarded to a client and awaits its answer.
type serverReq struct {
	sess   *Session
	origID json.RawMessage
	timer  *time.Timer
}

// Session is one client's view of the shared process (one Mcp-Session-Id).
type Session struct {
	ID       string
	Created  time.Time
	proto    string
	caps     map[string]bool
	internal bool

	// guarded by Proc.mu
	lastSeen time.Time
	stream   chan []byte // the open GET stream, if any
	gen      int
}

func (p *Proc) newPending(s *Session, clientID json.RawMessage, stream, internal bool) *pendingCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextID++
	pc := &pendingCall{childID: p.nextID, sess: s, clientID: clientID, done: make(chan []byte, 1), internalCall: internal}
	if stream {
		pc.events = make(chan []byte, streamBuffer)
	}
	p.pending[pc.childID] = pc
	return pc
}

func (p *Proc) dropPending(pc *pendingCall) {
	p.mu.Lock()
	delete(p.pending, pc.childID)
	if pc.token != "" {
		delete(p.tokens, pc.token)
	}
	p.mu.Unlock()
}

// failPending answers every in-flight call with an error and forgets server requests. Called when
// the child is gone.
func (p *Proc) failPending(msg string) {
	p.mu.Lock()
	pcs := make([]*pendingCall, 0, len(p.pending))
	for _, pc := range p.pending {
		pcs = append(pcs, pc)
	}
	p.pending = map[int64]*pendingCall{}
	p.tokens = map[string]*pendingCall{}
	for k, sr := range p.srvReqs {
		sr.timer.Stop()
		delete(p.srvReqs, k)
	}
	p.mu.Unlock()
	for _, pc := range pcs {
		select {
		case pc.done <- errorResponse(json.RawMessage(strconv.FormatInt(pc.childID, 10)), -32603, msg):
		default:
		}
	}
}

// ---- child -> skgate ----

func (p *Proc) onLine(c *child, line []byte) {
	if line[0] != '{' {
		p.junk(line)
		return
	}
	var m rpcMsg
	if json.Unmarshal(line, &m) != nil {
		p.junk(line)
		return
	}
	switch m.kind() {
	case 'r':
		p.onResponse(m, line)
	case 'n':
		p.onNotification(m)
	case 'q':
		p.onServerRequest(c, m)
	default:
		p.junk(line)
	}
}

func (p *Proc) junk(line []byte) {
	s := p.redactLine(string(line))
	p.ring.Add("out", s)
	p.m.o.Logf("managed[%s] stdout: %s", p.alias, clipLine(s))
}

func (p *Proc) onResponse(m rpcMsg, raw []byte) {
	id, err := strconv.ParseInt(string(m.ID), 10, 64)
	if err != nil {
		return
	}
	p.mu.Lock()
	pc := p.pending[id]
	delete(p.pending, id)
	if pc != nil && pc.token != "" {
		delete(p.tokens, pc.token)
	}
	p.mu.Unlock()
	if pc == nil {
		return // cancelled or unknown
	}
	pc.done <- raw
}

func (p *Proc) onNotification(m rpcMsg) {
	if m.Method == "notifications/progress" {
		var params map[string]json.RawMessage
		if json.Unmarshal(m.Params, &params) != nil {
			return
		}
		tok := compact(params["progressToken"])
		p.mu.Lock()
		pc := p.tokens[tok]
		p.mu.Unlock()
		if pc == nil {
			return
		}
		params["progressToken"] = pc.origToken
		out := mustJSON(rpcMsg{JSONRPC: "2.0", Method: m.Method, Params: mustJSON(params)})
		p.deliver(pc.sess, pc, out)
		return
	}
	if m.Method == "notifications/cancelled" {
		return
	}
	out := mustJSON(rpcMsg{JSONRPC: "2.0", Method: m.Method, Params: m.Params})
	p.broadcast(out)
}

func compact(b json.RawMessage) string {
	var buf bytes.Buffer
	if json.Compact(&buf, b) != nil {
		return string(b)
	}
	return buf.String()
}

// push queues a message on ch without blocking; a full queue drops the message.
func push(ch chan []byte, b []byte) bool {
	select {
	case ch <- b:
		return true
	default:
		return false
	}
}

// deliver sends a message tied to pc: on its POST stream when it has one, else on the session's
// GET stream.
func (p *Proc) deliver(s *Session, pc *pendingCall, b []byte) bool {
	if pc != nil && pc.events != nil {
		return push(pc.events, b)
	}
	if s == nil {
		return false
	}
	p.mu.Lock()
	ch := s.stream
	p.mu.Unlock()
	return ch != nil && push(ch, b)
}

// broadcast sends an unrelated notification to every open GET stream. When there is none and
// exactly one streaming call is in flight, that call's stream gets it.
func (p *Proc) broadcast(b []byte) {
	p.mu.Lock()
	var chans []chan []byte
	for _, s := range p.sessions {
		if s.stream != nil {
			chans = append(chans, s.stream)
		}
	}
	var only *pendingCall
	if len(chans) == 0 {
		for _, pc := range p.pending {
			if pc.events != nil {
				if only != nil {
					only = nil
					break
				}
				only = pc
			}
		}
	}
	p.mu.Unlock()
	for _, ch := range chans {
		push(ch, b)
	}
	if only != nil {
		push(only.events, b)
	}
}

// onServerRequest routes a request from the child to a client that can answer it.
func (p *Proc) onServerRequest(c *child, m rpcMsg) {
	if m.Method == "ping" {
		_ = c.send(mustJSON(rpcMsg{JSONRPC: "2.0", ID: m.ID, Result: json.RawMessage("{}")}))
		return
	}
	capName := ""
	switch m.Method {
	case "roots/list":
		capName = "roots"
	case "sampling/createMessage":
		capName = "sampling"
	case "elicitation/create":
		capName = "elicitation"
	}
	sess, pc := p.pickClient(capName)
	if sess == nil {
		_ = c.send(errorResponse(m.ID, -32601, "no connected client can handle "+clipText(m.Method, 80)))
		return
	}
	p.mu.Lock()
	p.nextID++
	key := serverReqIDPfx + strconv.FormatInt(p.nextID, 10)
	sr := &serverReq{sess: sess, origID: m.ID}
	sr.timer = time.AfterFunc(serverReqTTL, func() {
		p.mu.Lock()
		_, ok := p.srvReqs[key]
		delete(p.srvReqs, key)
		p.mu.Unlock()
		if ok {
			_ = c.send(errorResponse(m.ID, -32000, "the client did not answer in time"))
		}
	})
	p.srvReqs[key] = sr
	p.mu.Unlock()
	out := mustJSON(rpcMsg{JSONRPC: "2.0", ID: mustJSON(key), Method: m.Method, Params: m.Params})
	if !p.deliver(sess, pc, out) {
		p.mu.Lock()
		delete(p.srvReqs, key)
		p.mu.Unlock()
		sr.timer.Stop()
		_ = c.send(errorResponse(m.ID, -32000, "the client stream is not available"))
	}
}

// pickClient chooses the session (and its in-flight streaming call, if any) for a server request:
// a session that advertised the capability and is currently waiting on a streaming call, else one
// with an open GET stream.
func (p *Proc) pickClient(capName string) (*Session, *pendingCall) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ok := func(s *Session) bool { return s != nil && !s.internal && (capName == "" || s.caps[capName]) }
	var best *pendingCall
	for _, pc := range p.pending {
		if pc.events != nil && ok(pc.sess) && (best == nil || pc.childID > best.childID) {
			best = pc
		}
	}
	if best != nil {
		return best.sess, best
	}
	var bs *Session
	for _, s := range p.sessions {
		if s.stream != nil && ok(s) && (bs == nil || s.lastSeen.After(bs.lastSeen)) {
			bs = s
		}
	}
	return bs, nil
}

// answerServerRequest handles a response from a client to a request the child made.
func (p *Proc) answerServerRequest(s *Session, m rpcMsg) {
	var key string
	if json.Unmarshal(m.ID, &key) != nil {
		return
	}
	p.mu.Lock()
	sr := p.srvReqs[key]
	if sr != nil && sr.sess != s {
		sr = nil // another session's request
	}
	delete(p.srvReqs, key)
	c := p.cur
	p.mu.Unlock()
	if sr == nil || c == nil {
		return
	}
	sr.timer.Stop()
	_ = c.send(mustJSON(rpcMsg{JSONRPC: "2.0", ID: sr.origID, Result: m.Result, Error: m.Error}))
}

// ---- skgate -> child ----

var errNotRunning = errors.New("the managed process is not running")

// rewriteProgress swaps the request's progressToken for a unique one so several clients can use the
// same token values. It returns the new params, the mapped token and the original.
func rewriteProgress(params json.RawMessage, childID int64) (json.RawMessage, string, json.RawMessage) {
	if len(params) == 0 || params[0] != '{' {
		return params, "", nil
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(params, &top) != nil {
		return params, "", nil
	}
	var meta map[string]json.RawMessage
	if json.Unmarshal(top["_meta"], &meta) != nil || len(meta["progressToken"]) == 0 {
		return params, "", nil
	}
	orig := meta["progressToken"]
	tok := mustJSON(progressPfx + strconv.FormatInt(childID, 10))
	meta["progressToken"] = tok
	top["_meta"] = mustJSON(meta)
	return mustJSON(top), string(tok), orig
}

// start registers and sends one request. The returned call must be finished with p.dropPending or
// by receiving its response.
func (p *Proc) startCall(s *Session, m rpcMsg, stream bool) (*pendingCall, error) {
	p.mu.Lock()
	c := p.cur
	running := p.state == StateRunning
	p.mu.Unlock()
	if c == nil || !running {
		return nil, errNotRunning
	}
	pc := p.newPending(s, m.ID, stream, s != nil && s.internal)
	params, tok, orig := rewriteProgress(m.Params, pc.childID)
	if tok != "" {
		pc.token, pc.origToken = tok, orig
		p.mu.Lock()
		p.tokens[tok] = pc
		p.mu.Unlock()
	}
	out := mustJSON(rpcMsg{JSONRPC: "2.0", ID: json.RawMessage(strconv.FormatInt(pc.childID, 10)), Method: m.Method, Params: params})
	if err := c.send(out); err != nil {
		p.dropPending(pc)
		return nil, errNotRunning
	}
	return pc, nil
}

// abandon drops a call whose client went away and tells the child to stop working on it.
func (p *Proc) abandon(pc *pendingCall, reason string) {
	p.mu.Lock()
	_, was := p.pending[pc.childID]
	c := p.cur
	p.mu.Unlock()
	p.dropPending(pc)
	if was && c != nil {
		_ = c.send(mustJSON(rpcMsg{JSONRPC: "2.0", Method: "notifications/cancelled",
			Params: mustJSON(map[string]any{"requestId": pc.childID, "reason": reason})}))
	}
}

// relayResponse restores the client's request id in a child response.
func relayResponse(raw []byte, clientID json.RawMessage) []byte {
	var m rpcMsg
	if json.Unmarshal(raw, &m) != nil {
		return errorResponse(clientID, -32603, "invalid response from the managed process")
	}
	m.ID = clientID
	if m.Error == nil && len(m.Result) == 0 {
		m.Result = json.RawMessage("null")
	}
	return mustJSON(m)
}

// clientCancel forwards a client's notifications/cancelled, mapping the request id.
func (p *Proc) clientCancel(s *Session, params json.RawMessage) {
	var cp struct {
		RequestID json.RawMessage `json:"requestId"`
		Reason    string          `json:"reason"`
	}
	if json.Unmarshal(params, &cp) != nil || len(cp.RequestID) == 0 {
		return
	}
	want := compact(cp.RequestID)
	p.mu.Lock()
	var target *pendingCall
	for _, pc := range p.pending {
		if pc.sess == s && compact(pc.clientID) == want {
			target = pc
			break
		}
	}
	c := p.cur
	p.mu.Unlock()
	if target == nil || c == nil {
		return
	}
	_ = c.send(mustJSON(rpcMsg{JSONRPC: "2.0", Method: "notifications/cancelled",
		Params: mustJSON(map[string]any{"requestId": target.childID, "reason": clipText(cp.Reason, 200)})}))
	// The server owes no response to a cancelled request; end the client's HTTP request now.
	p.dropPending(target)
	select {
	case target.done <- errorResponse(json.RawMessage(strconv.FormatInt(target.childID, 10)), -32800, "request cancelled"):
	default:
	}
}

// ---- sessions ----

// clientHello is what a client says in initialize.
type clientHello struct {
	proto string
	caps  json.RawMessage
}

func parseHello(params json.RawMessage) clientHello {
	var ip struct {
		ProtocolVersion string          `json:"protocolVersion"`
		Capabilities    json.RawMessage `json:"capabilities"`
	}
	_ = json.Unmarshal(params, &ip)
	return clientHello{proto: ip.ProtocolVersion, caps: ip.Capabilities}
}

func (p *Proc) newSession(h clientHello) *Session {
	var b [16]byte
	_, _ = rand.Read(b[:])
	s := &Session{ID: hex.EncodeToString(b[:]), Created: time.Now(), proto: h.proto, caps: map[string]bool{}}
	var caps map[string]json.RawMessage
	if len(h.caps) > 0 && json.Unmarshal(h.caps, &caps) == nil {
		for _, k := range []string{"roots", "sampling", "elicitation"} {
			if _, ok := caps[k]; ok {
				s.caps[k] = true
			}
		}
	}
	s.lastSeen = time.Now()
	p.mu.Lock()
	p.sessions[s.ID] = s
	p.mu.Unlock()
	return s
}

// session finds a client session; touch refreshes its idle timer.
func (p *Proc) session(id string) *Session {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.sessions[id]
	if s != nil {
		s.lastSeen = time.Now()
	}
	return s
}

// EndSession removes a client session and closes its stream. It reports whether it existed.
func (p *Proc) EndSession(id string) bool {
	p.mu.Lock()
	s := p.sessions[id]
	delete(p.sessions, id)
	var ch chan []byte
	if s != nil {
		ch, s.stream = s.stream, nil
	}
	for k, sr := range p.srvReqs {
		if sr.sess == s {
			sr.timer.Stop()
			delete(p.srvReqs, k)
		}
	}
	p.mu.Unlock()
	if ch != nil {
		close(ch)
	}
	return s != nil
}

func (p *Proc) closeSessions() {
	p.mu.Lock()
	ids := make([]string, 0, len(p.sessions))
	for id := range p.sessions {
		ids = append(ids, id)
	}
	p.mu.Unlock()
	for _, id := range ids {
		p.EndSession(id)
	}
}

func (p *Proc) expireSessions() {
	p.mu.Lock()
	var old []string
	for id, s := range p.sessions {
		if s.stream == nil && time.Since(s.lastSeen) > sessionTTL {
			old = append(old, id)
		}
	}
	p.mu.Unlock()
	for _, id := range old {
		p.EndSession(id)
	}
}

// openStream attaches a GET stream to the session, replacing (and closing) an older one. The
// returned release must be called when the HTTP request ends.
func (p *Proc) openStream(s *Session) (ch chan []byte, release func()) {
	ch = make(chan []byte, streamBuffer)
	p.mu.Lock()
	old := s.stream
	s.stream = ch
	s.gen++
	gen := s.gen
	p.mu.Unlock()
	if old != nil {
		close(old)
	}
	return ch, func() {
		p.mu.Lock()
		if s.gen == gen && s.stream == ch {
			s.stream = nil
			p.mu.Unlock()
			close(ch)
			return
		}
		p.mu.Unlock()
	}
}

// ---- Go API for the aggregator and Test ----

// InitResult returns the child's cached initialize result (empty until running).
func (p *Proc) InitResult() json.RawMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.initResult
}

func (p *Proc) internalSession() *Session {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.internal == nil {
		p.internal = &Session{ID: "internal", internal: true, caps: map[string]bool{}, Created: time.Now()}
	}
	return p.internal
}

// Call starts the process when needed, sends one request as skgate itself (no client session) and
// waits for the result. A JSON-RPC error from the server is returned as rpcErr.
func (p *Proc) Call(ctx context.Context, method string, params any) (result json.RawMessage, rpcErr *RPCError, err error) {
	if err := p.Ensure(ctx, startHint{}); err != nil {
		return nil, nil, err
	}
	var pb json.RawMessage
	if params != nil {
		pb = mustJSON(params)
	}
	m := rpcMsg{Method: method, Params: pb, ID: json.RawMessage(`0`)}
	pc, err := p.startCall(p.internalSession(), m, false)
	if err != nil {
		return nil, nil, err
	}
	select {
	case raw := <-pc.done:
		var r rpcMsg
		if json.Unmarshal(raw, &r) != nil {
			return nil, nil, errors.New("invalid response from the managed process")
		}
		if r.Error != nil {
			if r.Error.Message == exitedMessage {
				return nil, nil, errors.New(exitedMessage)
			}
			return nil, r.Error, nil
		}
		return r.Result, nil, nil
	case <-ctx.Done():
		p.abandon(pc, "skgate request ended")
		return nil, nil, errors.New(describeCtx(ctx))
	}
}

func describeCtx(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timed out"
	}
	return "canceled"
}

// Touch marks the process as used now (keeps an on-demand process from idling out).
func (p *Proc) Touch() {
	p.mu.Lock()
	p.lastActive = time.Now()
	p.mu.Unlock()
}

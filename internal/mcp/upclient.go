package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// upClient talks to one upstream MCP server on behalf of the aggregator. It keeps one MCP session
// (Mcp-Session-Id) per upstream in memory, shared by all aggregator clients: the session is
// created with initialize + notifications/initialized on first use, reused for every call, and
// recreated when the upstream forgets it (HTTP 404) or when the upstream's settings change.
type upClient struct {
	mu   sync.Mutex
	sess map[string]*upSess
	seq  atomic.Int64
}

type upSess struct {
	mu    sync.Mutex // serializes initialize for this upstream
	fp    string     // fingerprint of the settings the session was created with
	sid   string
	proto string
	ready bool
}

func newUpClient() *upClient { return &upClient{sess: map[string]*upSess{}} }

// fingerprint changes whenever something that affects the connection changes, so an edited
// upstream never keeps using an old session. The credential is hashed, never stored or logged.
func fingerprint(u Upstream) string {
	sum := u.URL + "\x00" + u.EffectiveKind() + "\x00" + u.AuthName + "\x00" + u.AuthValue + "\x00" + u.HostOverride
	for _, h := range u.Headers {
		sum += "\x00" + h.Name + "=" + h.Value
	}
	h := sha256.Sum256([]byte(sum))
	return hex.EncodeToString(h[:8])
}

const aggTimeout = 30 * time.Second // per upstream request for list style calls
const aggCallTimeout = 2 * time.Minute

// upError is a failed upstream call. Text is plain and never contains credentials or URLs.
type upError struct {
	Status int
	Msg    string
}

func (e *upError) Error() string { return e.Msg }

// session returns the upstream session id and negotiated protocol, initializing when needed.
// force discards a cached session first.
func (c *upClient) session(s *Server, ctx context.Context, up Upstream, force bool) (sid, proto string, err error) {
	c.mu.Lock()
	ss := c.sess[up.Alias]
	if ss == nil {
		ss = &upSess{}
		c.sess[up.Alias] = ss
	}
	c.mu.Unlock()
	ss.mu.Lock()
	defer ss.mu.Unlock()
	fp := fingerprint(up)
	if ss.ready && ss.fp == fp && !force {
		return ss.sid, ss.proto, nil
	}
	ss.fp, ss.sid, ss.proto, ss.ready = fp, "", "", false
	rep, err := s.rpcPost(ctx, up, initBody(), 1, "", "", aggTimeout)
	if err != nil && rep == nil {
		return "", "", &upError{Msg: err.Error()}
	}
	if e := statusError("initialize", rep, up); e != nil {
		return "", "", e
	}
	if err != nil {
		return "", "", &upError{Status: rep.Status, Msg: "initialize: " + err.Error()}
	}
	if rep.Err != nil {
		return "", "", &upError{Status: rep.Status, Msg: fmt.Sprintf("initialize: JSON-RPC error %d: %s", rep.Err.Code, clipText(rep.Err.Message, 200))}
	}
	var ir struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if json.Unmarshal(rep.Result, &ir) != nil {
		return "", "", &upError{Status: rep.Status, Msg: "initialize: the result is not a valid MCP initialize result"}
	}
	ni, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	_, _ = s.rpcPost(ctx, up, ni, 0, rep.SessionID, ir.ProtocolVersion, aggTimeout) // best effort
	ss.sid, ss.proto, ss.ready = rep.SessionID, ir.ProtocolVersion, true
	return ss.sid, ss.proto, nil
}

func (c *upClient) drop(alias string) {
	c.mu.Lock()
	delete(c.sess, alias)
	c.mu.Unlock()
}

// statusError converts a non-2xx reply into an upError.
func statusError(what string, rep *rpcReply, up Upstream) *upError {
	switch {
	case rep.Status == http.StatusUnauthorized || rep.Status == http.StatusForbidden:
		m := fmt.Sprintf("%s: upstream answered HTTP %d, it rejected the outbound credentials (auth: %s)", what, rep.Status, up.EffectiveKind())
		if rep.Status == http.StatusUnauthorized && advertisesOAuth(rep.WWWAuth) {
			m += "; the upstream advertises OAuth, which skgate does not support for upstreams"
		}
		return &upError{Status: rep.Status, Msg: m}
	case rep.Status/100 != 2:
		return &upError{Status: rep.Status, Msg: fmt.Sprintf("%s: upstream answered HTTP %d", what, rep.Status)}
	}
	return nil
}

// call sends one JSON-RPC request to the upstream inside its session and returns the result, or
// the upstream's JSON-RPC error (rpcErr), or a transport/HTTP failure (err). A 404 on an existing
// session (the upstream restarted or expired it) triggers one transparent re-initialize.
func (c *upClient) call(s *Server, ctx context.Context, up Upstream, method string, params any, timeout time.Duration) (res json.RawMessage, rpcErr *rpcError, err error) {
	if up.Managed() {
		return c.callManaged(s, ctx, up, method, params, timeout)
	}
	if up.IsOpenAPI() {
		return c.callOpenAPI(s, ctx, up, method, params)
	}
	up = s.ensureDetected(ctx, up)
	eff := withKind(up, up.EffectiveKind())
	for attempt := 0; attempt < 2; attempt++ {
		sid, proto, serr := c.session(s, ctx, eff, attempt > 0)
		if serr != nil {
			return nil, nil, serr
		}
		id := int(c.seq.Add(1)%1_000_000) + 10
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		rep, perr := s.rpcPost(ctx, eff, body, id, sid, proto, timeout)
		if perr != nil && rep == nil {
			return nil, nil, &upError{Msg: method + ": " + perr.Error()}
		}
		if rep.Status == http.StatusNotFound && sid != "" && attempt == 0 {
			c.drop(up.Alias)
			continue
		}
		if e := statusError(method, rep, up); e != nil {
			return nil, nil, e
		}
		if perr != nil {
			return nil, nil, &upError{Status: rep.Status, Msg: method + ": " + perr.Error()}
		}
		return rep.Result, rep.Err, nil
	}
	return nil, nil, &upError{Msg: method + ": upstream session could not be re-established"}
}

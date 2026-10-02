package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/reqlog"
)

const (
	probeTimeout   = 15 * time.Second
	maxProbeBody   = 4 << 20
	probeProtocol  = "2025-06-18"
	oauthAdvertise = "oauth"
)

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// rpcReply is one decoded MCP response.
type rpcReply struct {
	Status    int
	SessionID string
	WWWAuth   string
	Result    json.RawMessage
	Err       *rpcError
	Header    http.Header
	Elapsed   time.Duration
}

// rpcPost POSTs one JSON-RPC message to the upstream using the outbound auth of up (whose
// AuthKind the caller sets to the concrete kind to use), honouring the host override. The reply
// may be JSON or an SSE stream; wantID selects the matching JSON-RPC response (0 means the
// message is a notification and no body is expected). The upstream's credentials and URL are
// never included in returned errors.
func (s *Server) rpcPost(ctx context.Context, up Upstream, body []byte, wantID int, sessionID, protocol string, timeout time.Duration) (*rpcReply, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, up.URL, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid upstream URL")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", "skgate/"+config.Version+" (upstream probe)")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	if protocol != "" {
		req.Header.Set("Mcp-Protocol-Version", protocol)
	}
	applyOutbound(req, nil, up)
	start := time.Now()
	resp, err := s.doUpstream(up, req, body)
	if err != nil {
		return nil, fmt.Errorf("upstream unreachable: %s", describeNetErr(ctx, err))
	}
	defer resp.Body.Close()
	rep := &rpcReply{Status: resp.StatusCode, SessionID: resp.Header.Get("Mcp-Session-Id"),
		WWWAuth: resp.Header.Get("Www-Authenticate"), Header: resp.Header}
	defer func() { rep.Elapsed = time.Since(start) }()
	if resp.StatusCode/100 != 2 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		rep.Elapsed = time.Since(start)
		return rep, nil
	}
	if wantID == 0 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		rep.Elapsed = time.Since(start)
		return rep, nil
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	lr := io.LimitReader(resp.Body, maxProbeBody)
	if strings.HasPrefix(ct, "text/event-stream") {
		res, rerr, perr := readSSEReply(lr, wantID)
		rep.Result, rep.Err = res, rerr
		rep.Elapsed = time.Since(start)
		if perr != nil {
			return rep, perr
		}
		return rep, nil
	}
	b, err := io.ReadAll(lr)
	if err != nil {
		return rep, fmt.Errorf("reading upstream response: %s", describeNetErr(ctx, err))
	}
	rep.Elapsed = time.Since(start)
	res, rerr, perr := decodeRPC(b, wantID)
	rep.Result, rep.Err = res, rerr
	return rep, perr
}

// describeNetErr turns a transport error into a short plain sentence without URLs.
func describeNetErr(ctx context.Context, err error) string {
	if ctx.Err() == context.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	return reqlog.Sanitize(err)
}

// decodeRPC finds the JSON-RPC response with the given id in a JSON body (object or batch).
func decodeRPC(b []byte, wantID int) (json.RawMessage, *rpcError, error) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil, nil, errors.New("empty response body, not a JSON-RPC message")
	}
	var msgs []json.RawMessage
	if b[0] == '[' {
		if err := json.Unmarshal(b, &msgs); err != nil {
			return nil, nil, errors.New("response is not valid JSON")
		}
	} else {
		msgs = []json.RawMessage{b}
	}
	sawRPC := false
	for _, m := range msgs {
		var e struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Result  json.RawMessage `json:"result"`
			Error   *rpcError       `json:"error"`
		}
		if json.Unmarshal(m, &e) != nil {
			continue
		}
		if e.JSONRPC != "2.0" {
			continue
		}
		sawRPC = true
		if string(bytes.TrimSpace(e.ID)) != fmt.Sprint(wantID) {
			continue
		}
		if e.Error != nil {
			return nil, e.Error, nil
		}
		if len(e.Result) == 0 {
			continue
		}
		return e.Result, nil, nil
	}
	if sawRPC {
		return nil, nil, errors.New("JSON-RPC message without a matching result")
	}
	return nil, nil, errors.New("response is not a JSON-RPC 2.0 message")
}

// readSSEReply reads an SSE stream until it finds the JSON-RPC response with the given id.
func readSSEReply(r io.Reader, wantID int) (json.RawMessage, *rpcError, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), maxProbeBody)
	var data []string
	var lastErr error = errors.New("event stream ended without a JSON-RPC response")
	try := func() (json.RawMessage, *rpcError, bool) {
		if len(data) == 0 {
			return nil, nil, false
		}
		payload := strings.Join(data, "\n")
		data = nil
		res, rerr, err := decodeRPC([]byte(payload), wantID)
		if err != nil {
			lastErr = err
			return nil, nil, false
		}
		return res, rerr, true
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if res, rerr, ok := try(); ok {
				return res, rerr, nil
			}
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if res, rerr, ok := try(); ok {
		return res, rerr, nil
	}
	return nil, nil, lastErr
}

func initBody() []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": probeProtocol, "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "skgate", "version": config.Version}}})
	return b
}

// advertisesOAuth reports whether a 401 WWW-Authenticate challenge points at OAuth.
func advertisesOAuth(h string) bool {
	l := strings.ToLower(h)
	for _, k := range []string{"resource_metadata", "authorization_uri", "as_uri", oauthAdvertise} {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

// withKind returns u with a concrete outbound auth kind (used for probing).
func withKind(u Upstream, kind string) Upstream {
	u.AuthKind = kind
	return u
}

// Detect probes the upstream with an MCP initialize POST using, in order, no auth, bearer (the
// stored credential) and header (auth_name + the stored credential, skipped without a name). The
// first attempt that answers with a valid initialize result wins. 401 and 403 count as failure;
// if a 401 challenge advertises OAuth and nothing worked the result is "oauth (unsupported)".
// It returns the detected kind (none, bearer, header, oauth (unsupported) or failed) and a note
// with one short outcome per attempt. Credentials never appear in the note.
func (s *Server) Detect(ctx context.Context, up Upstream) (kind, note string) {
	type attempt struct {
		kind string
		skip string
	}
	plan := []attempt{{kind: AuthNone}}
	if up.AuthValue != "" {
		plan = append(plan, attempt{kind: AuthBearer})
	} else {
		plan = append(plan, attempt{kind: AuthBearer, skip: "no credential stored"})
	}
	switch {
	case up.AuthValue == "":
		plan = append(plan, attempt{kind: AuthHeader, skip: "no credential stored"})
	case up.AuthName == "" || !validHeaderName(up.AuthName):
		plan = append(plan, attempt{kind: AuthHeader, skip: "no header name given"})
	default:
		plan = append(plan, attempt{kind: AuthHeader})
	}
	var outcomes []string
	oauth := false
	for _, a := range plan {
		if a.skip != "" {
			outcomes = append(outcomes, a.kind+": skipped ("+a.skip+")")
			continue
		}
		rep, err := s.rpcPost(ctx, withKind(up, a.kind), initBody(), 1, "", "", probeTimeout)
		var out string
		ok := false
		switch {
		case err != nil && rep == nil:
			out = err.Error()
		case rep.Status == http.StatusUnauthorized || rep.Status == http.StatusForbidden:
			out = fmt.Sprintf("HTTP %d", rep.Status)
			if rep.Status == http.StatusUnauthorized && advertisesOAuth(rep.WWWAuth) {
				oauth = true
				out += " (advertises OAuth)"
			}
		case rep.Status/100 == 3:
			out = fmt.Sprintf("HTTP %d redirect (not followed)", rep.Status)
		case rep.Status/100 != 2:
			out = fmt.Sprintf("HTTP %d", rep.Status)
		case err != nil:
			out = fmt.Sprintf("HTTP %d but %s", rep.Status, err.Error())
		case rep.Err != nil:
			out = fmt.Sprintf("HTTP %d with JSON-RPC error %d", rep.Status, rep.Err.Code)
		case len(rep.Result) == 0 || string(rep.Result) == "null":
			out = fmt.Sprintf("HTTP %d but the JSON-RPC result is empty", rep.Status)
		default:
			ok = true
			out = fmt.Sprintf("HTTP %d, valid initialize result", rep.Status)
		}
		outcomes = append(outcomes, a.kind+": "+out)
		if ok {
			return a.kind, strings.Join(outcomes, "; ")
		}
	}
	if oauth {
		return DetectedOAuth, strings.Join(outcomes, "; ")
	}
	return DetectedFailed, strings.Join(outcomes, "; ")
}

// Redetect runs Detect for a stored upstream and persists the result. It works for any auth
// kind, but the result only affects runtime behavior when the upstream's mode is auto.
func (s *Server) Redetect(ctx context.Context, alias string) (Upstream, error) {
	up, ok := s.Upstreams.Get(alias)
	if !ok {
		return up, errors.New("unknown alias")
	}
	kind, note := s.Detect(ctx, up)
	if cur, ok := s.Upstreams.Get(alias); ok {
		up.URL = cur.URL // a trailing-slash correction made while probing
	}
	if err := s.Upstreams.SetDetected(alias, kind, note); err != nil {
		return up, err
	}
	up.DetectedKind, up.DetectedNote = kind, note
	return up, nil
}

// ensureDetected runs detection once for an auto upstream that has none stored yet (for example
// right after an edit). Concurrent requests wait for the same probe.
func (s *Server) ensureDetected(ctx context.Context, up Upstream) Upstream {
	if up.AuthKind != AuthAuto || up.DetectedKind != "" {
		return up
	}
	s.detectMu.Lock()
	defer s.detectMu.Unlock()
	if cur, ok := s.Upstreams.Get(up.Alias); ok {
		if cur.DetectedKind != "" || cur.AuthKind != AuthAuto {
			return cur
		}
		up = cur
	}
	kind, note := s.Detect(ctx, up)
	_ = s.Upstreams.SetDetected(up.Alias, kind, note)
	up.DetectedKind, up.DetectedNote = kind, note
	s.Log.Printf("upstream_detect alias=%s result=%q", up.Alias, kind)
	return up
}

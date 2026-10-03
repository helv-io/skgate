package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/helv-io/skgate/internal/config"
)

const (
	testTimeout  = 20 * time.Second
	maxTestTools = 100 // tool list cap in the Test result
	maxDescRunes = 200
)

// ToolInfo is one tool from tools/list.
type ToolInfo struct {
	Name string
	Desc string // first line, clipped
}

// TestResult is the outcome of the admin "Test" button.
type TestResult struct {
	OK          bool
	Status      int    // HTTP status of the initialize call (0 when unreachable)
	Auth        string // effective auth used: none, bearer, header, passthrough
	AuthMode    string // configured mode (auto, ...)
	Detected    string
	Latency     time.Duration
	Server      string // serverInfo name and version
	Protocol    string
	SessionID   bool // upstream issued an Mcp-Session-Id
	Tools       []ToolInfo
	ToolTotal   int
	ToolsCapped bool
	Warnings    []string
	Error       string
	// Detail and Hint are for the admin Test screen only: Detail is what the network stack said (it can
	// name the host and port); Hint says what to try, with the ports found open on the host.
	Detail string
	Hint   string
	Diag   *Diagnosis
}

// Test performs initialize, notifications/initialized and tools/list against the upstream with
// its effective auth (auto upstreams without a stored detection are detected first), handling
// JSON and SSE replies and Mcp-Session-Id. Passthrough sends no credentials because there is no
// inbound client request.
func (s *Server) Test(ctx context.Context, alias string) (tr TestResult) {
	up, ok := s.Upstreams.Get(alias)
	if !ok {
		tr.Error = "unknown alias"
		return tr
	}
	tr.AuthMode = up.AuthKind
	start := time.Now()
	defer func() { tr.Latency = time.Since(start) }()
	if up.Managed() {
		return s.testManaged(ctx, up)
	}
	if up.AuthKind == AuthAuto && up.DetectedKind == "" {
		up = s.ensureDetected(ctx, up)
	}
	tr.Detected = up.DetectedKind
	tr.Auth = up.EffectiveKind()
	if up.AuthKind == AuthAuto && (up.DetectedKind == DetectedOAuth || up.DetectedKind == DetectedFailed) {
		tr.Warnings = append(tr.Warnings, "auto-detection found no working auth ("+up.DetectedKind+"), sending no credentials: "+up.DetectedNote)
	}
	eff := withKind(up, up.EffectiveKind())
	rep, err := s.rpcPost(ctx, eff, initBody(), 1, "", "", testTimeout)
	if rep != nil {
		tr.Status = rep.Status
	}
	if err != nil && rep == nil {
		tr.Error = err.Error()
		s.explain(ctx, &tr, up, err)
		return tr
	}
	switch {
	case rep.Status == http.StatusUnauthorized || rep.Status == http.StatusForbidden:
		tr.Error = fmt.Sprintf("initialize: upstream answered HTTP %d, it rejected the outbound credentials (auth: %s)", rep.Status, tr.Auth)
		if rep.Status == http.StatusUnauthorized && advertisesOAuth(rep.WWWAuth) {
			tr.Error += "; the upstream advertises OAuth, which skgate does not support for upstreams"
		}
		return tr
	case rep.Status/100 != 2:
		tr.Error = fmt.Sprintf("initialize: upstream answered HTTP %d", rep.Status)
		return tr
	case err != nil:
		tr.Error = "initialize: " + err.Error()
		return tr
	case rep.Err != nil:
		tr.Error = fmt.Sprintf("initialize: JSON-RPC error %d: %s", rep.Err.Code, clipText(rep.Err.Message, 200))
		return tr
	}
	var ir struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if json.Unmarshal(rep.Result, &ir) != nil {
		tr.Error = "initialize: the result is not a valid MCP initialize result"
		return tr
	}
	if ir.ProtocolVersion == "" {
		tr.Warnings = append(tr.Warnings, "initialize result has no protocolVersion")
	}
	tr.Protocol = ir.ProtocolVersion
	tr.Server = strings.TrimSpace(ir.ServerInfo.Name + " " + ir.ServerInfo.Version)
	sid := rep.SessionID
	tr.SessionID = sid != ""
	ni, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	nrep, nerr := s.rpcPost(ctx, eff, ni, 0, sid, ir.ProtocolVersion, testTimeout)
	switch {
	case nerr != nil:
		tr.Warnings = append(tr.Warnings, "notifications/initialized: "+nerr.Error())
	case nrep.Status/100 != 2:
		tr.Warnings = append(tr.Warnings, fmt.Sprintf("notifications/initialized: upstream answered HTTP %d", nrep.Status))
	}
	lb, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}})
	lrep, lerr := s.rpcPost(ctx, eff, lb, 2, sid, ir.ProtocolVersion, testTimeout)
	defer func() {
		if sid != "" { // end the session politely, ignore the outcome
			s.endSession(ctx, eff, sid, ir.ProtocolVersion)
		}
	}()
	switch {
	case lerr != nil && lrep == nil:
		tr.Error = "tools/list: " + lerr.Error()
		s.explain(ctx, &tr, up, lerr)
		return tr
	case lrep.Status/100 != 2:
		tr.Error = fmt.Sprintf("tools/list: upstream answered HTTP %d", lrep.Status)
		return tr
	case lerr != nil:
		tr.Error = "tools/list: " + lerr.Error()
		return tr
	case lrep.Err != nil:
		tr.Error = fmt.Sprintf("tools/list: JSON-RPC error %d: %s", lrep.Err.Code, clipText(lrep.Err.Message, 200))
		return tr
	}
	var lr struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
		NextCursor string `json:"nextCursor"`
	}
	if json.Unmarshal(lrep.Result, &lr) != nil {
		tr.Error = "tools/list: the result is not a valid tools list"
		return tr
	}
	tr.ToolTotal = len(lr.Tools)
	for i, t := range lr.Tools {
		if i >= maxTestTools {
			tr.ToolsCapped = true
			break
		}
		tr.Tools = append(tr.Tools, ToolInfo{Name: clipText(t.Name, 100), Desc: firstLine(t.Description)})
	}
	if lr.NextCursor != "" {
		tr.ToolsCapped = true
		tr.Warnings = append(tr.Warnings, "the upstream paginates tools/list; only the first page is shown")
	}
	tr.OK = true
	return tr
}

// explain adds the admin-only detail and hint for a transport failure.
func (s *Server) explain(ctx context.Context, tr *TestResult, up Upstream, err error) {
	nf, ok := AsNetFail(err)
	if !ok {
		return
	}
	tr.Detail = nf.Detail
	if nf.Kind == NetRefused || nf.Kind == NetDNS {
		tr.Diag = s.Diagnose(ctx, up, nf.Kind)
		tr.Hint = tr.Diag.Hint(true)
	}
}

func (s *Server) endSession(ctx context.Context, up Upstream, sid, protocol string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, up.URL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Mcp-Session-Id", sid)
	if protocol != "" {
		req.Header.Set("Mcp-Protocol-Version", protocol)
	}
	req.Header.Set("User-Agent", "skgate/"+config.Version+" (upstream probe)")
	applyOutbound(req, nil, up)
	if resp, err := s.HTTP.Do(req); err == nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return clipText(strings.TrimSpace(s), maxDescRunes)
}

func clipText(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

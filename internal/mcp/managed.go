package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/managed"
	"github.com/helv-io/skgate/internal/reqlog"
)

// ManagedState reports whether managed upstreams can run, and if not, why (terse, for the UI).
func (s *Server) ManagedState() (ok bool, why string) {
	if !config.ManagedAvailable() {
		return false, "not available in the slim image; use the full image (:latest)"
	}
	return true, ""
}

// Commands lists the common commands available to managed children (for the admin form).
func (s *Server) Commands() []managed.Command {
	if ok, _ := s.ManagedState(); !ok {
		return nil
	}
	return s.Managed.AvailableCommands()
}

var errManagedOff = errors.New("managed upstreams are not available in the slim image")

// proc returns the process of a managed upstream, applying its current settings.
func (s *Server) proc(up Upstream) (*managed.Proc, error) {
	if ok, _ := s.ManagedState(); !ok {
		return nil, errManagedOff
	}
	return s.Managed.Proc(up.Spec())
}

// SyncManaged makes the process manager follow the stored upstream: a changed or enabled managed
// upstream gets its (replaced) process, always-on ones start; a deleted, disabled or non-managed one
// is stopped and forgotten. It never blocks on the process.
func (s *Server) SyncManaged(alias string) {
	up, ok := s.Upstreams.Get(alias)
	if !ok || !up.Managed() || !up.Enabled {
		s.Managed.Forget(alias)
		return
	}
	if ok, _ := s.ManagedState(); !ok {
		return
	}
	p, err := s.Managed.Proc(up.Spec())
	if err != nil {
		s.Log.Printf("managed[%s]: %v", alias, err)
		return
	}
	if up.Lifecycle == managed.Always {
		if err := p.Start(); err != nil {
			s.Log.Printf("managed[%s]: not started: %v", alias, err)
		}
	}
}

// StartManaged starts the always-on managed upstreams (boot).
func (s *Server) StartManaged() {
	if ok, _ := s.ManagedState(); !ok {
		return
	}
	ups, err := s.Upstreams.List()
	if err != nil {
		return
	}
	var specs []managed.Spec
	for _, u := range ups {
		if u.Managed() && u.Enabled {
			specs = append(specs, u.Spec())
		}
	}
	s.Managed.Register(specs) // known from boot, so update checks and auto-update cover on-demand ones too
	s.Managed.StartAlways(specs)
}

// ShutdownManaged stops every child process (SIGTERM, then SIGKILL after the grace period).
func (s *Server) ShutdownManaged() { s.Managed.Shutdown() }

// serveManaged answers /mcp/{alias} for a managed upstream from its process.
func (s *Server) serveManaged(w http.ResponseWriter, r *http.Request, up Upstream) {
	reqlog.Upstream(r, up.Alias, "", 0, "managed")
	p, err := s.proc(up)
	if err != nil {
		reqlog.Reject(r, "managed upstream unavailable: %s", err)
		jsonErr(w, http.StatusServiceUnavailable, "managed_unavailable", err.Error())
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodPost, http.MethodDelete:
	default:
		reqlog.Reject(r, "method %s not allowed on /mcp", r.Method)
		w.Header().Set("Allow", "GET, POST, DELETE, OPTIONS")
		jsonErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST (Streamable HTTP), GET (SSE stream) or DELETE (end session)")
		return
	}
	httputil.SetCORS(w)
	p.ServeHTTP(w, r, func(w http.ResponseWriter, status int, code, desc string) {
		if status >= 500 {
			reqlog.Reject(r, "managed upstream error: %s", desc)
		}
		jsonErr(w, status, code, desc)
	})
}

// callManaged is upClient.call for a managed upstream: no HTTP, no sessions, the process is
// started on demand and shared with the per-alias endpoint.
func (c *upClient) callManaged(s *Server, ctx context.Context, up Upstream, method string, params any, timeout time.Duration) (res json.RawMessage, rpcErr *rpcError, err error) {
	p, perr := s.proc(up)
	if perr != nil {
		return nil, nil, &upError{Msg: method + ": " + perr.Error()}
	}
	wait := timeout
	if st := up.Spec().StartupTimeout; st > wait {
		wait = st
	}
	rctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	if err := p.Ready(rctx); err != nil {
		return nil, nil, &upError{Msg: method + ": " + managedErr(err)}
	}
	cctx, cancel2 := context.WithTimeout(ctx, timeout)
	defer cancel2()
	out, re, cerr := p.Call(cctx, method, params)
	if cerr != nil {
		return nil, nil, &upError{Msg: method + ": " + managedErr(cerr)}
	}
	if re != nil {
		return nil, &rpcError{Code: re.Code, Message: re.Message}, nil
	}
	return out, nil, nil
}

func managedErr(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out waiting for the managed process"
	}
	return clipText(err.Error(), 300)
}

// testManaged is Test for a managed upstream: start the process if needed, take its initialize
// result and list its tools.
func (s *Server) testManaged(ctx context.Context, up Upstream) (tr TestResult) {
	start := time.Now()
	defer func() { tr.Latency = time.Since(start) }()
	tr.Auth, tr.AuthMode = "managed", "managed"
	p, err := s.proc(up)
	if err != nil {
		tr.Error = err.Error()
		return tr
	}
	wait := up.Spec().StartupTimeout
	if wait <= 0 {
		wait = managed.DefaultStartupTimeout
	}
	if up.Install != "" || up.Kind == KindGit {
		wait += s.Cfg.ManagedInstallMax // the first start may clone and install
	}
	rctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	if err := p.Ready(rctx); err != nil {
		tr.Error = "start: " + managedErr(err)
		return tr
	}
	var ir struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if json.Unmarshal(p.InitResult(), &ir) != nil {
		tr.Error = "initialize: the result is not a valid MCP initialize result"
		return tr
	}
	if ir.ProtocolVersion == "" {
		tr.Warnings = append(tr.Warnings, "initialize result has no protocolVersion")
	}
	tr.Protocol = ir.ProtocolVersion
	tr.Server = strings.TrimSpace(ir.ServerInfo.Name + " " + ir.ServerInfo.Version)
	cctx, cancel2 := context.WithTimeout(ctx, testTimeout)
	defer cancel2()
	res, re, err := p.Call(cctx, "tools/list", map[string]any{})
	switch {
	case err != nil:
		tr.Error = "tools/list: " + managedErr(err)
		return tr
	case re != nil:
		tr.Error = fmt.Sprintf("tools/list: JSON-RPC error %d: %s", re.Code, clipText(re.Message, 200))
		return tr
	}
	var lr struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
		NextCursor string `json:"nextCursor"`
	}
	if json.Unmarshal(res, &lr) != nil {
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
		tr.Warnings = append(tr.Warnings, "the server paginates tools/list; only the first page is shown")
	}
	tr.OK = true
	return tr
}

// bridgeManaged serves one legacy /messages POST from a managed process: the message is handled
// like a Streamable HTTP POST on a private client session and the replies go to the SSE stream.
func (s *Server) bridgeManaged(ctx context.Context, ss *sseSession, body []byte, fail func(string)) {
	p, err := s.proc(ss.up)
	if err != nil {
		fail(err.Error())
		return
	}
	rec := &sseRecorder{h: http.Header{}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/mcp/"+ss.up.Alias, bytes.NewReader(body))
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	ss.mu.Lock()
	sid := ss.upstream
	ss.mu.Unlock()
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	p.ServeHTTP(rec, req, func(_ http.ResponseWriter, _ int, _, desc string) { fail(desc) })
	if id := rec.h.Get("Mcp-Session-Id"); id != "" {
		ss.mu.Lock()
		ss.upstream = id
		ss.mu.Unlock()
	}
	for _, m := range rec.messages() {
		ss.push(m)
	}
}

// sseRecorder captures a handler's response so its JSON-RPC messages can be re-sent on the legacy
// event stream.
type sseRecorder struct {
	h      http.Header
	status int
	buf    bytes.Buffer
}

func (r *sseRecorder) Header() http.Header         { return r.h }
func (r *sseRecorder) WriteHeader(code int)        { r.status = code }
func (r *sseRecorder) Write(b []byte) (int, error) { return r.buf.Write(b) }

func (r *sseRecorder) messages() [][]byte {
	if r.status != 0 && r.status/100 != 2 {
		return nil
	}
	if strings.HasPrefix(r.h.Get("Content-Type"), "text/event-stream") {
		var out [][]byte
		for _, blk := range strings.Split(r.buf.String(), "\n\n") {
			var data []string
			for _, ln := range strings.Split(blk, "\n") {
				if d, ok := strings.CutPrefix(ln, "data:"); ok {
					data = append(data, strings.TrimPrefix(d, " "))
				}
			}
			if len(data) > 0 {
				out = append(out, []byte(strings.Join(data, "\n")))
			}
		}
		return out
	}
	if b := bytes.TrimSpace(r.buf.Bytes()); len(b) > 0 {
		return [][]byte{append([]byte(nil), b...)}
	}
	return nil
}

// ProcInfo is what the admin UI shows about a managed upstream's process.
type ProcInfo struct {
	managed.Status
	Update    managed.UpdateInfo
	Available bool   // managed upstreams can run at all
	Why       string // why not
}

// ProcessInfo returns the process status of a managed upstream (stopped when it never ran).
func (s *Server) ProcessInfo(up Upstream) ProcInfo {
	ok, why := s.ManagedState()
	pi := ProcInfo{Available: ok, Why: why, Status: managed.Status{State: managed.StateStopped}}
	if p := s.Managed.Lookup(up.Alias); p != nil {
		pi.Status = p.Status()
		pi.Update = p.UpdateInfo()
	}
	return pi
}

// Process actions.
const (
	ActStart   = "start"
	ActStop    = "stop"
	ActRestart = "restart"
	ActUpdate  = "update"
	ActCheck   = "check"
	ActClear   = "clear-logs"
)

// ProcessAction runs an administrator's process action and returns a short result message. Start,
// restart and update return once the request is accepted; the state follows in the process table.
func (s *Server) ProcessAction(up Upstream, action string) (string, error) {
	if !up.Managed() {
		return "", errors.New("not a managed upstream")
	}
	p, err := s.proc(up)
	if err != nil {
		return "", err
	}
	switch action {
	case ActStart:
		if !up.Enabled {
			return "", errors.New("upstream is disabled")
		}
		return "start requested", p.Start()
	case ActStop:
		p.Stop(true)
		return "stopped", nil
	case ActRestart:
		if !up.Enabled {
			return "", errors.New("upstream is disabled")
		}
		return "restart requested", p.Restart()
	case ActUpdate:
		if !up.Enabled {
			return "", errors.New("upstream is disabled")
		}
		if err := p.UpdateAsync(); err != nil {
			return "", err
		}
		return "update started", nil
	case ActCheck:
		if up.Kind != KindGit {
			return "", errors.New("only git upstreams have a remote to check")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		if _, err := p.CheckRemote(ctx); err != nil {
			return "", err
		}
		if p.UpdateInfo().Available {
			return "update available", nil
		}
		return "up to date", nil
	case ActClear:
		p.ClearLogs()
		return "logs cleared", nil
	}
	return "", errors.New("unknown action")
}

// ProcessLogs returns the last n captured lines of a managed process (oldest first).
func (s *Server) ProcessLogs(alias string, n int) []managed.Line {
	if p := s.Managed.Lookup(alias); p != nil {
		return p.Logs(n)
	}
	return nil
}

package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/managed"
)

// Tool-list change notifications. Every endpoint skgate answers itself (the aggregate /mcp and /sse,
// OpenAPI and managed upstreams) advertises tools.listChanged. When a tool list really changes,
// notifications/tools/list_changed goes to the open event streams of that endpoint:
//   - managed: the process compares a hash of tools/list after each start (internal/managed/tools.go)
//     and tells its own sessions; OnToolsChanged brings the change here for the other transports.
//   - OpenAPI: the list served last is hashed; saving the selection or an update compares against it.
//   - aggregate: an included upstream's list changed, or an upstream was enabled, disabled, included,
//     left out or deleted.
// Remote upstreams are proxied unchanged, so their own notifications reach the client as they are.

const toolsStreamBuffer = 16

type toolsHub struct {
	mu     sync.Mutex
	subs   map[chan []byte]string // stream -> alias, "" for the aggregate
	hashes map[string]string      // OpenAPI alias -> hash of the tools/list served last
}

func (h *toolsHub) subscribe(alias string) (chan []byte, func()) {
	ch := make(chan []byte, toolsStreamBuffer)
	h.mu.Lock()
	if h.subs == nil {
		h.subs = map[chan []byte]string{}
	}
	h.subs[ch] = alias
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

// served records the hash of a tools/list answer and reports the previous one.
func (h *toolsHub) served(alias string, tools any) (prev, cur string) {
	b, _ := json.Marshal(tools)
	sum := sha256.Sum256(b)
	cur = hex.EncodeToString(sum[:])
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hashes == nil {
		h.hashes = map[string]string{}
	}
	prev = h.hashes[alias]
	h.hashes[alias] = cur
	return prev, cur
}

func (h *toolsHub) forget(alias string) {
	h.mu.Lock()
	delete(h.hashes, alias)
	h.mu.Unlock()
}

func (h *toolsHub) send(alias string, agg bool, msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch, a := range h.subs {
		if (alias != "" && a == alias) || (a == "" && agg) {
			select {
			case ch <- msg:
			default: // a full stream misses it; the client lists again on its next session anyway
			}
		}
	}
}

// ToolsChanged is called after something may have changed the tools of alias. OpenAPI upstreams are
// compared by hash so an unchanged list sends nothing.
func (s *Server) ToolsChanged(alias string) {
	up, ok := s.Upstreams.Get(alias)
	if ok && up.Enabled && up.IsOpenAPI() && !s.oaToolsChanged(up) {
		return
	}
	s.notifyTools(alias, !ok || up.IncludeInMCP)
}

// UpstreamsChanged is called after an upstream was enabled, disabled, included, left out or deleted:
// the aggregate's list changed even when the upstream's own did not.
func (s *Server) UpstreamsChanged(alias string) {
	if _, ok := s.Upstreams.Get(alias); !ok {
		s.tools.forget(alias)
	}
	s.notifyTools("", true)
}

func (s *Server) oaToolsChanged(up Upstream) bool {
	s.tools.mu.Lock()
	_, seen := s.tools.hashes[up.Alias]
	s.tools.mu.Unlock()
	if !seen {
		return false // never listed, so nobody holds the old list
	}
	tools, err := s.oaToolList(up)
	if err != nil {
		return false
	}
	prev, cur := s.tools.served(up.Alias, tools)
	return prev != cur
}

// notifyTools sends tools/list_changed to the streams of alias ("" for none) and, when agg is set,
// to the aggregate's. Managed upstreams tell their Streamable HTTP sessions themselves.
func (s *Server) notifyTools(alias string, agg bool) {
	msg := managed.ListChangedNotification()
	s.tools.send(alias, agg, msg)
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	for _, ss := range s.sessions {
		if (ss.agg && agg) || (alias != "" && !ss.agg && ss.up.Alias == alias && (ss.up.Managed() || ss.up.IsOpenAPI())) {
			select {
			case ss.events <- msg:
			case <-ss.done:
			default:
			}
		}
	}
}

func acceptsEventStream(r *http.Request) bool {
	for _, h := range r.Header.Values("Accept") {
		for _, part := range strings.Split(h, ",") {
			if mt, _, _ := mime.ParseMediaType(strings.TrimSpace(part)); mt == "text/event-stream" {
				return true
			}
		}
	}
	return false
}

// serveToolsStream is the GET event stream of an endpoint skgate answers itself: it carries only
// tools/list_changed. alias is "" for the aggregate.
func (s *Server) serveToolsStream(w http.ResponseWriter, r *http.Request, alias string) {
	if !acceptsEventStream(r) {
		jsonErr(w, http.StatusNotAcceptable, "not_acceptable", "Accept: text/event-stream is required")
		return
	}
	ch, release := s.tools.subscribe(alias)
	defer release()
	httputil.SetCORS(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	_, _ = w.Write([]byte(": ok\n\n"))
	_ = rc.Flush()
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			if _, err := w.Write([]byte("event: message\ndata: " + string(ev) + "\n\n")); err != nil {
				return
			}
			_ = rc.Flush()
		case <-tick.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			_ = rc.Flush()
		}
	}
}

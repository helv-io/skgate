package mcp

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Health is what the last calls to a remote upstream showed. It is kept in memory (it starts empty
// after a restart) and only for remote upstreams: a managed server has its process state instead.
type Health struct {
	OK        bool      // the latest call got a usable answer
	At        time.Time // when the latest call finished
	LastOK    time.Time // zero when no call has worked since start
	LastErr   string    // short text, never an address or a credential
	LastErrAt time.Time
}

type healthBook struct {
	mu sync.Mutex
	m  map[string]Health
}

// HealthOf returns the health of a remote upstream, false when nothing has been called yet.
func (s *Server) HealthOf(alias string) (Health, bool) {
	s.health.mu.Lock()
	defer s.health.mu.Unlock()
	h, ok := s.health.m[alias]
	return h, ok
}

// ForgetHealth drops what is known about an alias (it was edited or deleted).
func (s *Server) ForgetHealth(alias string) {
	s.health.mu.Lock()
	delete(s.health.m, alias)
	s.health.mu.Unlock()
}

// noteCall records the outcome of one call to a remote upstream.
func (s *Server) noteCall(ctx context.Context, up Upstream, resp *http.Response, err error) {
	if up.Alias == "" || up.Managed() {
		return
	}
	var bad string
	switch {
	case err != nil:
		if ctx.Err() == context.Canceled {
			return // the client went away; says nothing about the upstream
		}
		if nf, ok := AsNetFail(upstreamErr(ctx, err, false)); ok {
			bad = nf.Public()
		} else {
			bad = clipText(err.Error(), 120)
		}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		bad = fmt.Sprintf("rejected the outbound credentials (HTTP %d)", resp.StatusCode)
	case resp.StatusCode >= 500:
		bad = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	now := time.Now()
	s.health.mu.Lock()
	defer s.health.mu.Unlock()
	if s.health.m == nil {
		s.health.m = map[string]Health{}
	}
	h := s.health.m[up.Alias]
	h.At = now
	if bad == "" {
		h.OK, h.LastOK = true, now
	} else {
		h.OK, h.LastErr, h.LastErrAt = false, bad, now
	}
	s.health.m[up.Alias] = h
}

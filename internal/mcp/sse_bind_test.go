package mcp

import "testing"

// POST /messages is held to the resource its SSE stream was opened for (review SEC-07): an OAuth token bound to
// one /sse/{alias} cannot drive a stream of another alias, while a token for that alias or for all of /sse can.
func TestSSEMessagesBoundToStreamResource(t *testing.T) {
	e := newEnv(t, nil)
	other, _ := issueTokens(t, e, e.ts.URL+"/sse/public")
	own, _ := issueTokens(t, e, e.ts.URL+"/sse/private")
	parent, _ := issueTokens(t, e, e.ts.URL+"/sse")
	ss := &sseSession{id: "sess-bind", authPath: "/sse/private", events: make(chan []byte, 64), done: make(chan struct{})}
	ss.close() // nothing reads the stream in this test
	e.srv.sessMu.Lock()
	e.srv.sessions[ss.id] = ss
	e.srv.sessMu.Unlock()
	post := func(tok string) int {
		r := e.do("POST", "/messages?sessionId="+ss.id, map[string]string{"Authorization": "Bearer " + tok, "Content-Type": "application/json"}, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
		r.Body.Close()
		return r.StatusCode
	}
	if st := post(other); st != 401 {
		t.Fatalf("token for /sse/public on a /sse/private stream: %d, want 401", st)
	}
	if st := post(own); st != 202 {
		t.Fatalf("token for /sse/private on its own stream: %d, want 202", st)
	}
	if st := post(parent); st != 202 {
		t.Fatalf("token for /sse on a /sse/private stream: %d, want 202", st)
	}
}

package managed

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInitializeToolsListAndCall(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, err := m.Proc(fakeSpec("a"))
	if err != nil {
		t.Fatal(err)
	}
	ts := serve(t, p)
	sid := initSession(t, ts.URL, nil)
	if st := p.Status(); st.State != StateRunning || st.PID == 0 {
		t.Fatalf("status %+v", st)
	}
	r := post(t, ts.URL, sid, "", rpc(2, "tools/list", nil))
	if r.Status != 200 || !strings.Contains(string(r.Body), `"echo"`) {
		t.Fatalf("tools/list: %d %s", r.Status, r.Body)
	}
	if string(r.Events[0].ID) != "2" {
		t.Fatalf("client id not restored: %s", r.Events[0].ID)
	}
	if got := callTool(t, ts.URL, sid, "echo", map[string]any{"text": "héllo"}); got != "héllo" {
		t.Fatalf("echo: %q", got)
	}
	for _, method := range []string{"resources/list", "resources/read", "prompts/list", "prompts/get", "ping"} {
		r := post(t, ts.URL, sid, "", rpc(3, method, map[string]any{"uri": "file:///fake.txt", "name": "greet"}))
		if r.Status != 200 || r.Events[0].Error != nil {
			t.Errorf("%s: %d %s", method, r.Status, r.Body)
		}
	}
	// unknown method: the server's JSON-RPC error passes through with the client's id
	r = post(t, ts.URL, sid, "", rpc("abc", "nope/nothing", nil))
	if r.Events[0].Error == nil || r.Events[0].Error.Code != -32601 || string(r.Events[0].ID) != `"abc"` {
		t.Fatalf("error passthrough: %s", r.Body)
	}
}

func TestInitializeAnswersFromCacheAndSessionsAreIndependent(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	ts := serve(t, p)
	s1 := initSession(t, ts.URL, nil)
	pid := p.Status().PID
	s2 := initSession(t, ts.URL, nil)
	if s1 == s2 {
		t.Fatal("sessions must differ")
	}
	if p.Status().PID != pid || p.Status().Restarts != 0 {
		t.Fatal("a second initialize must not restart the process")
	}
	req, _ := http.NewRequest("DELETE", ts.URL, nil)
	req.Header.Set("Mcp-Session-Id", s1)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 204 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if r := post(t, ts.URL, s1, "", rpc(2, "tools/list", nil)); r.Status != 404 {
		t.Fatalf("ended session must be 404, got %d", r.Status)
	}
	if r := post(t, ts.URL, s2, "", rpc(2, "tools/list", nil)); r.Status != 200 {
		t.Fatalf("other session must still work, got %d", r.Status)
	}
	if r := post(t, ts.URL, "", "", rpc(2, "tools/list", nil)); r.Status != 200 {
		t.Fatalf("sessionless request: %d", r.Status)
	}
}

func TestConcurrentClientsIdRemapping(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	ts := serve(t, p)
	const clients, calls = 8, 12
	var wg sync.WaitGroup
	errs := make(chan string, clients*calls)
	for c := 0; c < clients; c++ {
		sid := initSession(t, ts.URL, nil)
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			for i := 0; i < calls; i++ {
				want := fmt.Sprintf("c%d-i%d", c, i)
				// every client uses the same request id 1 on purpose
				r := post(t, ts.URL, sid, "application/json", rpc(1, "tools/call", map[string]any{"name": "echo", "arguments": map[string]any{"text": want}}))
				if r.Status != 200 || len(r.Events) != 1 || string(r.Events[0].ID) != "1" {
					errs <- fmt.Sprintf("%s: bad reply %d %s", want, r.Status, r.Body)
					continue
				}
				var res struct{ Content []struct{ Text string } }
				json.Unmarshal(r.Events[0].Result, &res)
				if len(res.Content) == 0 || res.Content[0].Text != want {
					errs <- fmt.Sprintf("%s: got %s", want, r.Body)
				}
			}
		}(c)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	if p.Status().InFlight != 0 {
		t.Fatalf("pending calls leaked: %d", p.Status().InFlight)
	}
}

func TestSlowCallDoesNotBlockOthers(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	ts := serve(t, p)
	s1, s2 := initSession(t, ts.URL, nil), initSession(t, ts.URL, nil)
	done := make(chan string, 1)
	go func() { done <- callTool(t, ts.URL, s1, "slow", map[string]any{"ms": 600}) }()
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	callTool(t, ts.URL, s2, "echo", map[string]any{"text": "fast"})
	if time.Since(start) > 400*time.Millisecond {
		t.Fatal("a fast call waited for a slow one")
	}
	if got := <-done; got != "slow done" {
		t.Fatal(got)
	}
}

func TestBatchRequests(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	ts := serve(t, p)
	sid := initSession(t, ts.URL, nil)
	r := post(t, ts.URL, sid, "application/json", []any{
		rpc(1, "tools/call", map[string]any{"name": "echo", "arguments": map[string]any{"text": "one"}}),
		rpc("two", "ping", nil),
		rpc(nil, "notifications/initialized", nil),
	})
	var batch []rpcMsg
	if err := json.Unmarshal(r.Body, &batch); err != nil || len(batch) != 2 {
		t.Fatalf("batch reply: %d %s", r.Status, r.Body)
	}
	ids := map[string]bool{}
	for _, b := range batch {
		ids[string(b.ID)] = true
	}
	if !ids["1"] || !ids[`"two"`] {
		t.Fatalf("ids %v", ids)
	}
}

func TestProgressTokenRemapAndSSE(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	ts := serve(t, p)
	s1, s2 := initSession(t, ts.URL, nil), initSession(t, ts.URL, nil)
	var wg sync.WaitGroup
	for i, sid := range []string{s1, s2} {
		wg.Add(1)
		go func(i int, sid string) {
			defer wg.Done()
			// same token "tok" in both sessions
			r := post(t, ts.URL, sid, "application/json, text/event-stream", rpc(5, "tools/call", map[string]any{
				"name": "progress", "arguments": map[string]any{"steps": 3}, "_meta": map[string]any{"progressToken": "tok"}}))
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "text/event-stream") {
				t.Errorf("session %d: expected an event stream, got %s", i, r.Header.Get("Content-Type"))
				return
			}
			prog := 0
			for _, e := range r.Events {
				if e.Method == "notifications/progress" {
					prog++
					if !strings.Contains(string(e.Params), `"progressToken":"tok"`) {
						t.Errorf("token not restored: %s", e.Params)
					}
				}
			}
			last := r.Events[len(r.Events)-1]
			if prog != 3 || string(last.ID) != "5" || last.Result == nil {
				t.Errorf("session %d: %d progress events, last=%+v", i, prog, last)
			}
		}(i, sid)
	}
	wg.Wait()
}

func TestJSONOnlyClientGetsPlainJSONEvenWithProgress(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	ts := serve(t, p)
	sid := initSession(t, ts.URL, nil)
	r := post(t, ts.URL, sid, "application/json", rpc(5, "tools/call", map[string]any{
		"name": "progress", "arguments": map[string]any{"steps": 2}, "_meta": map[string]any{"progressToken": 9}}))
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || len(r.Events) != 1 || r.Events[0].Result == nil {
		t.Fatalf("%s %s", r.Header.Get("Content-Type"), r.Body)
	}
}

func TestSSEOnlyClientGetsEventStream(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	ts := serve(t, p)
	r := post(t, ts.URL, "", "text/event-stream", rpc(1, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}}))
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "text/event-stream") || len(r.Events) != 1 || r.Events[0].Result == nil {
		t.Fatalf("initialize as SSE: %s", r.Body)
	}
	sid := r.Header.Get("Mcp-Session-Id")
	r = post(t, ts.URL, sid, "text/event-stream", rpc(2, "tools/list", nil))
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "text/event-stream") || len(r.Events) != 1 || string(r.Events[0].ID) != "2" {
		t.Fatalf("tools/list as SSE: %s", r.Body)
	}
}

func TestServerNotificationsReachGetStream(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	ts := serve(t, p)
	s1, s2 := initSession(t, ts.URL, nil), initSession(t, ts.URL, nil)
	events := func(sid string) chan rpcMsg {
		ch := make(chan rpcMsg, 10)
		req, _ := http.NewRequest("GET", ts.URL, nil)
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Mcp-Session-Id", sid)
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("GET stream: %v", err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		go func() {
			buf := make([]byte, 4096)
			var acc string
			for {
				n, err := resp.Body.Read(buf)
				acc += string(buf[:n])
				for {
					i := strings.Index(acc, "\n\n")
					if i < 0 {
						break
					}
					blk := acc[:i]
					acc = acc[i+2:]
					if d, ok := strings.CutPrefix(blk, "event: message\ndata: "); ok {
						var m rpcMsg
						json.Unmarshal([]byte(d), &m)
						ch <- m
					}
				}
				if err != nil {
					return
				}
			}
		}()
		return ch
	}
	c1, c2 := events(s1), events(s2)
	time.Sleep(100 * time.Millisecond)
	callTool(t, ts.URL, s1, "notify", nil)
	for i, ch := range []chan rpcMsg{c1, c2} {
		got := map[string]bool{}
		timeout := time.After(3 * time.Second)
		for len(got) < 2 {
			select {
			case m := <-ch:
				got[m.Method] = true
			case <-timeout:
				t.Fatalf("stream %d got only %v", i+1, got)
			}
		}
		if !got["notifications/message"] || !got["notifications/tools/list_changed"] {
			t.Fatalf("stream %d: %v", i+1, got)
		}
	}
	if r := post(t, ts.URL, "", "text/event-stream", "x"); false {
		_ = r
	}
}

func TestGetWithoutAcceptOrSession(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	ts := serve(t, p)
	get := func(sid, accept string) int {
		req, _ := http.NewRequest("GET", ts.URL, nil)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if sid != "" {
			req.Header.Set("Mcp-Session-Id", sid)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := get("", "application/json"); c != 406 {
		t.Fatalf("no SSE accept: %d", c)
	}
	if c := get("", "text/event-stream"); c != 400 {
		t.Fatalf("no session: %d", c)
	}
	if c := get("nope", "text/event-stream"); c != 404 {
		t.Fatalf("unknown session: %d", c)
	}
}

func TestServerRequestAnsweredOnPostStream(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	ts := serve(t, p)
	sid := initSession(t, ts.URL, map[string]any{"roots": map[string]any{}})
	// open the POST stream by hand so we can answer the server request while it is open
	req, _ := http.NewRequest("POST", ts.URL, strings.NewReader(string(mustJSON(rpc(11, "tools/call", map[string]any{"name": "roots"})))))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", sid)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("a client with roots capability must get a stream: %s", resp.Header.Get("Content-Type"))
	}
	dec := newSSEReader(resp.Body)
	var srvReq rpcMsg
	for {
		m, ok := dec.next()
		if !ok {
			t.Fatal("stream ended before the server request")
		}
		if m.Method == "roots/list" {
			srvReq = m
			break
		}
	}
	var idStr string
	json.Unmarshal(srvReq.ID, &idStr)
	if !strings.HasPrefix(idStr, serverReqIDPfx) {
		t.Fatalf("server request id must be remapped, got %s", srvReq.ID)
	}
	ans := post(t, ts.URL, sid, "application/json", map[string]any{"jsonrpc": "2.0", "id": idStr, "result": map[string]any{"roots": []any{}}})
	if ans.Status != 202 {
		t.Fatalf("answer: %d %s", ans.Status, ans.Body)
	}
	for {
		m, ok := dec.next()
		if !ok {
			t.Fatal("stream ended before the response")
		}
		if string(m.ID) == "11" {
			if txt := toolText(t, m); !strings.Contains(txt, `client answered: {"roots":[]}`) {
				t.Fatalf("server did not receive the client's answer: %s", txt)
			}
			return
		}
	}
}

func TestServerRequestWithoutCapableClientIsRefused(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	ts := serve(t, p)
	sid := initSession(t, ts.URL, nil) // no roots capability
	got := callTool(t, ts.URL, sid, "roots", nil)
	if !strings.Contains(got, "client answered") && got != "no answer" {
		t.Fatalf("%q", got)
	}
	// the fake gets a JSON-RPC error response, so the "answered" text carries "null" result
	if !strings.Contains(got, "answered: ") {
		t.Fatalf("expected the server to get an immediate error answer, got %q", got)
	}
}

func TestCancellationIsForwardedWithMappedId(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	ts := serve(t, p)
	sid := initSession(t, ts.URL, nil)
	other := initSession(t, ts.URL, nil)
	done := make(chan reply, 1)
	go func() {
		done <- post(t, ts.URL, sid, "application/json", rpc(42, "tools/call", map[string]any{"name": "slow", "arguments": map[string]any{"ms": 20000}}))
	}()
	waitFor2(t, "call in flight", 3*time.Second, func() bool { return p.Status().InFlight == 1 })
	// another session cancelling the same client id must not touch this call
	post(t, ts.URL, other, "", rpc(nil, "notifications/cancelled", map[string]any{"requestId": 42}))
	time.Sleep(100 * time.Millisecond)
	if p.Status().InFlight != 1 {
		t.Fatal("a cancel from another session ended the call")
	}
	if n := post(t, ts.URL, sid, "", rpc(nil, "notifications/cancelled", map[string]any{"requestId": 42, "reason": "user"})); n.Status != 202 {
		t.Fatalf("cancel: %d", n.Status)
	}
	select {
	case r := <-done:
		if r.Events[0].Error == nil || r.Events[0].Error.Code != -32800 || string(r.Events[0].ID) != "42" {
			t.Fatalf("cancelled request answer: %s", r.Body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the cancelled request did not end")
	}
	// the child saw the cancel with the mapped (child) id, not the client's 42
	waitFor2(t, "child notified", 3*time.Second, func() bool {
		for _, l := range p.Logs(0) {
			if l.Src == "err" && strings.HasPrefix(l.Text, "fake: cancelled ") && !strings.HasSuffix(l.Text, " 42") {
				return true
			}
		}
		return false
	})
}

func TestClientDisconnectAbandonsCall(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	ts := serve(t, p)
	sid := initSession(t, ts.URL, nil)
	ctxReq, _ := http.NewRequest("POST", ts.URL, strings.NewReader(string(mustJSON(rpc(1, "tools/call", map[string]any{"name": "slow", "arguments": map[string]any{"ms": 3000}})))))
	ctxReq.Header.Set("Content-Type", "application/json")
	ctxReq.Header.Set("Accept", "application/json")
	ctxReq.Header.Set("Mcp-Session-Id", sid)
	cl := &http.Client{Timeout: 200 * time.Millisecond}
	if _, err := cl.Do(ctxReq); err == nil {
		t.Fatal("expected a client timeout")
	}
	waitFor2(t, "pending call dropped", 3*time.Second, func() bool { return p.Status().InFlight == 0 })
	// the process is still healthy and answers other calls
	if got := callTool(t, ts.URL, sid, "echo", map[string]any{"text": "still up"}); got != "still up" {
		t.Fatal(got)
	}
}

func TestBadRequests(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	ts := serve(t, p)
	sid := initSession(t, ts.URL, nil)
	for name, body := range map[string]string{"garbage": "not json", "empty batch": "[]", "no method": `{"jsonrpc":"2.0","id":1}`} {
		if r := post(t, ts.URL, sid, "", body); r.Status != 400 {
			t.Errorf("%s: status %d", name, r.Status)
		}
	}
	if r := post(t, ts.URL, "unknown-session", "", rpc(1, "tools/list", nil)); r.Status != 404 {
		t.Errorf("unknown session: %d", r.Status)
	}
	req, _ := http.NewRequest("PUT", ts.URL, nil)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 405 {
		t.Errorf("PUT: %d", resp.StatusCode)
	}
}

func TestLargeResponse(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	ts := serve(t, p)
	sid := initSession(t, ts.URL, nil)
	if got := callTool(t, ts.URL, sid, "bigtext", nil); len(got) != 300000 {
		t.Fatalf("len %d", len(got))
	}
}

// sseReader reads messages from an event stream incrementally.
type sseReader struct {
	r   interface{ Read([]byte) (int, error) }
	acc string
}

func newSSEReader(r interface{ Read([]byte) (int, error) }) *sseReader { return &sseReader{r: r} }

func (s *sseReader) next() (rpcMsg, bool) {
	buf := make([]byte, 4096)
	for {
		if i := strings.Index(s.acc, "\n\n"); i >= 0 {
			blk := s.acc[:i]
			s.acc = s.acc[i+2:]
			for _, ln := range strings.Split(blk, "\n") {
				if d, ok := strings.CutPrefix(ln, "data: "); ok {
					var m rpcMsg
					if json.Unmarshal([]byte(d), &m) == nil {
						return m, true
					}
				}
			}
			continue
		}
		n, err := s.r.Read(buf)
		s.acc += string(buf[:n])
		if err != nil && n == 0 {
			return rpcMsg{}, false
		}
	}
}

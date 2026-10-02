// Package fakemcp is a tiny stdio MCP server used by tests. A test binary re-executes itself with
// FAKE_MCP=1 (see IsChild and Run) so no external runtime is needed.
//
// Behavior is selected with environment variables:
//
//	FAKE_START_DELAY_MS  sleep before reading input (startup timeout tests)
//	FAKE_NO_INIT         never answer initialize
//	FAKE_JUNK            print non-JSON lines on stdout at startup
//	FAKE_CRASH_AFTER     exit(3) after this many tools/call requests
//	FAKE_CRASH_ON_START  exit(4) immediately
//	FAKE_STDERR          write this line to stderr at startup
//	FAKE_NAME            serverInfo.name (default fake-stdio)
//
// Tools: echo, slow, progress, notify, roots, env, pid, spawn, crash, bigtext, log.
package fakemcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// IsChild reports whether this process was started to act as the fake server.
func IsChild() bool { return os.Getenv("FAKE_MCP") == "1" }

type msg struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

type server struct {
	out       io.Writer
	wmu       sync.Mutex
	mu        sync.Mutex
	calls     int
	cancelled map[string]bool
	waiters   map[string]chan msg // responses to our own requests
	reqSeq    int
}

// Run serves MCP on in/out until in closes. It never returns when a crash option fires.
func Run(in io.Reader, out io.Writer) {
	if ms, _ := strconv.Atoi(os.Getenv("FAKE_START_DELAY_MS")); ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	if os.Getenv("FAKE_CRASH_ON_START") != "" {
		fmt.Fprintln(os.Stderr, "fake: crashing on start")
		os.Exit(4)
	}
	if v := os.Getenv("FAKE_STDERR"); v != "" {
		fmt.Fprintln(os.Stderr, v)
	}
	s := &server{out: out, waiters: map[string]chan msg{}, cancelled: map[string]bool{}}
	if os.Getenv("FAKE_JUNK") != "" {
		fmt.Fprintln(out, "this is not json")
		fmt.Fprintln(out, "{broken")
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m msg
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		go s.handle(m)
	}
}

func (s *server) send(v any) {
	b, _ := json.Marshal(v)
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.out.Write(append(b, '\n'))
}

func (s *server) reply(id json.RawMessage, result any) {
	s.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *server) fail(id json.RawMessage, code int, text string) {
	s.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": text}})
}

func (s *server) notify(method string, params any) {
	s.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (s *server) handle(m msg) {
	if m.Method == "" { // a response to one of our requests
		s.mu.Lock()
		ch := s.waiters[string(m.ID)]
		delete(s.waiters, string(m.ID))
		s.mu.Unlock()
		if ch != nil {
			ch <- m
		}
		return
	}
	hasID := len(m.ID) > 0 && string(m.ID) != "null"
	switch m.Method {
	case "initialize":
		if os.Getenv("FAKE_NO_INIT") != "" {
			return
		}
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(m.Params, &p)
		name := os.Getenv("FAKE_NAME")
		if name == "" {
			name = "fake-stdio"
		}
		s.reply(m.ID, map[string]any{
			"protocolVersion": p.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}, "resources": map[string]any{}, "prompts": map[string]any{}, "logging": map[string]any{}},
			"serverInfo":      map[string]any{"name": name, "version": "1.0"},
		})
	case "notifications/initialized":
	case "notifications/cancelled":
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		json.Unmarshal(m.Params, &p)
		s.mu.Lock()
		s.cancelled[string(p.RequestID)] = true
		s.mu.Unlock()
		fmt.Fprintln(os.Stderr, "fake: cancelled", string(p.RequestID))
	case "ping":
		if hasID {
			s.reply(m.ID, map[string]any{})
		}
	case "tools/list":
		s.reply(m.ID, map[string]any{"tools": []map[string]any{
			{"name": "echo", "description": "Echo the text argument.\nSecond line.", "inputSchema": map[string]any{"type": "object"}},
			{"name": "slow", "description": "Sleep ms then answer.", "inputSchema": map[string]any{"type": "object"}},
			{"name": "progress", "description": "Send progress notifications.", "inputSchema": map[string]any{"type": "object"}},
		}})
	case "resources/list":
		s.reply(m.ID, map[string]any{"resources": []map[string]any{{"uri": "file:///fake.txt", "name": "fake.txt"}}})
	case "resources/read":
		s.reply(m.ID, map[string]any{"contents": []map[string]any{{"uri": "file:///fake.txt", "text": "hello"}}})
	case "prompts/list":
		s.reply(m.ID, map[string]any{"prompts": []map[string]any{{"name": "greet"}}})
	case "prompts/get":
		s.reply(m.ID, map[string]any{"messages": []any{}})
	case "tools/call":
		s.call(m)
	default:
		if hasID {
			s.fail(m.ID, -32601, "method not found: "+m.Method)
		}
	}
}

func (s *server) call(m msg) {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
		Meta      struct {
			Token json.RawMessage `json:"progressToken"`
		} `json:"_meta"`
	}
	json.Unmarshal(m.Params, &p)
	s.mu.Lock()
	s.calls++
	n := s.calls
	s.mu.Unlock()
	text := func(t string) map[string]any {
		return map[string]any{"content": []map[string]any{{"type": "text", "text": t}}}
	}
	if k, _ := strconv.Atoi(os.Getenv("FAKE_CRASH_AFTER")); k > 0 && n >= k {
		fmt.Fprintln(os.Stderr, "fake: crashing after call", n)
		os.Exit(3)
	}
	switch p.Name {
	case "echo":
		t, _ := p.Arguments["text"].(string)
		s.reply(m.ID, text(t))
	case "slow":
		ms, _ := p.Arguments["ms"].(float64)
		end := time.Now().Add(time.Duration(ms) * time.Millisecond)
		for time.Now().Before(end) {
			s.mu.Lock()
			c := s.cancelled[string(m.ID)]
			s.mu.Unlock()
			if c {
				return // a cancelled request gets no response
			}
			time.Sleep(5 * time.Millisecond)
		}
		s.reply(m.ID, text("slow done"))
	case "progress":
		steps := 3
		if v, ok := p.Arguments["steps"].(float64); ok {
			steps = int(v)
		}
		for i := 1; i <= steps; i++ {
			if len(p.Meta.Token) > 0 {
				s.notify("notifications/progress", map[string]any{"progressToken": json.RawMessage(p.Meta.Token), "progress": i, "total": steps})
			}
			time.Sleep(20 * time.Millisecond)
		}
		s.reply(m.ID, text("progress done"))
	case "notify":
		s.notify("notifications/message", map[string]any{"level": "info", "data": "hello from server"})
		s.notify("notifications/tools/list_changed", map[string]any{})
		time.Sleep(50 * time.Millisecond)
		s.reply(m.ID, text("notified"))
	case "roots":
		s.mu.Lock()
		s.reqSeq++
		id := "srv-" + strconv.Itoa(s.reqSeq)
		ch := make(chan msg, 1)
		s.waiters[`"`+id+`"`] = ch
		s.mu.Unlock()
		s.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "roots/list"})
		select {
		case r := <-ch:
			s.reply(m.ID, text("client answered: "+string(r.Result)))
		case <-time.After(5 * time.Second):
			s.reply(m.ID, text("no answer"))
		}
	case "env":
		var keys []string
		for _, e := range os.Environ() {
			k, _, _ := strings.Cut(e, "=")
			keys = append(keys, k)
		}
		sort.Strings(keys)
		vals := map[string]string{}
		for _, k := range []string{"HOME", "TMPDIR", "API_TOKEN", "PATH"} {
			vals[k] = os.Getenv(k)
		}
		b, _ := json.Marshal(map[string]any{"keys": keys, "values": vals})
		wd, _ := os.Getwd()
		s.reply(m.ID, text(string(b)+"\ncwd="+wd))
	case "pid":
		s.reply(m.ID, text(strconv.Itoa(os.Getpid())))
	case "spawn":
		cmd := exec.Command("sleep", "300")
		cmd.Start()
		s.reply(m.ID, text(strconv.Itoa(cmd.Process.Pid)))
	case "bigtext":
		s.reply(m.ID, text(strings.Repeat("x", 300000)))
	case "log":
		fmt.Fprintln(os.Stderr, "fake log line from tool")
		s.reply(m.ID, text("logged"))
	case "leak":
		fmt.Fprintln(os.Stderr, "token is", os.Getenv("SECRET_TOKEN"), "ok")
		fmt.Println(`not json but has ` + os.Getenv("SECRET_TOKEN"))
		s.reply(m.ID, text("leaked"))
	case "crash":
		os.Exit(3)
	default:
		s.reply(m.ID, map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": "unknown tool " + p.Name}}})
	}
}

package suggest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// sseLLM answers with a server-sent event stream built by script. A script may block on done to stay silent.
type sseLLM struct {
	mu     sync.Mutex
	bodies []string
	script func(attempt int, body string, w *io.PipeWriter, done <-chan struct{}) (status int)
}

func (l *sseLLM) Post(context.Context, string, []byte) (int, []byte, error) {
	return 0, nil, errors.New("streaming expected")
}

func (l *sseLLM) Stream(ctx context.Context, rest string, body []byte) (*http.Response, error) {
	l.mu.Lock()
	l.bodies = append(l.bodies, string(body))
	n := len(l.bodies)
	l.mu.Unlock()
	pr, pw := io.Pipe()
	hdr := http.Header{"Content-Type": {"text/event-stream"}}
	go func() {
		l.script(n, string(body), pw, ctx.Done())
		pw.Close()
	}()
	return &http.Response{StatusCode: 200, Header: hdr, Body: &ctxBody{pr, ctx}}, nil
}

// ctxBody makes a blocked read return when the request context ends, like a real connection.
type ctxBody struct {
	io.ReadCloser
	ctx context.Context
}

func (b *ctxBody) Read(p []byte) (int, error) {
	type res struct {
		n   int
		err error
	}
	c := make(chan res, 1)
	go func() { n, err := b.ReadCloser.Read(p); c <- res{n, err} }()
	select {
	case r := <-c:
		return r.n, r.err
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	}
}

func delta(s string) string {
	b, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": s}}}})
	return "data: " + string(b) + "\n\n"
}

func streamService(t *testing.T, l Completer) *Service {
	svc := npmService(t, &mockLLM{})
	svc.LLM = l
	svc.Logf = t.Logf
	return svc
}

func TestSuggestStreamsAndSendsEffort(t *testing.T) {
	reply := goodReply()
	l := &sseLLM{script: func(_ int, _ string, w *io.PipeWriter, _ <-chan struct{}) int {
		io.WriteString(w, ": keep-alive\n\n")
		half := len(reply) / 2
		io.WriteString(w, delta(reply[:half]))
		time.Sleep(450 * time.Millisecond)
		io.WriteString(w, delta(reply[half:]))
		io.WriteString(w, "data: [DONE]\n\n")
		return 200
	}}
	svc := streamService(t, l)
	svc.Effort = "low"
	var events []Event
	svc.Progress = func(e Event) { events = append(events, e) }
	var logs []string
	svc.Logf = func(f string, a ...any) { logs = append(logs, f) }
	src, _ := ParseSource("mcp-thing")
	res, err := svc.Suggest(context.Background(), "m1", src, "", runners)
	if err != nil || res.Command != "npx" {
		t.Fatalf("%v %+v", err, res)
	}
	var req map[string]any
	json.Unmarshal([]byte(l.bodies[0]), &req)
	if req["reasoning_effort"] != "low" || req["stream"] != true {
		t.Fatalf("effort/stream missing: %s", l.bodies[0][:200])
	}
	chars := 0
	for _, e := range events {
		if e.Stage == StageModel && e.Chars > chars {
			chars = e.Chars
		}
	}
	if chars == 0 {
		t.Fatalf("no activity events: %+v", events)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "first token") {
		t.Fatalf("time to first token not logged: %v", logs)
	}
}

func TestSuggestNoEffortSentWhenDefault(t *testing.T) {
	l := &sseLLM{script: func(_ int, _ string, w *io.PipeWriter, _ <-chan struct{}) int {
		io.WriteString(w, delta(goodReply()))
		return 200
	}}
	svc := streamService(t, l)
	src, _ := ParseSource("mcp-thing")
	if _, err := svc.Suggest(context.Background(), "m1", src, "", runners); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(l.bodies[0], "reasoning_effort") {
		t.Fatalf("effort sent although unset: %s", l.bodies[0][:200])
	}
}

func TestSuggestIdleTimeout(t *testing.T) {
	l := &sseLLM{script: func(_ int, _ string, w *io.PipeWriter, done <-chan struct{}) int {
		io.WriteString(w, delta(`{"alias":`))
		<-done
		return 200
	}}
	svc := streamService(t, l)
	svc.Idle = 80 * time.Millisecond
	src, _ := ParseSource("mcp-thing")
	_, err := svc.Suggest(context.Background(), "m1", src, "", runners)
	var te *TimeoutError
	if !errors.As(err, &te) || te.Kind != "idle" || te.Stage != StageModel || te.Where() != "asking the model" {
		t.Fatalf("%T %v", err, err)
	}
	if !strings.Contains(err.Error(), "lower the effort") || !strings.Contains(err.Error(), "no data") {
		t.Fatalf("reason: %v", err)
	}
}

func TestSuggestOverallCap(t *testing.T) {
	l := &sseLLM{script: func(_ int, _ string, w *io.PipeWriter, done <-chan struct{}) int {
		for {
			select {
			case <-done:
				return 200
			case <-time.After(20 * time.Millisecond):
				io.WriteString(w, delta("x")) // chatty, never finishes
			}
		}
	}}
	svc := streamService(t, l)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	src, _ := ParseSource("mcp-thing")
	_, err := svc.Suggest(ctx, "m1", src, "", runners)
	var te *TimeoutError
	if !errors.As(err, &te) || te.Kind != "cap" || te.Stage != StageModel {
		t.Fatalf("%T %v", err, err)
	}
}

type rejectLLM struct {
	sseLLM
	status int
	msg    string
}

func (r *rejectLLM) Stream(ctx context.Context, rest string, body []byte) (*http.Response, error) {
	r.mu.Lock()
	r.bodies = append(r.bodies, string(body))
	r.mu.Unlock()
	if strings.Contains(string(body), "reasoning_effort") {
		return &http.Response{StatusCode: r.status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(r.msg))}, nil
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(chat(goodReply())))}, nil
}

func TestSuggestRetriesWithoutRejectedEffort(t *testing.T) {
	l := &rejectLLM{status: 400, msg: `{"error":{"message":"model does not support reasoning_effort"}}`}
	svc := streamService(t, l)
	svc.Effort = "high"
	src, _ := ParseSource("mcp-thing")
	if _, err := svc.Suggest(context.Background(), "m1", src, "", runners); err != nil {
		t.Fatal(err)
	}
	if len(l.bodies) != 2 || !strings.Contains(l.bodies[0], "reasoning_effort") || strings.Contains(l.bodies[1], "reasoning_effort") {
		t.Fatalf("expected one retry without effort, got %d bodies", len(l.bodies))
	}
}

func TestTimeoutErrorText(t *testing.T) {
	e := &TimeoutError{Kind: "cap", Stage: StageFetch, After: 300 * time.Second}
	if e.Error() != "timed out while fetching the repo after 300s" {
		t.Fatal(e.Error())
	}
}

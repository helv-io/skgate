package provider

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestParseUsageJSON(t *testing.T) {
	for name, c := range map[string]struct {
		in   string
		want Tokens
		ok   bool
	}{
		"chat":        {`{"id":"x","choices":[],"usage":{"prompt_tokens":19,"completion_tokens":4,"total_tokens":23,"prompt_tokens_details":{"cached_tokens":1}}}`, Tokens{19, 4, 23}, true},
		"embeddings":  {`{"data":[],"usage":{"prompt_tokens":8,"total_tokens":8}}`, Tokens{8, 0, 8}, true},
		"responses":   {`{"id":"r","usage":{"input_tokens":5,"output_tokens":7,"total_tokens":12}}`, Tokens{5, 7, 12}, true},
		"stream-done": {`{"type":"response.completed","response":{"usage":{"input_tokens":3,"output_tokens":4}}}`, Tokens{3, 4, 7}, true},
		"no total":    {`{"usage":{"prompt_tokens":2,"completion_tokens":3}}`, Tokens{2, 3, 5}, true},
		"null":        {`{"choices":[{"delta":{}}],"usage":null}`, Tokens{}, false},
		"absent":      {`{"model":"echo","ok":true}`, Tokens{}, false},
		"empty usage": {`{"usage":{}}`, Tokens{}, false},
		"negative":    {`{"usage":{"prompt_tokens":-1}}`, Tokens{}, false},
		"not json":    {`data`, Tokens{}, false},
		"error body":  {`{"error":{"message":"x"}}`, Tokens{}, false},
	} {
		got, ok := ParseUsage([]byte(c.in))
		if ok != c.ok || got != c.want {
			t.Errorf("%s: %+v %v, want %+v %v", name, got, ok, c.want, c.ok)
		}
	}
}

const sseStream = "" +
	"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}],\"usage\":null}\n\n" +
	"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":171,\"completion_tokens\":2,\"total_tokens\":173}}\r\n\r\n" +
	"data: {\"choices\":[],\"usage\":null}\n\n" +
	"data: [DONE]\n\n"

// The scanner gives the same answer however the stream is cut into reads.
func TestSSEUsageAnyChunking(t *testing.T) {
	for _, size := range []int{1, 3, 7, 64, len(sseStream)} {
		s := &sseUsage{}
		for i := 0; i < len(sseStream); i += size {
			s.Write([]byte(sseStream[i:min(i+size, len(sseStream))]))
		}
		if got, ok := s.result(); !ok || got != (Tokens{171, 2, 173}) {
			t.Errorf("chunks of %d: %+v %v", size, got, ok)
		}
	}
}

func TestSSEUsageEdgeCases(t *testing.T) {
	s := &sseUsage{}
	s.Write([]byte("data: {\"choices\":[]}\n\ndata: [DONE]\n\n"))
	if _, ok := s.result(); ok {
		t.Error("a stream without usage must report none")
	}
	s = &sseUsage{}
	s.Write([]byte(": ping\nevent: x\ndata: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}")) // no final newline
	if got, ok := s.result(); !ok || got.Total != 2 {
		t.Errorf("last line without newline: %+v %v", got, ok)
	}
	s = &sseUsage{}
	s.Write([]byte("data: {\"x\":\"" + strings.Repeat("a", maxSSELine+10) + "\",\"usage\":{\"total_tokens\":9}}\n"))
	s.Write([]byte("data: {\"usage\":{\"total_tokens\":4}}\n"))
	if got, ok := s.result(); !ok || got.Total != 4 {
		t.Errorf("an oversized line is skipped, the next one still counts: %+v %v", got, ok)
	}
	s = &sseUsage{}
	s.Write([]byte("data: {\"response\":{\"usage\":{\"input_tokens\":2,\"output_tokens\":6}}}\n"))
	if got, ok := s.result(); !ok || got != (Tokens{2, 6, 8}) {
		t.Errorf("responses event: %+v %v", got, ok)
	}
}

func TestJSONUsageBounded(t *testing.T) {
	j := &jsonUsage{}
	j.Write([]byte(`{"usage":{"total_tokens":1},"pad":"`))
	j.Write([]byte(strings.Repeat("a", maxUsageBody)))
	j.Write([]byte(`"}`))
	if _, ok := j.result(); ok || j.buf.Len() != 0 {
		t.Error("a body over the limit is not buffered or scanned")
	}
}

func usageOfRig(t *testing.T, r *rig) (u struct {
	P, C, T, Req int64
}) {
	t.Helper()
	ks, err := r.keys.List()
	if err != nil || len(ks) != 1 {
		t.Fatalf("keys: %v %d", err, len(ks))
	}
	x := ks[0].Usage
	u.P, u.C, u.T, u.Req = x.PromptTokens, x.CompletionTokens, x.TotalTokens, x.Requests
	return u
}

func TestProxyCountsJSONAndStreamingUsage(t *testing.T) {
	r := newRig(t, func(w http.ResponseWriter, req *http.Request, body string) {
		switch {
		case req.URL.Path == "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, upModels)
		case strings.Contains(body, `"stream":true`):
			w.Header().Set("Content-Type", "text/event-stream")
			fl := w.(http.Flusher)
			for _, part := range strings.SplitAfter(sseStream, "\n\n") {
				io.WriteString(w, part)
				fl.Flush()
			}
		case strings.Contains(body, "fail"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(500)
			io.WriteString(w, `{"usage":{"total_tokens":999}}`)
		case strings.Contains(body, "nousage"):
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"ok":true}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
		}
	})
	if c, _, _ := r.do(t, "GET", "/v1/models", ""); c != 200 {
		t.Fatal(c)
	}
	if u := usageOfRig(t, r); u.Req != 0 || u.T != 0 {
		t.Fatalf("a model list is not a counted request: %+v", u)
	}
	r.do(t, "POST", "/v1/chat/completions", `{"model":"m"}`)
	_, body, _ := r.do(t, "POST", "/v1/chat/completions", `{"model":"m","stream":true}`)
	if body != sseStream {
		t.Fatalf("stream must pass through unchanged:\n%q", body)
	}
	r.do(t, "POST", "/v1/chat/completions", `{"model":"nousage"}`)
	r.do(t, "POST", "/v1/chat/completions", `{"model":"fail"}`)
	u := usageOfRig(t, r)
	if u.P != 181 || u.C != 7 || u.T != 188 || u.Req != 3 {
		t.Fatalf("JSON 10/5/15 + stream 171/2/173, a no-usage call counted as a request, the failed one not at all: %+v", u)
	}
}

func TestProxyForwardsRequestUnchangedForUsage(t *testing.T) {
	r := newRig(t, nil)
	in := `{"model":"m","stream":true}`
	r.do(t, "POST", "/v1/chat/completions", in)
	if _, _, _, b := r.last(); b != in {
		t.Fatalf("the request body must not be altered to get usage: %q", b)
	}
}

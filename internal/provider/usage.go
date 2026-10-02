package provider

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
)

// Tokens is a usage object as the provider reported it. Nothing is estimated: a response without
// a usage object yields no tokens.
type Tokens struct{ Prompt, Completion, Total int64 }

// maxUsageBody bounds a buffered JSON response while it is scanned for usage; bigger bodies are not scanned.
const maxUsageBody = 4 << 20

// maxSSELine bounds one buffered SSE line; longer lines are skipped.
const maxSSELine = 1 << 20

type usageObj struct {
	PromptTokens     *int64 `json:"prompt_tokens"`
	CompletionTokens *int64 `json:"completion_tokens"`
	InputTokens      *int64 `json:"input_tokens"`
	OutputTokens     *int64 `json:"output_tokens"`
	TotalTokens      *int64 `json:"total_tokens"`
}

func (u usageObj) tokens() (Tokens, bool) {
	pick := func(a, b *int64) int64 {
		switch {
		case a != nil:
			return *a
		case b != nil:
			return *b
		}
		return 0
	}
	t := Tokens{Prompt: pick(u.PromptTokens, u.InputTokens), Completion: pick(u.CompletionTokens, u.OutputTokens)}
	if u.TotalTokens != nil {
		t.Total = *u.TotalTokens
	} else {
		t.Total = t.Prompt + t.Completion
	}
	if t.Prompt < 0 || t.Completion < 0 || t.Total < 0 {
		return Tokens{}, false
	}
	return t, u.PromptTokens != nil || u.CompletionTokens != nil || u.InputTokens != nil || u.OutputTokens != nil || u.TotalTokens != nil
}

// ParseUsage reads the usage object of one JSON value: a chat completion or embeddings body, a
// stream chunk ("usage" at the top, null in all but the last), or a Responses API event ("response.usage").
// Prompt/completion and input/output names are the same thing. ok is false when there is no usage.
func ParseUsage(raw []byte) (Tokens, bool) {
	var top struct {
		Usage    *usageObj `json:"usage"`
		Response *struct {
			Usage *usageObj `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &top) != nil {
		return Tokens{}, false
	}
	if top.Usage != nil {
		return top.Usage.tokens()
	}
	if top.Response != nil && top.Response.Usage != nil {
		return top.Response.Usage.tokens()
	}
	return Tokens{}, false
}

// sseUsage scans an event stream as it passes and keeps the last usage it saw (the final chunk
// carries the totals). It keeps at most one line in memory.
type sseUsage struct {
	line []byte
	skip bool // inside a line longer than maxSSELine
	last Tokens
	seen bool
}

func (s *sseUsage) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			s.add(p)
			break
		}
		s.add(p[:i])
		s.flushLine()
		p = p[i+1:]
	}
	return n, nil
}

func (s *sseUsage) add(b []byte) {
	if s.skip {
		return
	}
	if len(s.line)+len(b) > maxSSELine {
		s.line, s.skip = s.line[:0], true
		return
	}
	s.line = append(s.line, b...)
}

func (s *sseUsage) flushLine() {
	line := bytes.TrimSuffix(s.line, []byte("\r"))
	if !s.skip && bytes.HasPrefix(line, []byte("data:")) && bytes.Contains(line, []byte(`"usage"`)) {
		if t, ok := ParseUsage(bytes.TrimSpace(line[len("data:"):])); ok {
			s.last, s.seen = t, true
		}
	}
	s.line, s.skip = s.line[:0], false
}

func (s *sseUsage) result() (Tokens, bool) {
	if len(s.line) > 0 { // stream ended without a final newline
		s.flushLine()
	}
	return s.last, s.seen
}

// jsonUsage buffers a JSON response (up to maxUsageBody) so its usage can be read after it was relayed.
type jsonUsage struct {
	buf  bytes.Buffer
	over bool
}

func (j *jsonUsage) Write(p []byte) (int, error) {
	if !j.over {
		if j.buf.Len()+len(p) > maxUsageBody {
			j.over = true
			j.buf.Reset()
		} else {
			j.buf.Write(p)
		}
	}
	return len(p), nil
}

func (j *jsonUsage) result() (Tokens, bool) {
	if j.over {
		return Tokens{}, false
	}
	return ParseUsage(j.buf.Bytes())
}

// usageTap copies what a response body yields into a scanner. The response is relayed unchanged.
type usageTap struct {
	rc  io.ReadCloser
	sc  io.Writer
	res func() (Tokens, bool)
}

func (t *usageTap) Read(p []byte) (int, error) {
	n, err := t.rc.Read(p)
	if n > 0 {
		_, _ = t.sc.Write(p[:n])
	}
	return n, err
}

func (t *usageTap) Close() error { return t.rc.Close() }

// tapUsage wraps resp.Body so usage can be read once the body was relayed. It returns nil when the
// response is not a plain JSON body or an event stream (then nothing is scanned).
func tapUsage(resp *http.Response) *usageTap {
	if resp.Header.Get("Content-Encoding") != "" {
		return nil
	}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	var t usageTap
	switch {
	case mt == "text/event-stream":
		s := &sseUsage{}
		t.sc, t.res = s, s.result
	case mt == "application/json" || len(mt) > 5 && mt[len(mt)-5:] == "+json":
		j := &jsonUsage{}
		t.sc, t.res = j, j.result
	default:
		return nil
	}
	t.rc = resp.Body
	resp.Body = &t
	return &t
}

// Result is the usage seen so far.
func (t *usageTap) Result() (Tokens, bool) {
	if t == nil {
		return Tokens{}, false
	}
	return t.res()
}

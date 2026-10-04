package keyed

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Anthropic is the Anthropic provider. Its API is not OpenAI compatible, so it answers the proxy's OpenAI-style
// requests itself: /chat/completions becomes a Messages request and the answer (also a stream) is translated back.
// The model list needs no translation.
type Anthropic struct{ *Provider }

const anthropicVersion = "2023-06-01"

var anthropicClient = &http.Client{Timeout: 0} // streams run as long as the model does; callers set a context

func (a *Anthropic) base() string {
	if v, ok := a.DB.GetSetting("provider." + a.Preset.ID + ".base"); ok && strings.TrimSpace(v) != "" {
		return strings.TrimRight(v, "/")
	}
	return strings.TrimRight(a.Preset.Base, "/")
}

// RoundTrip implements the proxy's Transport.
func (a *Anthropic) RoundTrip(ctx context.Context, method, rest, rawQuery string, hdr http.Header, body []byte) (*http.Response, error) {
	key, err := a.Token(ctx)
	if err != nil {
		return nil, err
	}
	switch {
	case method == http.MethodGet && strings.TrimRight(rest, "/") == "/models":
		q := url.Values{"limit": {"1000"}}.Encode()
		return a.call(ctx, http.MethodGet, "/models?"+q, key, nil, nil)
	case method == http.MethodPost && strings.TrimRight(rest, "/") == "/chat/completions":
		var req map[string]json.RawMessage
		if json.Unmarshal(body, &req) != nil {
			return synth(http.StatusBadRequest, errBody("invalid_request_error", "the request body is not valid JSON")), nil
		}
		msg, stream, model := toMessages(req)
		out, _ := json.Marshal(msg)
		resp, err := a.call(ctx, http.MethodPost, "/messages", key, out, hdr)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			defer resp.Body.Close()
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			return synth(resp.StatusCode, errBody(errType(resp.StatusCode), upstreamMessage(raw, resp.Status))), nil
		}
		if stream {
			return streamResponse(resp, model), nil
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if err != nil {
			return nil, err
		}
		b, _ := json.Marshal(fromMessage(raw, model))
		return synth(http.StatusOK, b), nil
	}
	return synth(http.StatusNotFound, errBody("invalid_request_error", "this provider supports /models and /chat/completions")), nil
}

func (a *Anthropic) call(ctx context.Context, method, path, key string, body []byte, hdr http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, a.base()+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if hdr != nil && hdr.Get("Accept") == "text/event-stream" {
		req.Header.Set("Accept", "text/event-stream")
	}
	return anthropicClient.Do(req)
}

func synth(status int, body []byte) *http.Response {
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}
}

func errBody(typ, msg string) []byte {
	b, _ := json.Marshal(map[string]any{"error": map[string]any{"message": msg, "type": typ}})
	return b
}

func errType(status int) string {
	switch status {
	case 401, 403:
		return "authentication_error"
	case 429:
		return "rate_limit_error"
	case 404:
		return "not_found_error"
	}
	if status >= 500 {
		return "api_error"
	}
	return "invalid_request_error"
}

func upstreamMessage(raw []byte, fallback string) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return fallback
}

// ---- request: OpenAI chat completion -> Anthropic message ----

type block = map[string]any

// toMessages translates an OpenAI chat request body. It returns the Messages request, whether the client asked for
// a stream and the model name to echo back.
func toMessages(req map[string]json.RawMessage) (msg map[string]any, stream bool, model string) {
	msg = map[string]any{}
	_ = json.Unmarshal(req["model"], &model)
	msg["model"] = model
	_ = json.Unmarshal(req["stream"], &stream)
	if stream {
		msg["stream"] = true
	}
	max := 4096
	for _, k := range []string{"max_completion_tokens", "max_tokens"} {
		var n int
		if json.Unmarshal(req[k], &n) == nil && n > 0 {
			max = n
			break
		}
	}
	msg["max_tokens"] = max
	for _, k := range []string{"temperature", "top_p"} {
		var f float64
		if json.Unmarshal(req[k], &f) == nil && req[k] != nil {
			msg[k] = f
		}
	}
	var stop []string
	if json.Unmarshal(req["stop"], &stop) != nil {
		var one string
		if json.Unmarshal(req["stop"], &one) == nil && one != "" {
			stop = []string{one}
		}
	}
	if len(stop) > 0 {
		msg["stop_sequences"] = stop
	}

	var in []struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		ToolCalls  []toolCall      `json:"tool_calls"`
		ToolCallID string          `json:"tool_call_id"`
	}
	_ = json.Unmarshal(req["messages"], &in)
	var system []string
	var out []map[string]any
	push := func(role string, blocks ...block) {
		if len(blocks) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1]["role"] == role { // Anthropic wants alternating roles
			out[n-1]["content"] = append(out[n-1]["content"].([]block), blocks...)
			return
		}
		out = append(out, map[string]any{"role": role, "content": blocks})
	}
	for _, m := range in {
		switch m.Role {
		case "system", "developer":
			if t := textOf(m.Content); t != "" {
				system = append(system, t)
			}
		case "assistant":
			var bs []block
			bs = append(bs, contentBlocks(m.Content)...)
			for _, tc := range m.ToolCalls {
				var input any = map[string]any{}
				if tc.Function.Arguments != "" {
					if json.Unmarshal([]byte(tc.Function.Arguments), &input) != nil {
						input = map[string]any{}
					}
				}
				bs = append(bs, block{"type": "tool_use", "id": tc.ID, "name": tc.Function.Name, "input": input})
			}
			push("assistant", bs...)
		case "tool":
			push("user", block{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": textOf(m.Content)})
		default:
			push("user", contentBlocks(m.Content)...)
		}
	}
	if len(out) == 0 {
		out = append(out, map[string]any{"role": "user", "content": []block{{"type": "text", "text": "."}}})
	}
	msg["messages"] = out
	if len(system) > 0 {
		msg["system"] = strings.Join(system, "\n\n")
	}

	var tools []struct {
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	}
	if json.Unmarshal(req["tools"], &tools) == nil && len(tools) > 0 {
		var ts []block
		for _, t := range tools {
			var schema any = map[string]any{"type": "object"}
			if len(t.Function.Parameters) > 0 {
				_ = json.Unmarshal(t.Function.Parameters, &schema)
			}
			ts = append(ts, block{"name": t.Function.Name, "description": t.Function.Description, "input_schema": schema})
		}
		msg["tools"] = ts
		var choice any
		if json.Unmarshal(req["tool_choice"], &choice) == nil {
			switch c := choice.(type) {
			case string:
				switch c {
				case "required":
					msg["tool_choice"] = map[string]any{"type": "any"}
				case "none":
					delete(msg, "tools")
				default:
					msg["tool_choice"] = map[string]any{"type": "auto"}
				}
			case map[string]any:
				if f, ok := c["function"].(map[string]any); ok {
					msg["tool_choice"] = map[string]any{"type": "tool", "name": f["name"]}
				}
			}
		}
	}
	return msg, stream, model
}

type toolCall struct {
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// textOf joins the text of a message content, which is a string or a list of parts.
func textOf(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	var b []string
	if json.Unmarshal(raw, &parts) == nil {
		for _, p := range parts {
			if p.Text != "" {
				b = append(b, p.Text)
			}
		}
	}
	return strings.Join(b, "\n")
}

// contentBlocks turns an OpenAI message content into Anthropic blocks: text, and images (data: URLs are sent
// inline, other URLs by reference).
func contentBlocks(raw json.RawMessage) []block {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "" {
			return nil
		}
		return []block{{"type": "text", "text": s}}
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return nil
	}
	var out []block
	for _, p := range parts {
		switch p.Type {
		case "text":
			if p.Text != "" {
				out = append(out, block{"type": "text", "text": p.Text})
			}
		case "image_url":
			u := p.ImageURL.URL
			if rest, ok := strings.CutPrefix(u, "data:"); ok {
				if meta, data, ok := strings.Cut(rest, ","); ok {
					out = append(out, block{"type": "image", "source": map[string]any{"type": "base64", "media_type": strings.TrimSuffix(meta, ";base64"), "data": data}})
				}
			} else if u != "" {
				out = append(out, block{"type": "image", "source": map[string]any{"type": "url", "url": u}})
			}
		}
	}
	return out
}

// ---- response: Anthropic message -> OpenAI chat completion ----

func finishReason(stop string) string {
	switch stop {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "refusal":
		return "content_filter"
	}
	return "stop"
}

func fromMessage(raw []byte, model string) map[string]any {
	var m struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			In  int `json:"input_tokens"`
			Out int `json:"output_tokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(raw, &m)
	if m.Model != "" {
		model = m.Model
	}
	var text strings.Builder
	var calls []map[string]any
	for _, c := range m.Content {
		switch c.Type {
		case "text":
			text.WriteString(c.Text)
		case "tool_use":
			args := string(c.Input)
			if args == "" || args == "null" {
				args = "{}"
			}
			calls = append(calls, map[string]any{"id": c.ID, "type": "function", "function": map[string]any{"name": c.Name, "arguments": args}})
		}
	}
	message := map[string]any{"role": "assistant", "content": text.String()}
	if len(calls) > 0 {
		message["tool_calls"] = calls
		if text.Len() == 0 {
			message["content"] = nil
		}
	}
	return map[string]any{
		"id": "chatcmpl-" + strings.TrimPrefix(m.ID, "msg_"), "object": "chat.completion", "created": time.Now().Unix(), "model": model,
		"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finishReason(m.StopReason)}},
		"usage":   usage(m.Usage.In, m.Usage.Out),
	}
}

func usage(in, out int) map[string]any {
	return map[string]any{"prompt_tokens": in, "completion_tokens": out, "total_tokens": in + out}
}

// ---- streaming ----

// streamResponse reads Anthropic's server-sent events and writes OpenAI chunks. The last chunk before [DONE]
// carries the finish reason and the usage, so skgate's usage counters work.
func streamResponse(resp *http.Response, model string) *http.Response {
	pr, pw := io.Pipe()
	go func() {
		defer resp.Body.Close()
		err := translateStream(resp.Body, pw, model)
		pw.CloseWithError(err)
	}()
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": {"text/event-stream"}, "Cache-Control": {"no-cache"}}, Body: pr}
}

func translateStream(r io.Reader, w io.Writer, model string) error {
	id := "chatcmpl-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	created := time.Now().Unix()
	send := func(delta map[string]any, finish any, u map[string]any) error {
		chunk := map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
		if u != nil {
			chunk["usage"] = u
		}
		b, _ := json.Marshal(chunk)
		_, err := w.Write(append(append([]byte("data: "), b...), '\n', '\n'))
		return err
	}
	var in, out int
	finish := "stop"
	tools := 0 // index of the next tool call in the OpenAI stream
	toolOf := map[int]int{}

	dec := newSSE(r)
	for {
		ev, data, err := dec.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		var e struct {
			Index   int `json:"index"`
			Message struct {
				ID    string `json:"id"`
				Model string `json:"model"`
				Usage struct {
					In  int `json:"input_tokens"`
					Out int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				Out int `json:"output_tokens"`
			} `json:"usage"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(data), &e) != nil {
			continue
		}
		switch ev {
		case "message_start":
			in, out = e.Message.Usage.In, e.Message.Usage.Out
			if e.Message.ID != "" {
				id = "chatcmpl-" + strings.TrimPrefix(e.Message.ID, "msg_")
			}
			if e.Message.Model != "" {
				model = e.Message.Model
			}
			if err := send(map[string]any{"role": "assistant", "content": ""}, nil, nil); err != nil {
				return err
			}
		case "content_block_start":
			if e.ContentBlock.Type == "tool_use" {
				toolOf[e.Index] = tools
				call := map[string]any{"index": tools, "id": e.ContentBlock.ID, "type": "function", "function": map[string]any{"name": e.ContentBlock.Name, "arguments": ""}}
				tools++
				if err := send(map[string]any{"tool_calls": []any{call}}, nil, nil); err != nil {
					return err
				}
			}
		case "content_block_delta":
			switch e.Delta.Type {
			case "text_delta":
				if err := send(map[string]any{"content": e.Delta.Text}, nil, nil); err != nil {
					return err
				}
			case "input_json_delta":
				call := map[string]any{"index": toolOf[e.Index], "function": map[string]any{"arguments": e.Delta.PartialJSON}}
				if err := send(map[string]any{"tool_calls": []any{call}}, nil, nil); err != nil {
					return err
				}
			}
		case "message_delta":
			finish = finishReason(e.Delta.StopReason)
			if e.Usage.Out > 0 {
				out = e.Usage.Out
			}
		case "error":
			b, _ := json.Marshal(map[string]any{"error": map[string]any{"message": e.Error.Message, "type": "api_error"}})
			_, err := w.Write(append(append([]byte("data: "), b...), '\n', '\n'))
			return err
		}
	}
	if err := send(map[string]any{}, finish, usage(in, out)); err != nil {
		return err
	}
	_, err := w.Write([]byte("data: [DONE]\n\n"))
	return err
}

// sse reads server-sent events: an optional "event:" line and the "data:" lines up to a blank line.
type sse struct {
	r   io.Reader
	buf []byte
	err error // the read error, kept until the buffered lines are used
}

func newSSE(r io.Reader) *sse { return &sse{r: r} }

func (s *sse) readLine() (string, error) {
	for {
		if i := bytes.IndexByte(s.buf, '\n'); i >= 0 {
			line := string(bytes.TrimRight(s.buf[:i], "\r"))
			s.buf = s.buf[i+1:]
			return line, nil
		}
		if s.err != nil {
			if len(s.buf) > 0 && s.err == io.EOF {
				line := string(s.buf)
				s.buf = nil
				return line, nil
			}
			return "", s.err
		}
		chunk := make([]byte, 4096)
		n, err := s.r.Read(chunk)
		s.buf = append(s.buf, chunk[:n]...)
		s.err = err
	}
}

func (s *sse) next() (event, data string, err error) {
	var d []string
	for {
		line, err := s.readLine()
		if err != nil {
			if err == io.EOF && len(d) > 0 {
				return event, strings.Join(d, "\n"), nil
			}
			return "", "", err
		}
		switch {
		case line == "":
			if len(d) > 0 {
				return event, strings.Join(d, "\n"), nil
			}
			event = ""
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			d = append(d, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
}

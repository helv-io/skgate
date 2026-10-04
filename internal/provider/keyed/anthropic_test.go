package keyed

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fake is an Anthropic API: it records the last request and answers with reply.
type fake struct {
	srv                *httptest.Server
	path, key, version string
	body               map[string]any
}

func newFake(t *testing.T, reply func(w http.ResponseWriter)) *fake {
	f := &fake{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.path, f.key, f.version = r.URL.RequestURI(), r.Header.Get("x-api-key"), r.Header.Get("anthropic-version")
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &f.body)
		reply(w)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func anthropic(t *testing.T, f *fake) *Anthropic {
	db := open(t)
	p := get(t, db, "anthropic")
	p.SetEnabled(true)
	p.SaveKey("sk-ant-key")
	db.SetSetting("provider.anthropic.base", f.srv.URL+"/v1")
	return &Anthropic{Provider: p}
}

const chat = `{"model":"claude-x","max_tokens":50,"temperature":0.2,"stop":"END",
 "messages":[{"role":"system","content":"be brief"},
  {"role":"user","content":[{"type":"text","text":"hi"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]},
  {"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"now","arguments":"{\"tz\":\"UTC\"}"}}]},
  {"role":"tool","tool_call_id":"c1","content":"12:00"}],
 "tools":[{"type":"function","function":{"name":"now","description":"time","parameters":{"type":"object","properties":{"tz":{"type":"string"}}}}}],
 "tool_choice":"required"}`

func TestChatRequestIsTranslated(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter) {
		io.WriteString(w, `{"id":"msg_1","model":"claude-x","content":[{"type":"text","text":"hello"},{"type":"tool_use","id":"t1","name":"now","input":{"tz":"UTC"}}],"stop_reason":"tool_use","usage":{"input_tokens":7,"output_tokens":3}}`)
	})
	a := anthropic(t, f)
	resp, err := a.RoundTrip(context.Background(), "POST", "/chat/completions", "", nil, []byte(chat))
	if err != nil {
		t.Fatal(err)
	}
	if f.path != "/v1/messages" || f.key != "sk-ant-key" || f.version != anthropicVersion {
		t.Fatalf("upstream saw %s key=%q version=%q", f.path, f.key, f.version)
	}
	b := f.body
	if b["system"] != "be brief" || b["max_tokens"].(float64) != 50 || b["temperature"].(float64) != 0.2 {
		t.Fatalf("%v", b)
	}
	if s := b["stop_sequences"].([]any); len(s) != 1 || s[0] != "END" {
		t.Fatalf("%v", b["stop_sequences"])
	}
	msgs := b["messages"].([]any)
	if len(msgs) != 3 { // user, assistant (tool_use), user (tool_result)
		t.Fatalf("messages: %v", msgs)
	}
	user := msgs[0].(map[string]any)["content"].([]any)
	if user[1].(map[string]any)["type"] != "image" || user[1].(map[string]any)["source"].(map[string]any)["media_type"] != "image/png" {
		t.Fatalf("image: %v", user)
	}
	tu := msgs[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if tu["type"] != "tool_use" || tu["id"] != "c1" || tu["input"].(map[string]any)["tz"] != "UTC" {
		t.Fatalf("tool_use: %v", tu)
	}
	tr := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if tr["type"] != "tool_result" || tr["tool_use_id"] != "c1" || tr["content"] != "12:00" {
		t.Fatalf("tool_result: %v", tr)
	}
	tool := b["tools"].([]any)[0].(map[string]any)
	if tool["name"] != "now" || tool["input_schema"] == nil || b["tool_choice"].(map[string]any)["type"] != "any" {
		t.Fatalf("tools: %v %v", tool, b["tool_choice"])
	}

	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		Object  string `json:"object"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct{ Name, Arguments string }
				} `json:"tool_calls"`
			}
		}
		Usage struct{ Prompt_tokens, Completion_tokens, Total_tokens int }
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err, string(raw))
	}
	c := out.Choices[0]
	if out.Object != "chat.completion" || c.FinishReason != "tool_calls" || c.Message.Content != "hello" ||
		c.Message.ToolCalls[0].ID != "t1" || c.Message.ToolCalls[0].Function.Name != "now" || !strings.Contains(c.Message.ToolCalls[0].Function.Arguments, "UTC") ||
		out.Usage.Prompt_tokens != 7 || out.Usage.Completion_tokens != 3 || out.Usage.Total_tokens != 10 {
		t.Fatalf("%s", raw)
	}
}

func TestStreamIsTranslated(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `event: message_start
data: {"type":"message_start","message":{"id":"msg_9","model":"claude-x","usage":{"input_tokens":11,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t9","name":"now"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"tz\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"UTC\"}"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":9}}

event: message_stop
data: {"type":"message_stop"}

`)
	})
	a := anthropic(t, f)
	resp, err := a.RoundTrip(context.Background(), "POST", "/chat/completions", "", nil, []byte(`{"model":"claude-x","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if f.body["stream"] != true || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("%v %v", f.body, resp.Header)
	}
	raw, _ := io.ReadAll(resp.Body)
	var text, args, finish string
	var in, out float64
	done := false
	for _, line := range strings.Split(string(raw), "\n") {
		d, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		if d == "[DONE]" {
			done = true
			continue
		}
		var c struct {
			Choices []struct {
				Delta struct {
					Content   string
					ToolCalls []struct {
						Index    int
						ID       string
						Function struct{ Name, Arguments string }
					} `json:"tool_calls"`
				}
				FinishReason *string `json:"finish_reason"`
			}
			Usage map[string]float64
		}
		if err := json.Unmarshal([]byte(d), &c); err != nil {
			t.Fatal(err, d)
		}
		ch := c.Choices[0]
		text += ch.Delta.Content
		for _, tc := range ch.Delta.ToolCalls {
			args += tc.Function.Arguments
			if tc.ID != "" && (tc.ID != "t9" || tc.Function.Name != "now" || tc.Index != 0) {
				t.Fatalf("tool call start: %+v", tc)
			}
		}
		if ch.FinishReason != nil {
			finish = *ch.FinishReason
			in, out = c.Usage["prompt_tokens"], c.Usage["completion_tokens"]
		}
	}
	if text != "Hello" || args != `{"tz":"UTC"}` || finish != "tool_calls" || in != 11 || out != 9 || !done {
		t.Fatalf("text=%q args=%q finish=%q in=%v out=%v done=%v\n%s", text, args, finish, in, out, done, raw)
	}
}

func TestErrorsAndOtherPaths(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter) {
		w.WriteHeader(401)
		io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	})
	a := anthropic(t, f)
	resp, _ := a.RoundTrip(context.Background(), "POST", "/chat/completions", "", nil, []byte(`{"model":"m","messages":[]}`))
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 401 || !strings.Contains(string(raw), `"message":"invalid x-api-key"`) || !strings.Contains(string(raw), "authentication_error") {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	resp, _ = a.RoundTrip(context.Background(), "POST", "/embeddings", "", nil, []byte(`{}`))
	if resp.StatusCode != 404 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	resp, _ = a.RoundTrip(context.Background(), "POST", "/chat/completions", "", nil, []byte(`nope`))
	if resp.StatusCode != 400 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestModelListPassesThroughWithAllModels(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter) {
		io.WriteString(w, `{"data":[{"id":"claude-x","type":"model"}],"has_more":false}`)
	})
	a := anthropic(t, f)
	resp, err := a.RoundTrip(context.Background(), "GET", "/models", "", nil, nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatal(err)
	}
	if !strings.Contains(f.path, "limit=1000") || f.key != "sk-ant-key" {
		t.Fatalf("%s %s", f.path, f.key)
	}
}

func TestNoKeyIsAnErrorBeforeAnyRequest(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter) { t.Error("must not call upstream") })
	a := anthropic(t, f)
	a.SaveKey("")
	if _, err := a.RoundTrip(context.Background(), "GET", "/models", "", nil, nil); err == nil {
		t.Fatal("expected an error")
	}
}

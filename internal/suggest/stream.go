package suggest

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Time limits of the model call. The call is streamed, so a slow model is told apart from a stuck one:
// it fails after DefaultIdle without any data, or after the caller's overall deadline.
const DefaultIdle = 45 * time.Second

// Streamer is a Completer that can also return the response unread, for a streamed answer. The caller
// closes the body.
type Streamer interface {
	Stream(ctx context.Context, rest string, body []byte) (*http.Response, error)
}

// TimeoutError says which limit was hit, in which stage and after how long.
type TimeoutError struct {
	Kind  string // "idle" (no data for After) or "cap" (the overall limit)
	Stage string
	After time.Duration
}

var stageWords = map[string]string{StageFetch: "fetching the repo", StageRead: "reading the README", StageModel: "asking the model", StageCheck: "checking the config"}

// Where names the stage the way the UI shows it: "asking the model".
func (e *TimeoutError) Where() string { return stageWords[e.Stage] }

func (e *TimeoutError) Error() string {
	secs := int(e.After.Round(time.Second) / time.Second)
	msg := fmt.Sprintf("timed out while %s after %ds", e.Where(), secs)
	if e.Kind == "idle" {
		msg = fmt.Sprintf("timed out while %s: no data for %ds", e.Where(), secs)
	}
	if e.Stage == StageModel {
		msg += "; lower the effort"
	}
	return msg
}

// modelCall is one attempt at the chat completion.
type modelCall struct {
	status int
	reply  []byte // a complete chat completion body (assembled from the stream when streamed)
	chars  int
	first  time.Duration // time to the first token, 0 if none arrived
}

// maxReply bounds what is read from the model, streamed or not.
const maxReply = 1 << 20

func (s *Service) idle() time.Duration {
	if s.Idle > 0 {
		return s.Idle
	}
	return DefaultIdle
}

// call sends body (with "stream":true added when the completer can stream). A timeout becomes a
// *TimeoutError with the elapsed time since started.
func (s *Service) call(ctx context.Context, body []byte, started time.Time) (modelCall, error) {
	st, ok := s.LLM.(Streamer)
	if !ok {
		status, reply, err := s.LLM.Post(ctx, "/chat/completions", body)
		if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = &TimeoutError{Kind: "cap", Stage: StageModel, After: time.Since(started)}
		}
		return modelCall{status: status, reply: reply}, err
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var idled atomic.Bool
	idle := s.idle()
	timer := time.AfterFunc(idle, func() { idled.Store(true); cancel() })
	defer timer.Stop()
	timedOut := func(err error) error {
		switch {
		case idled.Load():
			return &TimeoutError{Kind: "idle", Stage: StageModel, After: idle}
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return &TimeoutError{Kind: "cap", Stage: StageModel, After: time.Since(started)}
		}
		return err
	}
	resp, err := st.Stream(cctx, "/chat/completions", withStream(body))
	if err != nil {
		return modelCall{}, timedOut(err)
	}
	defer resp.Body.Close()
	c := modelCall{status: resp.StatusCode}
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		c.reply, err = io.ReadAll(io.LimitReader(resp.Body, maxReply))
		if err != nil {
			return c, timedOut(err)
		}
		return c, nil
	}
	t0 := time.Now()
	var content strings.Builder
	var last time.Time
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), maxReply)
	for sc.Scan() {
		timer.Reset(idle)
		line := strings.TrimSpace(sc.Text())
		data, isData := strings.CutPrefix(line, "data:")
		if !isData {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var ch struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					Reasoning string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
			Error any `json:"error"`
		}
		if json.Unmarshal([]byte(data), &ch) != nil {
			continue
		}
		if ch.Error != nil {
			b, _ := json.Marshal(map[string]any{"error": ch.Error})
			c.reply = b
			c.status = http.StatusBadGateway
			return c, nil
		}
		for _, x := range ch.Choices {
			n := len(x.Delta.Content) + len(x.Delta.Reasoning)
			if n == 0 {
				continue
			}
			if c.first == 0 {
				c.first = time.Since(t0)
				s.logf("suggest: model first token after %s", c.first.Round(time.Millisecond))
			}
			content.WriteString(x.Delta.Content)
			c.chars += n
		}
		if c.chars > 0 && time.Since(last) > 400*time.Millisecond {
			last = time.Now()
			s.progress(Event{Stage: StageModel, Label: "Asking the model", Chars: c.chars})
		}
	}
	if err := sc.Err(); err != nil {
		return c, timedOut(err)
	}
	if idled.Load() || ctx.Err() != nil {
		return c, timedOut(ctx.Err())
	}
	c.reply, _ = json.Marshal(map[string]any{"choices": []map[string]any{{"message": map[string]any{"content": content.String()}}}})
	return c, nil
}

func withStream(body []byte) []byte {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return body
	}
	m["stream"] = true
	b, _ := json.Marshal(m)
	return b
}

func withoutEffort(body []byte) []byte {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return body
	}
	delete(m, "reasoning_effort")
	b, _ := json.Marshal(m)
	return b
}

// effortRejected reports whether an error reply blames the reasoning_effort parameter.
func effortRejected(status int, reply []byte) bool {
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
		return false
	}
	l := strings.ToLower(string(reply))
	return strings.Contains(l, "reasoning") || strings.Contains(l, "effort")
}

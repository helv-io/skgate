package provider

import (
	"encoding/json"
	"github.com/helv-io/skgate/internal/vkeys"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestEffortSettingsAreSeparateAndDefaulted(t *testing.T) {
	r := newRig(t, nil)
	if r.set.Effort("fake") != "low" || r.set.ChatEffort("fake") != "default" {
		t.Fatalf("defaults: helper %q chat %q", r.set.Effort("fake"), r.set.ChatEffort("fake"))
	}
	r.set.SetEffort("fake", "high")
	r.set.SetChatEffort("fake", "medium")
	if r.set.Effort("fake") != "high" || r.set.ChatEffort("fake") != "medium" {
		t.Fatalf("stored: helper %q chat %q", r.set.Effort("fake"), r.set.ChatEffort("fake"))
	}
	if EffortParam("default") != "" || EffortParam("low") != "low" || EffortParam("bogus") != "" || ValidEffort("bogus") {
		t.Fatal("EffortParam / ValidEffort")
	}
}

func TestChatEffortAddedOnlyWhenClientSetNone(t *testing.T) {
	r := newRig(t, nil)
	post := func(body string) string {
		t.Helper()
		r.do(t, "POST", "/v1/chat/completions", body)
		_, _, _, got := r.last()
		return got
	}
	if got := post(`{"model":"real-a"}`); strings.Contains(got, "reasoning_effort") {
		t.Fatalf("default effort must send nothing: %s", got)
	}
	r.set.SetChatEffort("fake", "medium")
	if got := post(`{"model":"real-a"}`); !strings.Contains(got, `"reasoning_effort":"medium"`) {
		t.Fatalf("stored effort not added: %s", got)
	}
	if got := post(`{"model":"real-a","reasoning_effort":"high"}`); !strings.Contains(got, `"reasoning_effort":"high"`) || strings.Contains(got, "medium") {
		t.Fatalf("the client's own effort must win: %s", got)
	}
	r.do(t, "GET", "/v1/models", "")
	if _, _, _, got := r.last(); strings.Contains(got, "reasoning_effort") {
		t.Fatalf("only chat completions get the effort: %s", got)
	}
}

func TestChatEffortRetriedWithoutWhenRejected(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	r := newRig(t, func(w http.ResponseWriter, req *http.Request, body string) {
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		if strings.Contains(body, "reasoning_effort") {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"message":"Unsupported parameter: reasoning_effort"}}`)
			return
		}
		io.WriteString(w, `{"ok":true}`)
	})
	r.set.SetChatEffort("fake", "high")
	st, out, _ := r.do(t, "POST", "/v1/chat/completions", `{"model":"real-a"}`)
	if st != 200 || !strings.Contains(out, `"ok":true`) || len(bodies) != 2 || strings.Contains(bodies[1], "reasoning_effort") {
		t.Fatalf("status %d %s bodies %v", st, out, bodies)
	}
	// an unrelated 400 is passed on, not retried
	bodies = nil
	r2 := newRig(t, func(w http.ResponseWriter, req *http.Request, body string) {
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"message":"messages is required"}}`)
	})
	r2.set.SetChatEffort("fake", "high")
	st, out, _ = r2.do(t, "POST", "/v1/chat/completions", `{"model":"real-a"}`)
	if st != 400 || len(bodies) != 1 || !strings.Contains(out, "messages is required") {
		t.Fatalf("status %d %s bodies %v", st, out, bodies)
	}
}

func TestLimitedKeyGets429WithOpenAIStyleError(t *testing.T) {
	var logs strings.Builder
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	r := newRig(t, nil)
	ks, _ := r.keys.List()
	id := ks[0].ID
	if st, _, _ := r.do(t, "POST", "/v1/chat/completions", `{"model":"real-a"}`); st != 200 {
		t.Fatalf("unlimited key: %d", st)
	}
	r.keys.SetLimits(id, 2, 0)
	r.do(t, "POST", "/v1/chat/completions", `{}`)
	r.do(t, "POST", "/v1/chat/completions", `{}`)
	st, body, hdr := r.do(t, "POST", "/v1/chat/completions", `{}`)
	var e struct {
		Error struct{ Message, Type, Code string }
	}
	json.Unmarshal([]byte(body), &e)
	if st != 429 || e.Error.Type != "rate_limit_error" || e.Error.Code != "rate_limit_exceeded" || !strings.Contains(e.Error.Message, "2 requests per minute") || hdr.Get("Retry-After") == "" {
		t.Fatalf("%d %s retry-after=%q", st, body, hdr.Get("Retry-After"))
	}
	r.keys.SetLimits(id, 0, 1)
	r.keys.Record(id, vkeys.Usage{Requests: 1})
	st, body, hdr = r.do(t, "GET", "/v1/models", "")
	json.Unmarshal([]byte(body), &e)
	if st != 429 || e.Error.Code != "key_hard_stop" || e.Error.Type != "insufficient_quota" || hdr.Get("Retry-After") != "" {
		t.Fatalf("hard stop: %d %s", st, body)
	}
	if !strings.Contains(logs.String(), "rejected POST /v1/chat/completions: rate_limit_exceeded") || !strings.Contains(logs.String(), "key_hard_stop") {
		t.Errorf("rejections not logged:\n%s", logs.String())
	}
	if strings.Contains(logs.String(), r.key) {
		t.Error("the key reached the log")
	}
}

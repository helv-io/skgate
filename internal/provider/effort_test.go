package provider

import (
	"io"
	"net/http"
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

package provider

import (
	"encoding/json"
	"github.com/helv-io/skgate/internal/vkeys"
	"log"
	"os"
	"strings"
	"testing"
)

func TestReasoningDefaultsToTheModelsChoiceAndKeepsSavedValues(t *testing.T) {
	r := newRig(t, nil)
	if r.set.Effort("fake") != "default" {
		t.Fatalf("unset: %q", r.set.Effort("fake"))
	}
	r.set.SetEffort("fake", "high")
	if r.set.Effort("fake") != "high" {
		t.Fatalf("stored: %q", r.set.Effort("fake"))
	}
	r.set.Set("fake", "effort", "bogus") // an unknown stored value falls back to the default
	if r.set.Effort("fake") != "default" {
		t.Fatalf("bogus: %q", r.set.Effort("fake"))
	}
	if EffortParam("default") != "" || EffortParam("low") != "low" || EffortParam("bogus") != "" || ValidEffort("bogus") {
		t.Fatal("EffortParam / ValidEffort")
	}
}

// The reasoning choice belongs to the MCP helper model only: proxied chat requests go through as the client wrote
// them, whatever an older version stored.
func TestChatRequestsAreNeverGivenReasoning(t *testing.T) {
	r := newRig(t, nil)
	r.set.Set("fake", "chat_effort", "high") // left over from an older version
	r.set.SetEffort("fake", "high")
	r.do(t, "POST", "/v1/chat/completions", `{"model":"real-a"}`)
	if _, _, _, got := r.last(); strings.Contains(got, "reasoning") {
		t.Fatalf("a reasoning setting reached a chat request: %s", got)
	}
	r.do(t, "POST", "/v1/chat/completions", `{"model":"real-a","reasoning_effort":"low"}`)
	if _, _, _, got := r.last(); !strings.Contains(got, `"reasoning_effort":"low"`) {
		t.Fatalf("the client's own setting must pass untouched: %s", got)
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

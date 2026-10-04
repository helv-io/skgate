package provider

import (
	"context"
	"net/http"
)

// Reasoning choices shown in the model dialog and on the status page. "auto" sends nothing: the model decides.
// "default" is the name this choice was stored under before and still reads as auto.
var Efforts = []string{"auto", "low", "medium", "high"}

// ValidEffort reports whether v is one of Efforts (or the earlier name of auto).
func ValidEffort(v string) bool { return v == "default" || contains(Efforts, v) }

// Effort is the reasoning setting of the MCP helper model's calls; "auto" (the model decides) unless chosen
// otherwise. A choice saved earlier keeps working, including a stored "default" (now auto). (An older separate
// setting for proxied chat requests is gone; its stored value is simply never read.)
func (s Settings) Effort(id string) string {
	if v, ok := s.Get(id, "effort"); ok && ValidEffort(v) && v != "default" {
		return v
	}
	return "auto"
}

// SetEffort stores the helper model's reasoning.
func (s Settings) SetEffort(id, v string) error {
	if v == "default" {
		v = "auto"
	}
	return s.Set(id, "effort", v)
}

// EffortParam is the value to send as reasoning_effort, or "" when nothing should be sent.
func EffortParam(v string) string {
	if v == "auto" || v == "default" || !ValidEffort(v) {
		return ""
	}
	return v
}

// Stream sends a JSON request and returns the response with its body unread, so the caller can consume
// a streamed (server-sent events) answer as it arrives. The caller closes the body and bounds the time.
// The provider is chosen as Post does it.
func (p *Proxy) Stream(ctx context.Context, rest string, body []byte) (*http.Response, error) {
	return p.send(ctx, http.MethodPost, rest, "", http.Header{"Content-Type": {"application/json"}, "Accept": {"text/event-stream"}}, body)
}

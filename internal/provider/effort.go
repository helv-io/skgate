package provider

import (
	"context"
	"net/http"
)

// Reasoning choices shown in the model dialog. "default" sends nothing: the model decides.
var Efforts = []string{"default", "low", "medium", "high"}

// ValidEffort reports whether v is one of Efforts.
func ValidEffort(v string) bool { return contains(Efforts, v) }

// Effort is the reasoning setting of the MCP helper model's calls; "default" (the model decides) unless chosen
// otherwise. A choice saved earlier keeps working. (An older separate setting for proxied chat requests is gone;
// its stored value is simply never read.)
func (s Settings) Effort(id string) string {
	if v, ok := s.Get(id, "effort"); ok && ValidEffort(v) {
		return v
	}
	return "default"
}

// SetEffort stores the helper model's reasoning.
func (s Settings) SetEffort(id, v string) error { return s.Set(id, "effort", v) }

// EffortParam is the value to send as reasoning_effort, or "" when nothing should be sent.
func EffortParam(v string) string {
	if v == "default" || !ValidEffort(v) {
		return ""
	}
	return v
}

// Stream sends a JSON request and returns the response with its body unread, so the caller can consume
// a streamed (server-sent events) answer as it arrives. The caller closes the body and bounds the time.
// A "model" naming one of skgate's aliases for this provider is resolved to its target, as Post does.
func (p *Proxy) Stream(ctx context.Context, rest string, body []byte) (*http.Response, error) {
	body = rewriteModel(body, p.Set.Aliases(p.Backend.ID()))
	return p.send(ctx, http.MethodPost, rest, "", http.Header{"Content-Type": {"application/json"}, "Accept": {"text/event-stream"}}, body)
}

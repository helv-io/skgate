package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Reasoning effort choices shown in the model dialogs. "default" sends nothing: the provider decides.
var Efforts = []string{"default", "low", "medium", "high"}

// ValidEffort reports whether v is one of Efforts.
func ValidEffort(v string) bool { return contains(Efforts, v) }

// Effort is the reasoning effort for the MCP helper model's calls; low unless chosen otherwise.
func (s Settings) Effort(id string) string {
	if v, ok := s.Get(id, "effort"); ok && ValidEffort(v) {
		return v
	}
	return "low"
}

// SetEffort stores the helper model's effort.
func (s Settings) SetEffort(id, v string) error { return s.Set(id, "effort", v) }

// ChatEffort is the reasoning effort added to proxied chat requests that do not set one; default
// (nothing added) unless chosen otherwise. It is separate from the helper model's.
func (s Settings) ChatEffort(id string) string {
	if v, ok := s.Get(id, "chat_effort"); ok && ValidEffort(v) {
		return v
	}
	return "default"
}

// SetChatEffort stores the effort for proxied chat requests.
func (s Settings) SetChatEffort(id, v string) error { return s.Set(id, "chat_effort", v) }

// EffortParam is the value to send as reasoning_effort, or "" when nothing should be sent.
func EffortParam(v string) string {
	if v == "default" || !ValidEffort(v) {
		return ""
	}
	return v
}

// withEffort adds reasoning_effort to a chat completion body that does not carry one. It returns the
// body unchanged (and false) for anything else.
func withEffort(body []byte, effort string) ([]byte, bool) {
	if effort == "" {
		return body, false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil || m == nil {
		return body, false
	}
	if _, set := m["reasoning_effort"]; set {
		return body, false
	}
	if _, set := m["reasoning"]; set {
		return body, false
	}
	m["reasoning_effort"], _ = json.Marshal(effort)
	out, err := json.Marshal(m)
	if err != nil {
		return body, false
	}
	return out, true
}

// RejectsEffort reports whether an error reply says the reasoning_effort parameter is the problem.
func RejectsEffort(status int, reply []byte) bool {
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
		return false
	}
	l := strings.ToLower(string(reply))
	return strings.Contains(l, "reasoning") || strings.Contains(l, "effort")
}

// sendChat sends a chat completion, adding the stored effort when the client set none. If the provider
// rejects the parameter, it sends the request again as the client wrote it.
func (p *Proxy) sendChat(r *http.Request, rest string, hdr http.Header, body []byte) (*http.Response, error) {
	sent, added := withEffort(body, EffortParam(p.Set.ChatEffort(p.Backend.ID())))
	resp, err := p.send(r.Context(), r.Method, rest, r.URL.RawQuery, hdr, sent)
	if err != nil || !added || (resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusUnprocessableEntity) {
		return resp, err
	}
	reply, _ := io.ReadAll(io.LimitReader(resp.Body, maxList))
	resp.Body.Close()
	if !RejectsEffort(resp.StatusCode, reply) {
		resp.Body = io.NopCloser(bytes.NewReader(reply))
		return resp, nil
	}
	return p.send(r.Context(), r.Method, rest, r.URL.RawQuery, hdr, body)
}

// Stream sends a JSON request and returns the response with its body unread, so the caller can consume
// a streamed (server-sent events) answer as it arrives. The caller closes the body and bounds the time.
func (p *Proxy) Stream(ctx context.Context, rest string, body []byte) (*http.Response, error) {
	return p.send(ctx, http.MethodPost, rest, "", http.Header{"Content-Type": {"application/json"}, "Accept": {"text/event-stream"}}, body)
}

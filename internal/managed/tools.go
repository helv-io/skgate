package managed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// Tool-list tracking. After every start skgate lists the child's tools and keeps a hash of the list.
// When a run's list differs from the previous run's (an update, a reinstall, a restart, a changed
// source), every client session gets notifications/tools/list_changed, so a client that caches the
// list fetches it again. An unchanged list sends nothing. skgate always advertises listChanged.

// ToolsListChanged is the MCP notification sent when the tool list changed.
const ToolsListChanged = "notifications/tools/list_changed"

const (
	toolsMaxPages     = 20
	toolsCheckTimeout = 30 * time.Second
)

// ListChangedNotification is the JSON-RPC message clients get when the tool list changed.
func ListChangedNotification() []byte {
	return mustJSON(rpcMsg{JSONRPC: "2.0", Method: ToolsListChanged})
}

// ToolsHash returns the hash of the tool list seen on the last check ("" before the first).
func (p *Proc) ToolsHash() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.toolsHash
}

// checkToolsAsync runs checkTools in the background.
func (p *Proc) checkToolsAsync(notify bool) {
	p.m.bg(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, toolsCheckTimeout)
		defer cancel()
		p.checkTools(ctx, notify)
	})
}

// checkTools lists the running child's tools and records their hash. When notify is set and the hash
// differs from the previous one, client sessions are told the list changed.
func (p *Proc) checkTools(ctx context.Context, notify bool) {
	h, err := p.listHash(ctx)
	if err != nil {
		return
	}
	p.mu.Lock()
	prev := p.toolsHash
	p.toolsHash = h
	p.mu.Unlock()
	if prev == "" || prev == h {
		return
	}
	p.note("tool list changed")
	if notify {
		p.broadcast(ListChangedNotification())
	}
	if f := p.m.o.OnToolsChanged; f != nil {
		f(p.alias)
	}
}

// listHash hashes every page of tools/list from the running child. It never starts the process.
func (p *Proc) listHash(ctx context.Context) (string, error) {
	sum := sha256.New()
	cursor := ""
	for range toolsMaxPages {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		res, rerr, err := p.callRunning(ctx, "tools/list", params)
		if err != nil {
			return "", err
		}
		if rerr != nil {
			return "", rerr
		}
		var page struct {
			Tools      json.RawMessage `json:"tools"`
			NextCursor string          `json:"nextCursor"`
		}
		if json.Unmarshal(res, &page) != nil {
			return "", errors.New("invalid tools/list result")
		}
		sum.Write([]byte(compact(page.Tools)))
		sum.Write([]byte{0})
		if page.NextCursor == "" || page.NextCursor == cursor {
			break
		}
		cursor = page.NextCursor
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// withListChanged returns an initialize result whose capabilities say tools.listChanged is true.
func withListChanged(res json.RawMessage) json.RawMessage {
	var top map[string]json.RawMessage
	if json.Unmarshal(res, &top) != nil || top == nil {
		return res
	}
	var caps map[string]json.RawMessage
	if json.Unmarshal(top["capabilities"], &caps) != nil || caps == nil {
		caps = map[string]json.RawMessage{}
	}
	var tools map[string]json.RawMessage
	if json.Unmarshal(caps["tools"], &tools) != nil || tools == nil {
		tools = map[string]json.RawMessage{}
	}
	tools["listChanged"] = json.RawMessage("true")
	caps["tools"] = mustJSON(tools)
	top["capabilities"] = mustJSON(caps)
	return mustJSON(top)
}

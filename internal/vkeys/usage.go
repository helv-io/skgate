package vkeys

import (
	"context"
	"time"
)

// Usage is the cumulative use of one key. Tokens are only what the provider reported; a call
// without a usage object adds a request and no tokens.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	// Requests counts successful API calls (not model-list reads); MCPRequests counts authenticated MCP requests.
	Requests    int64
	MCPRequests int64
	LastUsed    time.Time
}

// Empty reports whether nothing was recorded.
func (u Usage) Empty() bool {
	return u.PromptTokens == 0 && u.CompletionTokens == 0 && u.TotalTokens == 0 && u.Requests == 0 && u.MCPRequests == 0
}

// Record adds d to a key's usage. It only touches memory: the totals are written by FlushUsage
// (RunUsage does it every few seconds), so the proxy path never waits for the database.
func (m *Manager) Record(id int64, d Usage) {
	if id == 0 || d.Empty() {
		return
	}
	if d.LastUsed.IsZero() {
		d.LastUsed = time.Now()
	}
	m.umu.Lock()
	p := m.pending[id]
	p.PromptTokens += d.PromptTokens
	p.CompletionTokens += d.CompletionTokens
	p.TotalTokens += d.TotalTokens
	p.Requests += d.Requests
	p.MCPRequests += d.MCPRequests
	if d.LastUsed.After(p.LastUsed) {
		p.LastUsed = d.LastUsed
	}
	if m.pending == nil {
		m.pending = map[int64]Usage{}
	}
	m.pending[id] = p
	m.umu.Unlock()
}

// FlushUsage writes the pending increments in one transaction. Each is added to the stored totals
// atomically; a key that no longer exists is skipped. On failure the increments stay pending.
func (m *Manager) FlushUsage() error {
	m.umu.Lock()
	batch := m.pending
	m.pending = nil
	m.umu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	err := m.writeUsage(batch)
	if err != nil {
		for id, d := range batch {
			m.Record(id, d)
		}
	}
	return err
}

func (m *Manager) writeUsage(batch map[int64]Usage) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for id, d := range batch {
		_, err := tx.Exec(`INSERT INTO key_usage(key_id,prompt_tokens,completion_tokens,total_tokens,requests,mcp_requests,last_used)
			SELECT ?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM vkeys WHERE id=?)
			ON CONFLICT(key_id) DO UPDATE SET
			  prompt_tokens=prompt_tokens+excluded.prompt_tokens,
			  completion_tokens=completion_tokens+excluded.completion_tokens,
			  total_tokens=total_tokens+excluded.total_tokens,
			  requests=requests+excluded.requests,
			  mcp_requests=mcp_requests+excluded.mcp_requests,
			  last_used=MAX(last_used,excluded.last_used)`,
			id, d.PromptTokens, d.CompletionTokens, d.TotalTokens, d.Requests, d.MCPRequests, d.LastUsed.Unix(), id)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RunUsage flushes the pending usage every interval and once more when ctx ends.
func (m *Manager) RunUsage(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := m.FlushUsage(); err != nil {
				m.Logf("vkeys: usage flush failed: %v", err)
			}
			return
		case <-t.C:
			if err := m.FlushUsage(); err != nil {
				m.Logf("vkeys: usage flush failed: %v", err)
			}
		}
	}
}

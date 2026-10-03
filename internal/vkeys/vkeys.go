// Package vkeys mints and verifies skgate virtual API keys (sk-...). Only a SHA-256 hash,
// a short display prefix and a label are stored; the full key is shown once at creation.
package vkeys

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/store"
	"github.com/helv-io/skgate/internal/timefmt"
)

const (
	Prefix    = "sk-"
	RandChars = 48
	KeyLen    = len(Prefix) + RandChars
)

// Key is stored metadata about a virtual key (never the secret).
type Key struct {
	ID        int64
	Prefix    string
	Label     string
	Last4     string // display only; empty for keys minted by older releases
	CreatedAt time.Time
	LastUsed  time.Time
	Revoked   bool
	Usage     Usage
	// Optional limits; 0 / the zero time mean none (see limits.go).
	RatePerMin int64
	ExpiresAt  time.Time
	// URLKey allows this key as ?key= on MCP endpoints. Off by default: URLs end up in logs, history and referrers.
	URLKey bool
}

// Masked is the display form of the key: asterisks plus the last 4 characters when known.
func (k Key) Masked() string { return httputil.MaskStars + k.Last4 }

// RevokedRetention is how long a revoked key that was used is kept after its last use.
const RevokedRetention = 30 * 24 * time.Hour

// Manager manages virtual keys.
type Manager struct {
	db *store.DB
	// pending holds usage increments not yet written (see Record).
	umu     sync.Mutex
	pending map[int64]Usage
	// Logf receives purge notices (default: the standard logger).
	Logf func(format string, args ...any)
	// Now replaces the clock of the rate windows (tests).
	Now     func() time.Time
	windows map[int64]*window
}

// New returns a Manager.
func New(db *store.DB) *Manager { return &Manager{db: db, Logf: log.Printf} }

// LooksLikeKey reports whether s has the shape of a virtual key.
func LooksLikeKey(s string) bool {
	if len(s) != KeyLen || !strings.HasPrefix(s, Prefix) {
		return false
	}
	for _, c := range s[len(Prefix):] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// CleanLabel trims a key name, falls back to "unnamed" and cuts it to 80 bytes at a character boundary.
func CleanLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return "unnamed"
	}
	for len(label) > 80 {
		_, n := utf8.DecodeLastRuneInString(label)
		label = label[:len(label)-n]
	}
	return strings.TrimSpace(label)
}

// Create mints a new key and returns the full secret exactly once.
func (m *Manager) Create(label string) (string, Key, error) {
	label = CleanLabel(label)
	full := mint()
	prefix := full[:8]
	now := time.Now()
	res, err := m.db.Exec(`INSERT INTO vkeys(prefix,hash,label,created_at,last4) VALUES(?,?,?,?,?)`,
		prefix, httputil.SHA256Hex(full), label, now.Unix(), full[len(full)-4:])
	if err != nil {
		return "", Key{}, err
	}
	id, _ := res.LastInsertId()
	return full, Key{ID: id, Prefix: prefix, Label: label, Last4: full[len(full)-4:], CreatedAt: now}, nil
}

// Verify checks a presented key. Revoked, expired or unknown keys fail.
func (m *Manager) Verify(token string) (Key, bool) {
	k, ok := m.lookup(token)
	if !ok || k.Expired(m.clock()) {
		return Key{}, false
	}
	return k, true
}

// ExpiredAt reports that token is a known, active key whose expiration has passed, and when it did.
func (m *Manager) ExpiredAt(token string) (time.Time, bool) {
	k, ok := m.lookup(token)
	if !ok || !k.Expired(m.clock()) {
		return time.Time{}, false
	}
	return k.ExpiresAt, true
}

func (m *Manager) lookup(token string) (Key, bool) {
	if !LooksLikeKey(token) {
		return Key{}, false
	}
	var k Key
	var created, last, revoked, expires, urlKey int64
	err := m.db.QueryRow(`SELECT k.id,k.prefix,k.label,k.created_at,k.last_used,k.revoked_at,k.last4,k.rate_per_min,k.expires_at,k.url_key,COALESCE(u.requests,0)
		FROM vkeys k LEFT JOIN key_usage u ON u.key_id=k.id WHERE k.hash=?`,
		httputil.SHA256Hex(token)).Scan(&k.ID, &k.Prefix, &k.Label, &created, &last, &revoked, &k.Last4, &k.RatePerMin, &expires, &urlKey, &k.Usage.Requests)
	if err != nil || revoked != 0 {
		return Key{}, false
	}
	k.CreatedAt = time.Unix(created, 0)
	k.URLKey = urlKey != 0
	if expires > 0 {
		k.ExpiresAt = time.Unix(expires, 0)
	}
	if !k.Expired(m.clock()) && time.Since(time.Unix(last, 0)) > time.Minute {
		_, _ = m.db.Exec(`UPDATE vkeys SET last_used=? WHERE id=?`, time.Now().Unix(), k.ID)
	}
	return k, true
}

func mint() string { return Prefix + httputil.RandString(RandChars) }

// Regenerate replaces the secret of an active key and returns the new one exactly once. The
// record keeps its id, label, created and last-used values; the old secret stops working at once.
func (m *Manager) Regenerate(id int64) (string, Key, error) {
	full := mint()
	res, err := m.db.Exec(`UPDATE vkeys SET prefix=?,hash=?,last4=? WHERE id=? AND revoked_at=0`,
		full[:8], httputil.SHA256Hex(full), full[len(full)-4:], id)
	if err != nil {
		return "", Key{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", Key{}, errors.New("key not found or revoked")
	}
	ks, err := m.List()
	if err != nil {
		return "", Key{}, err
	}
	for _, k := range ks {
		if k.ID == id {
			return full, k, nil
		}
	}
	return "", Key{}, errors.New("key not found")
}

// Revoke revokes a key. A key that was never used is deleted right away; a used one is kept
// (shown as revoked) until PurgeRevoked removes it 30 days after its last use.
func (m *Manager) Revoke(id int64) error {
	res, err := m.db.Exec(`UPDATE vkeys SET revoked_at=? WHERE id=? AND revoked_at=0`, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("key not found or already revoked")
	}
	_, err = m.PurgeRevoked(time.Now())
	return err
}

// PurgeRevoked deletes revoked keys that were never used, and revoked keys whose last use is more
// than RevokedRetention before now. It logs every key it removes and returns them.
func (m *Manager) PurgeRevoked(now time.Time) ([]Key, error) {
	cutoff := now.Add(-RevokedRetention).Unix()
	rows, err := m.db.Query(`SELECT id,prefix,label,created_at,last_used,last4 FROM vkeys
		WHERE revoked_at<>0 AND (last_used=0 OR last_used<?)`, cutoff)
	if err != nil {
		return nil, err
	}
	var gone []Key
	for rows.Next() {
		var k Key
		var c, l int64
		if err := rows.Scan(&k.ID, &k.Prefix, &k.Label, &c, &l, &k.Last4); err != nil {
			rows.Close()
			return nil, err
		}
		k.CreatedAt, k.Revoked = time.Unix(c, 0), true
		if l > 0 {
			k.LastUsed = time.Unix(l, 0)
		}
		gone = append(gone, k)
	}
	rows.Close()
	for _, k := range gone {
		if _, err := m.db.Exec(`DELETE FROM vkeys WHERE id=?`, k.ID); err != nil {
			return nil, err
		}
		why := "never used"
		if !k.LastUsed.IsZero() {
			why = "last used " + timefmt.Date(k.LastUsed)
		}
		m.Logf("vkeys: purged revoked key id=%d label=%q key=%s (%s)", k.ID, k.Label, k.Masked(), why)
	}
	return gone, nil
}

// RunPurge purges once immediately, then every interval until ctx ends.
func (m *Manager) RunPurge(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if _, err := m.PurgeRevoked(time.Now()); err != nil {
			m.Logf("vkeys: purge failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// List returns all keys, newest first, with their usage (pending increments are written first).
func (m *Manager) List() ([]Key, error) {
	_ = m.FlushUsage()
	rows, err := m.db.Query(`SELECT k.id,k.prefix,k.label,k.created_at,k.last_used,k.revoked_at,k.last4,k.rate_per_min,k.expires_at,k.url_key,
		COALESCE(u.prompt_tokens,0),COALESCE(u.completion_tokens,0),COALESCE(u.total_tokens,0),
		COALESCE(u.requests,0),COALESCE(u.mcp_requests,0),COALESCE(u.last_used,0)
		FROM vkeys k LEFT JOIN key_usage u ON u.key_id=k.id ORDER BY k.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Key
	for rows.Next() {
		var k Key
		var c, l, r, ul, ex, uk int64
		if err := rows.Scan(&k.ID, &k.Prefix, &k.Label, &c, &l, &r, &k.Last4, &k.RatePerMin, &ex, &uk,
			&k.Usage.PromptTokens, &k.Usage.CompletionTokens, &k.Usage.TotalTokens, &k.Usage.Requests, &k.Usage.MCPRequests, &ul); err != nil {
			return nil, err
		}
		k.CreatedAt = time.Unix(c, 0)
		if l > 0 {
			k.LastUsed = time.Unix(l, 0)
		}
		if ul > 0 {
			k.Usage.LastUsed = time.Unix(ul, 0)
		}
		if ex > 0 {
			k.ExpiresAt = time.Unix(ex, 0)
		}
		k.URLKey = uk != 0
		k.Revoked = r != 0
		out = append(out, k)
	}
	return out, rows.Err()
}

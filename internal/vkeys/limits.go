package vkeys

import (
	"errors"
	"fmt"
	"time"

	"github.com/helv-io/skgate/internal/timefmt"
)

// Limits are optional and per key; every key is unlimited and never expires until they are set.
//
//   - RatePerMin: at most this many /v1 requests in each minute (a fixed one-minute window per key). 0 = no rate limit.
//     A request over it is answered with 429 and does not count.
//   - ExpiresAt: from this moment the key is refused everywhere (/v1 and MCP) with 401 and a message that says it
//     expired. The zero time = never expires. The date can be moved or cleared again.
const MaxLimit = 1_000_000_000

// Limited reports whether a rate limit is set.
func (k Key) Limited() bool { return k.RatePerMin > 0 }

// Expired reports whether the key's expiration has passed at now.
func (k Key) Expired(now time.Time) bool { return !k.ExpiresAt.IsZero() && !now.Before(k.ExpiresAt) }

// LimitsText is the short display form of the rate limit: "unlimited" or "30/min".
func (k Key) LimitsText() string {
	if k.RatePerMin > 0 {
		return fmt.Sprintf("%d/min", k.RatePerMin)
	}
	return "unlimited"
}

// ExpiredMessage is what a client is told when it presents an expired key.
func ExpiredMessage(at time.Time) string {
	return "This API key expired on " + timefmt.Long(at) + ". Ask the administrator for a new expiration date or a new key."
}

// SetLimits stores the limits of an active key; 0 clears the rate limit and the zero time clears the expiration.
func (m *Manager) SetLimits(id, ratePerMin int64, expires time.Time) error {
	if ratePerMin < 0 || ratePerMin > MaxLimit {
		return fmt.Errorf("the rate limit must be a whole number from 0 to %d", MaxLimit)
	}
	var ex int64
	if !expires.IsZero() {
		ex = expires.Unix()
	}
	res, err := m.db.Exec(`UPDATE vkeys SET rate_per_min=?,expires_at=? WHERE id=? AND revoked_at=0`, ratePerMin, ex, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("key not found or revoked")
	}
	return nil
}

// SetLabel renames an active key.
func (m *Manager) SetLabel(id int64, label string) error {
	res, err := m.db.Exec(`UPDATE vkeys SET label=? WHERE id=? AND revoked_at=0`, CleanLabel(label), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("key not found or revoked")
	}
	return nil
}

// Rejection says why a request was refused. RetryAfter is set for a rate limit.
type Rejection struct {
	Code       string // "rate_limit_exceeded"
	Message    string
	RetryAfter time.Duration
}

type window struct {
	start time.Time
	n     int64
}

// Check applies k's rate limit to one /v1 request and returns nil if it may go on. k must come from Verify.
func (m *Manager) Check(k Key) *Rejection {
	if k.RatePerMin <= 0 {
		return nil
	}
	now := m.clock()
	m.umu.Lock()
	defer m.umu.Unlock()
	if m.windows == nil {
		m.windows = map[int64]*window{}
	}
	if len(m.windows) > 512 { // forget windows that ended
		for id, w := range m.windows {
			if now.Sub(w.start) >= time.Minute {
				delete(m.windows, id)
			}
		}
	}
	w := m.windows[k.ID]
	if w == nil || now.Sub(w.start) >= time.Minute {
		w = &window{start: now}
		m.windows[k.ID] = w
	}
	if w.n >= k.RatePerMin {
		wait := w.start.Add(time.Minute).Sub(now)
		return &Rejection{Code: "rate_limit_exceeded", RetryAfter: wait,
			Message: fmt.Sprintf("Rate limit of %d requests per minute reached for this API key. Retry in %d seconds.", k.RatePerMin, secs(wait))}
	}
	w.n++
	return nil
}

func secs(d time.Duration) int {
	s := int((d + time.Second - 1) / time.Second)
	if s < 1 {
		s = 1
	}
	return s
}

// RetryAfterSeconds is RetryAfter rounded up to whole seconds (at least 1), for the Retry-After header.
func (r *Rejection) RetryAfterSeconds() int { return secs(r.RetryAfter) }

func (m *Manager) clock() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// SetURLKey allows or forbids presenting an active key as ?key= on MCP endpoints.
func (m *Manager) SetURLKey(id int64, allowed bool) error {
	v := 0
	if allowed {
		v = 1
	}
	res, err := m.db.Exec(`UPDATE vkeys SET url_key=? WHERE id=? AND revoked_at=0`, v, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("key not found or revoked")
	}
	return nil
}

// SettingStore is the part of the settings store the one-time migration of the old global switch needs.
type SettingStore interface {
	GetSetting(key string) (string, bool)
	DeleteSetting(key string) error
}

// MigrateGlobalURLKey retires the old global "accept ?key=" setting (allow_query_key). If it was on, every existing
// key gets the per-key switch, so nothing that worked stops working; if it was off, all stay off. The setting is
// removed afterwards, which makes running this again (or again after a legacy environment import) harmless.
func (m *Manager) MigrateGlobalURLKey(kv SettingStore) error {
	v, ok := kv.GetSetting("allow_query_key")
	if !ok {
		return nil
	}
	if v == "1" {
		if _, err := m.db.Exec(`UPDATE vkeys SET url_key=1`); err != nil {
			return err
		}
	}
	return kv.DeleteSetting("allow_query_key")
}

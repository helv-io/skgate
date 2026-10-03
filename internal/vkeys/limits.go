package vkeys

import (
	"errors"
	"fmt"
	"time"
)

// Limits are optional and per key; every key is unlimited until they are set.
//
//   - RatePerMin: at most this many /v1 requests in each minute (a fixed one-minute window per key). 0 = no rate limit.
//   - HardStop: after this many successful requests in total, every further /v1 request is rejected until the
//     number is raised or cleared. Counted with the usage totals. 0 = no hard stop.
//
// A rejected request is answered with 429 and does not count.
const MaxLimit = 1_000_000_000

// Limited reports whether any limit is set.
func (k Key) Limited() bool { return k.RatePerMin > 0 || k.HardStop > 0 }

// LimitsText is the short display form: "unlimited", "30/min", "stop at 1000" or "30/min, stop at 1000".
func (k Key) LimitsText() string {
	var parts []string
	if k.RatePerMin > 0 {
		parts = append(parts, fmt.Sprintf("%d/min", k.RatePerMin))
	}
	if k.HardStop > 0 {
		parts = append(parts, fmt.Sprintf("stop at %d", k.HardStop))
	}
	if len(parts) == 0 {
		return "unlimited"
	}
	if len(parts) == 2 {
		return parts[0] + ", " + parts[1]
	}
	return parts[0]
}

// SetLimits stores the limits of an active key; 0 clears one.
func (m *Manager) SetLimits(id, ratePerMin, hardStop int64) error {
	if ratePerMin < 0 || hardStop < 0 || ratePerMin > MaxLimit || hardStop > MaxLimit {
		return fmt.Errorf("limits must be whole numbers from 0 to %d", MaxLimit)
	}
	res, err := m.db.Exec(`UPDATE vkeys SET rate_per_min=?,hard_stop=? WHERE id=? AND revoked_at=0`, ratePerMin, hardStop, id)
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
	Code       string // "rate_limit_exceeded" or "key_hard_stop"
	Message    string
	RetryAfter time.Duration
}

type window struct {
	start time.Time
	n     int64
}

// Check applies k's limits to one /v1 request and returns nil if it may go on. k must come from Verify
// (it carries the stored request total); requests recorded but not yet written count too.
func (m *Manager) Check(k Key) *Rejection {
	if !k.Limited() {
		return nil
	}
	if k.HardStop > 0 {
		m.umu.Lock()
		total := k.Usage.Requests + m.pending[k.ID].Requests
		m.umu.Unlock()
		if total >= k.HardStop {
			return &Rejection{Code: "key_hard_stop", Message: fmt.Sprintf("This API key reached its hard stop of %d requests. Ask the administrator to raise or clear it.", k.HardStop)}
		}
	}
	if k.RatePerMin > 0 {
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
	}
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

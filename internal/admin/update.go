package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ReleaseWatch tells the admin header whether GitHub has a newer release than the running one. It asks the
// public latest-release API (no credentials) at most every few hours, remembers the answer in memory, never
// makes a page wait for it, and stays silent when GitHub cannot be reached or answers anything unexpected.
type ReleaseWatch struct {
	URL     string       // latest-release API; empty = never ask
	Client  *http.Client // nil = a client with a short timeout
	Current string       // the running version, "0.7.8"
	Every   time.Duration
	Retry   time.Duration // wait after a failure (offline, rate limited, unreadable)

	mu      sync.Mutex
	latest  string // newest stable tag seen, "v0.8.0"
	next    time.Time
	running bool
}

// NewReleaseWatch watches url for releases newer than current; an empty url disables it.
func NewReleaseWatch(url, current string) *ReleaseWatch {
	return &ReleaseWatch{URL: url, Current: current, Every: 6 * time.Hour, Retry: time.Hour}
}

// Newer returns the tag of the newest release when it is newer than the running version, else "". It answers from
// memory; when the answer is stale it starts one background refresh.
func (w *ReleaseWatch) Newer() string {
	if w == nil || w.URL == "" {
		return ""
	}
	w.mu.Lock()
	stale := !time.Now().Before(w.next) && !w.running
	if stale {
		w.running = true
	}
	latest := w.latest
	w.mu.Unlock()
	if stale {
		go func() { w.Refresh(context.Background()) }()
	}
	if latest != "" && newerVersion(latest, w.Current) {
		return latest
	}
	return ""
}

// Refresh asks GitHub now. A failure keeps the last good answer and delays the next try.
func (w *ReleaseWatch) Refresh(ctx context.Context) {
	tag, ok := w.fetch(ctx)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.running = false
	if ok {
		w.latest, w.next = tag, time.Now().Add(w.Every)
		return
	}
	w.next = time.Now().Add(w.Retry)
}

func (w *ReleaseWatch) fetch(ctx context.Context) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.URL, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "skgate/"+w.Current)
	c := w.Client
	if c == nil {
		c = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	var rel struct {
		Tag        string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel) != nil || rel.Prerelease || rel.Draft {
		return "", false
	}
	if _, ok := parseVersion(rel.Tag); !ok {
		return "", false
	}
	return rel.Tag, true
}

// parseVersion reads "v1.2.3" or "1.2.3" (a stable version only: anything after the patch number, such as -rc1, is refused).
func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" || p[0] == '+' || p[0] == '-' {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// newerVersion reports whether a is a higher stable version than b; unreadable versions are never newer.
func newerVersion(a, b string) bool {
	x, ok1 := parseVersion(a)
	y, ok2 := parseVersion(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

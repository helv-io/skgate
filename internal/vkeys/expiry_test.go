package vkeys

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseExpiry(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 10, 3, 12, 30, 0, 0, ny) // a Saturday
	at := func(y int, mo time.Month, d, h, mi, s int) time.Time { return time.Date(y, mo, d, h, mi, s, 0, ny) }
	cases := []struct {
		in   string
		want time.Time
	}{
		{"30d", now.AddDate(0, 0, 30)},
		{"30 days", now.AddDate(0, 0, 30)},
		{"in 30 days", now.AddDate(0, 0, 30)},
		{"  2W ", now.AddDate(0, 0, 14)},
		{"6 months", now.AddDate(0, 6, 0)},
		{"1y", now.AddDate(1, 0, 0)},
		{"12h", now.Add(12 * time.Hour)},
		{"90 min", now.Add(90 * time.Minute)},
		{"today", at(2026, 10, 3, 23, 59, 59)},
		{"tomorrow", at(2026, 10, 4, 23, 59, 59)},
		{"tomorrow at 5pm", at(2026, 10, 4, 17, 0, 0)},
		{"friday", at(2026, 10, 9, 23, 59, 59)},
		{"next sat", at(2026, 10, 10, 23, 59, 59)}, // today is Saturday: the next one, not today
		{"2026-12-31", at(2026, 12, 31, 23, 59, 59)},
		{"2026-12-31 18:00", at(2026, 12, 31, 18, 0, 0)},
		{"2026-12-31T18:05", at(2026, 12, 31, 18, 5, 0)},
		{"dec 31", at(2026, 12, 31, 23, 59, 59)},
		{"Dec 31, 2027", at(2027, 12, 31, 23, 59, 59)},
		{"december 31st", at(2026, 12, 31, 23, 59, 59)},
		{"31 dec", at(2026, 12, 31, 23, 59, 59)},
		{"sept 9", at(2027, 9, 9, 23, 59, 59)}, // already gone this year: next year, and the preview says so
		{"oct 3 11pm", at(2026, 10, 3, 23, 0, 0)},
		{"dec 31 12am", at(2026, 12, 31, 0, 0, 0)},
		{"dec 31 12pm", at(2026, 12, 31, 12, 0, 0)},
		{"feb 29", at(2028, 2, 29, 23, 59, 59)},
	}
	for _, c := range cases {
		got, never, err := ParseExpiry(c.in, now, ny)
		if c.want.IsZero() {
			if err == nil || never {
				t.Errorf("%q: want an error, got %v", c.in, got)
			}
			continue
		}
		if err != nil || never || !got.Equal(c.want) {
			t.Errorf("%q: got %v (never %v, %v), want %v", c.in, got, never, err, c.want)
		}
	}
	for _, in := range []string{"", "  ", "never", "Never", "none"} {
		if _, never, err := ParseExpiry(in, now, ny); err != nil || !never {
			t.Errorf("%q must mean never: %v %v", in, never, err)
		}
	}
}

func TestParseExpiryRefusesGuesses(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 10, 3, 12, 30, 0, 0, ny)
	for _, in := range []string{"soon", "5", "2026", "12/31/2026", "31/12", "0d", "-3d", "3 m", "1.5y", "200y", "dec 32", "feb 30", "2026-02-30",
		"2026-13-01", "13pm", "dec 31 25:00", "dec 31 10:75", "30d 5pm", "next", "monthly", "dec", "tomorrow tomorrow", "99999999d"} {
		_, never, err := ParseExpiry(in, now, ny)
		if err == nil || never {
			t.Errorf("%q must be refused", in)
			continue
		}
		if !errors.Is(err, ErrExpiryUnreadable) || !strings.Contains(err.Error(), "30d") {
			t.Errorf("%q: the error must carry the hint: %v", in, err)
		}
	}
	// the past is named as such, not called unreadable
	for _, in := range []string{"2020-01-01", "oct 3 2026 9am", "2026-10-03 12:30"} {
		_, _, err := ParseExpiry(in, now, ny)
		if err == nil || errors.Is(err, ErrExpiryUnreadable) || !strings.Contains(err.Error(), "already in the past") {
			t.Errorf("%q: %v", in, err)
		}
	}
}

// The zone decides what a date means: the same words are different moments in different zones.
func TestParseExpiryUsesTheZone(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	a, _, _ := ParseExpiry("2026-12-31", now, ny)
	b, _, _ := ParseExpiry("2026-12-31", now, tokyo)
	saved := time.Local
	time.Local = ny // the admin shows everything in the server's zone
	defer func() { time.Local = saved }()
	if a.Equal(b) || b.Sub(a) != -14*time.Hour { // Tokyo's midnight comes 14 hours before New York's in December
		t.Fatalf("%v %v", a, b)
	}
	if got := ExpiryPreview(a); got != "Expires Thu Dec 31, 2026, 11:59 PM EST" {
		t.Fatalf("%q", got)
	}
	if got := ExpiryText(a); got != "2026-12-31" {
		t.Fatalf("%q", got)
	}
	c, _, _ := ParseExpiry("2026-12-31 18:00", now, ny)
	if got := ExpiryText(c); got != "2026-12-31 18:00" {
		t.Fatalf("%q", got)
	}
	for _, v := range []time.Time{a, c} {
		back, _, err := ParseExpiry(ExpiryText(v), now, ny)
		if err != nil || !back.Equal(v) {
			t.Errorf("the text of %v must read back to it: %v %v", v, back, err)
		}
	}
}

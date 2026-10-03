package vkeys

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/helv-io/skgate/internal/timefmt"
)

// ExpiryHint is shown beside the field whenever the text is not understood. Nothing is guessed.
const ExpiryHint = "Try 30d, 2w, 6 months, tomorrow, friday, 2026-12-31 or dec 31 (add a time like 5pm or 17:30 if you need one); leave empty or type never for no expiration."

// ErrExpiryUnreadable is returned for text that is not an expiration; its message carries the hint.
var ErrExpiryUnreadable = errors.New("not understood")

// ExpiryPreview is the one-line answer shown under the field: "Expires Fri Dec 31, 2026, 11:59 PM EST".
func ExpiryPreview(t time.Time) string { return "Expires " + timefmt.Long(t) }

// ExpiryText is the form value that reads back to the same moment: the date alone for an end of day, else date and time.
func ExpiryText(t time.Time) string {
	if l := t.Local(); l.Hour() == 23 && l.Minute() == 59 && l.Second() == 59 {
		return timefmt.Date(t)
	}
	return timefmt.DateTime(t)
}

var (
	reTime     = regexp.MustCompile(`^(.*?)\s*(?:\bat\s+)?\s*(\d{1,2})(?::(\d{2}))?\s*(am|pm)?$`)
	reRelative = regexp.MustCompile(`^(\d{1,6})\s*([a-z]+)$`)
	reISO      = regexp.MustCompile(`^(\d{4})-(\d{1,2})-(\d{1,2})$`)
	reMonthDay = regexp.MustCompile(`^([a-z]{3,9})\.?\s+(\d{1,2})(?:st|nd|rd|th)?(?:\s+(\d{4}))?$`)
	reDayMonth = regexp.MustCompile(`^(\d{1,2})(?:st|nd|rd|th)?\s+([a-z]{3,9})\.?(?:\s+(\d{4}))?$`)
	reSpaces   = regexp.MustCompile(`\s+`)
	reISOT     = regexp.MustCompile(`(\d)t(\d)`)
)

var months = []string{"january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"}
var weekdays = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

func monthOf(name string) (time.Month, bool) {
	if len(name) < 3 {
		return 0, false
	}
	for i, m := range months {
		if strings.HasPrefix(m, name) {
			return time.Month(i + 1), true
		}
	}
	return 0, false
}

func weekdayOf(name string) (time.Weekday, bool) {
	if len(name) < 3 {
		return 0, false
	}
	for i, d := range weekdays {
		if strings.HasPrefix(d, name) {
			return time.Weekday(i), true
		}
	}
	return 0, false
}

func unreadable(in string) error {
	return fmt.Errorf("%w: %q. %s", ErrExpiryUnreadable, in, ExpiryHint)
}

// ParseExpiry reads what an administrator typed into the expiration field, in the zone loc (the server's local zone in the admin), relative to now.
//
//	""  "never"                         no expiration (never is true)
//	30d  2w  6 months  1y  12h  90min   that long from now (also "in 30 days")
//	today  tomorrow  friday             the end of that day (a weekday means its next occurrence)
//	2026-12-31  dec 31  31 dec 2027     the end of that day (a missing year means the next time that date comes)
//	... 5pm  ... 17:30  ... at 9:15am   a date followed by a time of day
//
// A moment that is not after now is refused, as is anything else it cannot read.
func ParseExpiry(input string, now time.Time, loc *time.Location) (t time.Time, never bool, err error) {
	if loc == nil {
		loc = time.Local
	}
	now = now.In(loc)
	s := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(input, ",", " ")))
	s = reSpaces.ReplaceAllString(s, " ")
	switch s {
	case "", "never", "none", "no", "no expiry", "no expiration", "off":
		return time.Time{}, true, nil
	}
	s = reISOT.ReplaceAllString(s, "$1 $2") // 2026-12-31T18:00
	s = strings.TrimPrefix(s, "in ")
	s = strings.TrimPrefix(s, "on ")

	// optional time of day at the end: 17:30, 5pm, 9:15 am, at 5pm. A bare number is a day or a year, not an hour.
	hour, min, hasTime := 0, 0, false
	rest := s
	if m := reTime.FindStringSubmatch(s); m != nil && (m[3] != "" || m[4] != "") && m[1] != "" {
		h, _ := strconv.Atoi(m[2])
		mi := 0
		if m[3] != "" {
			mi, _ = strconv.Atoi(m[3])
		}
		switch {
		case m[4] != "" && (h < 1 || h > 12), m[4] == "" && h > 23, mi > 59:
			return time.Time{}, false, unreadable(input)
		case m[4] == "pm" && h < 12:
			h += 12
		case m[4] == "am" && h == 12:
			h = 0
		}
		hour, min, hasTime = h, mi, true
		rest = strings.TrimSpace(m[1])
	}

	at := func(y int, mo time.Month, d int) (time.Time, bool) {
		h, mi, sec := 23, 59, 59
		if hasTime {
			h, mi, sec = hour, min, 0
		}
		v := time.Date(y, mo, d, h, mi, sec, 0, loc)
		return v, v.Day() == d && v.Month() == mo // false for Feb 30 and the like
	}
	// withYear builds month/day in year, or, with no year, in the next year the moment lies ahead.
	withYear := func(mo time.Month, d int, ys string) (time.Time, error) {
		if ys != "" {
			y, _ := strconv.Atoi(ys)
			v, ok := at(y, mo, d)
			if !ok {
				return time.Time{}, unreadable(input)
			}
			return v, nil
		}
		for y := now.Year(); y <= now.Year()+8; y++ { // 8 covers Feb 29
			if v, ok := at(y, mo, d); ok && v.After(now) {
				return v, nil
			}
		}
		return time.Time{}, unreadable(input)
	}

	var out time.Time
	switch {
	case reRelative.MatchString(rest) && !hasTime && relativeUnit(reRelative.FindStringSubmatch(rest)[2]) != "":
		m := reRelative.FindStringSubmatch(rest)
		n, _ := strconv.Atoi(m[1])
		switch relativeUnit(m[2]) {
		case "minute":
			out = now.Add(time.Duration(n) * time.Minute)
		case "hour":
			out = now.Add(time.Duration(n) * time.Hour)
		case "day":
			out = now.AddDate(0, 0, n)
		case "week":
			out = now.AddDate(0, 0, 7*n)
		case "month":
			out = now.AddDate(0, n, 0)
		default:
			out = now.AddDate(n, 0, 0)
		}
		if n == 0 || out.After(now.AddDate(100, 0, 0)) {
			return time.Time{}, false, unreadable(input)
		}
	case rest == "today" || rest == "tomorrow":
		d := now
		if rest == "tomorrow" {
			d = now.AddDate(0, 0, 1)
		}
		out, _ = at(d.Year(), d.Month(), d.Day())
	case weekdayName(rest) >= 0:
		wd := time.Weekday(weekdayName(rest))
		days := (int(wd) - int(now.Weekday()) + 7) % 7
		if days == 0 {
			days = 7
		}
		d := now.AddDate(0, 0, days)
		out, _ = at(d.Year(), d.Month(), d.Day())
	case reISO.MatchString(rest):
		m := reISO.FindStringSubmatch(rest)
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		v, ok := at(y, time.Month(mo), d)
		if !ok {
			return time.Time{}, false, unreadable(input)
		}
		out = v
	case reMonthDay.MatchString(rest):
		m := reMonthDay.FindStringSubmatch(rest)
		mo, ok := monthOf(m[1])
		if !ok {
			return time.Time{}, false, unreadable(input)
		}
		d, _ := strconv.Atoi(m[2])
		if out, err = withYear(mo, d, m[3]); err != nil {
			return time.Time{}, false, err
		}
	case reDayMonth.MatchString(rest):
		m := reDayMonth.FindStringSubmatch(rest)
		mo, ok := monthOf(m[2])
		if !ok {
			return time.Time{}, false, unreadable(input)
		}
		d, _ := strconv.Atoi(m[1])
		if out, err = withYear(mo, d, m[3]); err != nil {
			return time.Time{}, false, err
		}
	default:
		return time.Time{}, false, unreadable(input)
	}
	if !out.After(now) {
		return time.Time{}, false, fmt.Errorf("%s is already in the past", timefmt.Long(out))
	}
	return out, false, nil
}

// weekdayName returns the weekday number of "friday", "fri" or "next friday", else -1.
func weekdayName(s string) int {
	s = strings.TrimPrefix(s, "next ")
	if d, ok := weekdayOf(s); ok {
		return int(d)
	}
	return -1
}

// relativeUnit names the unit of "30d", "6 months" and the like; "" when the word is not a unit.
func relativeUnit(u string) string {
	switch u {
	case "min", "mins", "minute", "minutes":
		return "minute"
	case "h", "hr", "hrs", "hour", "hours":
		return "hour"
	case "d", "day", "days":
		return "day"
	case "w", "wk", "wks", "week", "weeks":
		return "week"
	case "mo", "mos", "month", "months":
		return "month"
	case "y", "yr", "yrs", "year", "years":
		return "year"
	}
	return ""
}

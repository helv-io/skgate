// Package timefmt renders every displayed timestamp in the server's local time zone (TZ, or UTC when
// unset), so the admin UI and the logs agree. Use these helpers instead of formatting times directly.
package timefmt

import (
	"fmt"
	"time"
)

// Layouts, one per display need.
const (
	dateLayout     = "2006-01-02"
	minuteLayout   = "15:04"
	secondLayout   = "15:04:05"
	dateTimeLayout = "2006-01-02 15:04"
	stampLayout    = "Jan 2 15:04 MST"
	logLayout      = "2006/01/02 15:04:05"
	longLayout     = "Mon Jan 2, 2006, 3:04 PM MST"
)

// Date is "2026-10-02".
func Date(t time.Time) string { return t.Local().Format(dateLayout) }

// Minute is "19:04".
func Minute(t time.Time) string { return t.Local().Format(minuteLayout) }

// Second is "19:04:05".
func Second(t time.Time) string { return t.Local().Format(secondLayout) }

// DateTime is "2026-10-02 19:04".
func DateTime(t time.Time) string { return t.Local().Format(dateTimeLayout) }

// Stamp is "Oct 2 19:04 EDT": short, with the zone abbreviation.
func Stamp(t time.Time) string { return t.Local().Format(stampLayout) }

// Long is "Fri Dec 31, 2026, 11:59 PM EST": weekday, date, 12-hour time and zone, for a moment typed by an administrator.
func Long(t time.Time) string { return t.Local().Format(longLayout) }

// Log is the prefix layout of skgate's own log lines, "2026/10/02 19:04:05".
func Log(t time.Time) string { return t.Local().Format(logLayout) }

// RFC3339 is "2026-10-02T19:04:05-04:00" in the local zone, for log lines that name a moment.
func RFC3339(t time.Time) string { return t.Local().Format(time.RFC3339) }

// Zone names the active zone for the startup line, for example "America/New_York (EDT)".
func Zone() string {
	name := time.Local.String()
	abbr, _ := time.Now().Zone()
	return name + " (" + abbr + ")"
}

// Latency renders how long a request took for a person: 5.8 ms below 10 ms, whole milliseconds below a second
// (435 ms), then seconds with one decimal (1.2 s).
func Latency(d time.Duration) string {
	switch {
	case d < 10*time.Millisecond:
		return fmt.Sprintf("%.1f ms", float64(d)/float64(time.Millisecond))
	case d < time.Second:
		return fmt.Sprintf("%d ms", d.Round(time.Millisecond)/time.Millisecond)
	default:
		return fmt.Sprintf("%.1f s", d.Seconds())
	}
}

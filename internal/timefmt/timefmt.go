// Package timefmt renders every displayed timestamp in the server's local time zone (TZ, or UTC when
// unset), so the admin UI and the logs agree. Use these helpers instead of formatting times directly.
package timefmt

import "time"

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

// Zone names the active zone for the startup line, for example "America/New_York (EDT)".
func Zone() string {
	name := time.Local.String()
	abbr, _ := time.Now().Zone()
	return name + " (" + abbr + ")"
}

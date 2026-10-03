package timefmt

import (
	"testing"
	"time"
)

// Every helper renders in time.Local, which the TZ variable selects.
func TestHelpersUseLocalZone(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Local
	time.Local = ny
	defer func() { time.Local = old }()
	u := time.Date(2026, 10, 3, 1, 4, 5, 0, time.UTC) // 21:04:05 the evening before in New York (EDT)
	for name, got := range map[string]string{
		"Date": Date(u), "Minute": Minute(u), "Second": Second(u), "DateTime": DateTime(u), "Stamp": Stamp(u), "Log": Log(u),
	} {
		want := map[string]string{"Date": "2026-10-02", "Minute": "21:04", "Second": "21:04:05", "DateTime": "2026-10-02 21:04",
			"Stamp": "Oct 2 21:04 EDT", "Log": "2026/10/02 21:04:05"}[name]
		if got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	if z := Zone(); z != "America/New_York (EDT)" {
		t.Errorf("zone %q", z)
	}
}

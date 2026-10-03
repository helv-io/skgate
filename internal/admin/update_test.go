package admin

import "testing"

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.7.9", "0.7.8", true}, {"v0.8.0", "0.7.8", true}, {"v1.0.0", "0.99.99", true}, {"v0.7.10", "0.7.9", true},
		{"v0.7.8", "0.7.8", false}, {"v0.7.7", "0.7.8", false}, {"v0.6.99", "0.7.0", false},
		{"v0.8.0-rc1", "0.7.8", false}, {"latest", "0.7.8", false}, {"v1.2", "0.7.8", false}, {"v0.8.0", "dev", false}, {"", "0.7.8", false}, {"v+1.0.0", "0.7.8", false},
	} {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

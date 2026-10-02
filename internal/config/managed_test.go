package config

import (
	"path/filepath"
	"testing"
	"time"
)

func clearManaged(t *testing.T) {
	for _, k := range []string{"MANAGED_DIR", "MANAGED_CACHE_DIR", "MANAGED_MAX_PROCS", "MANAGED_STOP_GRACE", "MANAGED_LOG_LINES", "MANAGED_INSTALL_TIMEOUT"} {
		t.Setenv(k, "")
	}
}

func TestManagedDefaults(t *testing.T) {
	clearManaged(t)
	t.Setenv("DB_PATH", "/var/lib/sk/skgate.db")
	c := Load()
	if c.ManagedCacheDir != filepath.Join("/var/lib/sk", "cache", "managed") || c.ManagedDir != filepath.Join("/var/lib/sk", "managed") || c.ManagedMaxProcs != 0 || c.ManagedStopGrace != 5*time.Second ||
		c.ManagedLogLines != 2000 || c.ManagedInstallMax != 15*time.Minute {
		t.Fatalf("defaults: %+v", c)
	}
}

// The rarely needed managed knobs are internal defaults now: their old variables have no effect.
func TestManagedOverridesAndRemovedKnobs(t *testing.T) {
	clearManaged(t)
	t.Setenv("DB_PATH", "/var/lib/sk/skgate.db")
	t.Setenv("MANAGED_DIR", "/srv/m")
	t.Setenv("MANAGED_MAX_PROCS", "3")
	t.Setenv("MANAGED_CACHE_DIR", "/elsewhere")
	t.Setenv("MANAGED_STOP_GRACE", "12")
	t.Setenv("MANAGED_LOG_LINES", "500")
	t.Setenv("MANAGED_INSTALL_TIMEOUT", "90s")
	c := Load()
	if c.ManagedDir != "/srv/m" || c.ManagedMaxProcs != 3 {
		t.Fatalf("overrides: %+v", c)
	}
	if c.ManagedCacheDir != filepath.Join("/var/lib/sk", "cache", "managed") || c.ManagedStopGrace != 5*time.Second || c.ManagedLogLines != 2000 || c.ManagedInstallMax != 15*time.Minute {
		t.Fatalf("removed variables must be ignored: %+v", c)
	}
	t.Setenv("MANAGED_MAX_PROCS", "many")
	if Load().ManagedMaxProcs != 0 {
		t.Fatal("a bad MANAGED_MAX_PROCS falls back to 0")
	}
}

func TestManagedMaxProcsZeroAndNegative(t *testing.T) {
	clearManaged(t)
	t.Setenv("MANAGED_MAX_PROCS", "0")
	if Load().ManagedMaxProcs != 0 {
		t.Fatal("0 means unlimited")
	}
	t.Setenv("MANAGED_MAX_PROCS", "-2")
	if Load().ManagedMaxProcs != 0 {
		t.Fatal("a negative value falls back to unlimited")
	}
}

func TestManagedAvailabilityFollowsEdition(t *testing.T) {
	old := Edition
	defer func() { Edition = old }()
	Edition = "full"
	if !ManagedAvailable() {
		t.Fatal("the full edition always has managed upstreams")
	}
	Edition = "slim"
	if ManagedAvailable() {
		t.Fatal("the slim edition never has managed upstreams")
	}
}

package config

import (
	"os"
	"strings"
)

// removedEnv lists variables older releases read. They are ignored now; MigrateLegacyEnv carries the
// ones that changed behavior into the settings store once.
var removedEnv = []string{"UPSTREAM_BASE", "UPSTREAM_FALLBACK", "XAI_ISSUER", "XAI_CLIENT_ID", "XAI_SCOPES", "XAI_REDIRECT_URI",
	"MCP_ALLOW_QUERY_KEY", "MANAGED_CACHE_DIR", "MANAGED_STOP_GRACE", "MANAGED_LOG_LINES", "MANAGED_INSTALL_TIMEOUT"}

const legacyEnvDone = "legacy_env_migrated"

// MigrateLegacyEnv runs once per database. It stores MCP_ALLOW_QUERY_KEY as the old global switch (which
// vkeys.MigrateGlobalURLKey turns into the per-key switch) and
// UPSTREAM_BASE/UPSTREAM_FALLBACK as the Grok provider's base and fallback, so a deployment that set
// them keeps its effective behavior. A value already stored in the UI is never overwritten.
func MigrateLegacyEnv(kv KV) {
	if _, done := kv.GetSetting(legacyEnvDone); done {
		return
	}
	failed := false
	put := func(key, v string) {
		if _, have := kv.GetSetting(key); !have {
			if kv.SetSetting(key, v) != nil {
				failed = true
			}
		}
	}
	if v, ok := os.LookupEnv("MCP_ALLOW_QUERY_KEY"); ok {
		put("allow_query_key", map[bool]string{true: "1", false: "0"}[truthy(v)])
	}
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("UPSTREAM_BASE")), "/"); v != "" {
		put("provider.grok.base", v)
	}
	if v, ok := os.LookupEnv("UPSTREAM_FALLBACK"); ok {
		put("provider.grok.fallback", strings.TrimRight(strings.TrimSpace(v), "/")) // empty keeps fallback off
	}
	if !failed { // a failed write is tried again at the next start, not forgotten
		_ = kv.SetSetting(legacyEnvDone, "1")
	}
}

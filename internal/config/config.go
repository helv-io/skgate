// Package config loads skgate settings from the environment, with runtime overrides from SQLite.
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Version is the release.
const Version = "0.7.4"

// Edition is "full" (managed MCP upstreams available) or "slim" (proxy only). The slim image sets
// it at link time: -ldflags "-X github.com/helv-io/skgate/internal/config.Edition=slim".
var Edition = "full"

// Managed process defaults.
const (
	DefaultManagedMaxProcs   = 0 // 0 = unlimited
	DefaultManagedStopGrace  = 5 * time.Second
	DefaultManagedLogLines   = 2000
	DefaultManagedInstallMax = 15 * time.Minute
)

// KV is the runtime settings store (implemented by store.DB).
type KV interface {
	GetSetting(key string) (string, bool)
	SetSetting(key, value string) error
}

// Config holds environment configuration.
type Config struct {
	PublicURL        string // origin without trailing slash
	Listen           string
	DBPath           string
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCScopes       string
	OIDCRedirectURL  string // empty means PublicURL + /admin/oidc/callback
	OIDCEmails       []string
	OIDCGroups       []string
	RequireConsent   bool
	LogLevel         string // info (default) or debug
	// Managed MCP upstreams (child processes run by skgate).
	ManagedDir        string        // MANAGED_DIR: per-alias work dirs, clones and homes
	ManagedCacheDir   string        // per-alias package caches (npm, uv, pip), <db dir>/cache/managed
	ManagedMaxProcs   int           // MANAGED_MAX_PROCS: concurrent child processes, 0 = unlimited
	ManagedStopGrace  time.Duration // SIGTERM to SIGKILL delay (internal default)
	ManagedLogLines   int           // stderr lines kept per process (internal default)
	ManagedInstallMax time.Duration // limit for one install step (internal default)
	SecretsKey        string        // SECRETS_KEY: encryption key for stored secrets (empty: key file next to the database)
	kv                KV
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// intEnv reads an integer variable, falling back to def when unset, invalid or out of [min, max].
func intEnv(k string, def, min, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(k)))
	if err != nil || n < min || n > max {
		return def
	}
	return n
}

// ManagedAvailable reports whether this build can run managed upstreams: the full edition can, the
// slim edition (proxy only) never does. There is no runtime switch.
func ManagedAvailable() bool { return Edition != "slim" }

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// OIDCEnabled reports whether admin SSO is configured. Without it the admin UI is closed.
func (c *Config) OIDCEnabled() bool {
	return c.OIDCIssuer != "" && c.OIDCClientID != "" && c.OIDCClientSecret != ""
}

// OIDCRedirect returns the callback URL registered at the identity provider.
func (c *Config) OIDCRedirect() string {
	if c.OIDCRedirectURL != "" {
		return c.OIDCRedirectURL
	}
	return c.PublicURL + "/admin/oidc/callback"
}

// Load reads the environment.
func Load() *Config {
	c := &Config{
		PublicURL:         strings.TrimRight(env("PUBLIC_URL", "http://localhost:8080"), "/"),
		Listen:            env("LISTEN_ADDR", ":8080"),
		DBPath:            env("DB_PATH", "/data/skgate.db"),
		OIDCIssuer:        strings.TrimSpace(os.Getenv("OIDC_ISSUER")),
		OIDCClientID:      strings.TrimSpace(os.Getenv("OIDC_CLIENT_ID")),
		OIDCClientSecret:  strings.TrimSpace(os.Getenv("OIDC_CLIENT_SECRET")),
		OIDCScopes:        env("OIDC_SCOPES", "openid profile email groups"),
		OIDCRedirectURL:   strings.TrimSpace(os.Getenv("OIDC_REDIRECT_URL")),
		OIDCEmails:        splitList(os.Getenv("OIDC_ALLOWED_EMAILS")),
		OIDCGroups:        splitList(os.Getenv("OIDC_ALLOWED_GROUPS")),
		RequireConsent:    truthy(os.Getenv("MCP_OAUTH_REQUIRE_CONSENT")),
		LogLevel:          strings.ToLower(env("LOG_LEVEL", "info")),
		SecretsKey:        strings.TrimSpace(os.Getenv("SECRETS_KEY")),
		ManagedDir:        strings.TrimSpace(os.Getenv("MANAGED_DIR")),
		ManagedMaxProcs:   intEnv("MANAGED_MAX_PROCS", DefaultManagedMaxProcs, 0, 100000),
		ManagedStopGrace:  DefaultManagedStopGrace,
		ManagedLogLines:   DefaultManagedLogLines,
		ManagedInstallMax: DefaultManagedInstallMax,
	}
	if c.ManagedDir == "" {
		c.ManagedDir = filepath.Join(filepath.Dir(c.DBPath), "managed")
	}
	c.ManagedCacheDir = filepath.Join(filepath.Dir(c.DBPath), "cache", "managed")
	return c
}

// Bind attaches the runtime settings store.
func (c *Config) Bind(kv KV) { c.kv = kv }

func (c *Config) get(key string) (string, bool) {
	if c.kv == nil {
		return "", false
	}
	return c.kv.GetSetting(key)
}

// QueryKeyAllowed reports whether ?key= is accepted on MCP endpoints: the keys page switch, off by default.
func (c *Config) QueryKeyAllowed() bool {
	if v, ok := c.get("allow_query_key"); ok {
		return v == "1"
	}
	return false
}

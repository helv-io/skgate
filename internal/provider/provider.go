// Package provider defines a sign-in provider (an account whose OAuth tokens skgate holds and whose
// OpenAI-compatible API it proxies), the registry of providers and their per-provider settings.
package provider

import (
	"context"
	"strings"
	"time"
)

// Provider is one upstream account. Grok is the first; others plug in by implementing this.
type Provider interface {
	ID() string   // stable, lowercase: settings and admin routes are namespaced by it
	Name() string // display name
	// DefaultBase and DefaultFallback are the API bases used until the admin overrides them.
	DefaultBase() string
	DefaultFallback() string
	// Headers are extra request headers the provider's hosts require for the given base.
	Headers(base string) map[string]string

	Token(ctx context.Context) (string, error)
	ForceRefresh(ctx context.Context) (string, error)
	Refresh(ctx context.Context) error
	SignOut()
	Status() Status

	StartDevice(ctx context.Context) (DeviceFlow, error)
	Device() DeviceFlow
	CancelDevice()
	StartBrowser(ctx context.Context) string
	BrowserPending() bool
	FinishBrowser(ctx context.Context, code, state string) error

	// Info lists the technical facts shown in the provider dialog (client, scopes, endpoints, PKCE).
	Info() []InfoRow
	// Run keeps tokens fresh until ctx ends.
	Run(ctx context.Context)
}

// InfoRow is one name/value line of technical information.
type InfoRow struct{ Name, Value string }

// Status describes the stored sign-in state. It never includes token values.
type Status struct {
	SignedIn  bool
	Expires   time.Time
	ExpiresIn time.Duration
	Account   string
	State     string // ok | reauth | tier_blocked | ""
	LastError string
	// AccessMasked and RefreshMasked are asterisks plus the last 4 characters (display only).
	AccessMasked, RefreshMasked string
}

// DeviceFlow is a snapshot of a running device-code sign-in.
type DeviceFlow struct {
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresAt               time.Time
	Interval                time.Duration
	State                   string // pending | done | error
	Err                     string
	DeviceCode              string // internal; cleared in snapshots
}

// Registry holds the providers in display order.
type Registry struct{ list []Provider }

// NewRegistry returns a registry of the given providers.
func NewRegistry(ps ...Provider) *Registry { return &Registry{list: ps} }

// List returns the providers in order.
func (r *Registry) List() []Provider { return r.list }

// Get returns the provider with the given id.
func (r *Registry) Get(id string) (Provider, bool) {
	for _, p := range r.list {
		if p.ID() == id {
			return p, true
		}
	}
	return nil, false
}

// Default is the provider that serves the API proxy (the first one).
func (r *Registry) Default() Provider {
	if len(r.list) == 0 {
		return nil
	}
	return r.list[0]
}

// Pending returns the provider with a browser sign-in in progress.
func (r *Registry) Pending() (Provider, bool) {
	for _, p := range r.list {
		if p.BrowserPending() {
			return p, true
		}
	}
	return nil, false
}

// KV is the settings store (implemented by store.DB).
type KV interface {
	GetSetting(key string) (string, bool)
	SetSetting(key, value string) error
	DeleteSetting(key string) error
}

// Settings reads and writes per-provider settings; keys are "provider.<id>.<name>".
type Settings struct{ KV KV }

func key(id, name string) string { return "provider." + id + "." + name }

// Get returns a setting or "" and whether it is set.
func (s Settings) Get(id, name string) (string, bool) { return s.KV.GetSetting(key(id, name)) }

// Set stores a setting.
func (s Settings) Set(id, name, v string) error { return s.KV.SetSetting(key(id, name), v) }

// Delete removes a setting.
func (s Settings) Delete(id, name string) error { return s.KV.DeleteSetting(key(id, name)) }

// Defaults are the provider facts the settings need.
type Defaults interface {
	ID() string
	DefaultBase() string
	DefaultFallback() string
}

// Base is the effective API base: the admin's override, else the provider default.
func (s Settings) Base(p Defaults) string {
	if v, ok := s.Get(p.ID(), "base"); ok && v != "" {
		return strings.TrimRight(v, "/")
	}
	return strings.TrimRight(p.DefaultBase(), "/")
}

// Fallback is the effective fallback base (may be empty; an explicit empty override disables it).
func (s Settings) Fallback(p Defaults) string {
	if v, ok := s.Get(p.ID(), "fallback"); ok {
		return strings.TrimRight(v, "/")
	}
	return strings.TrimRight(p.DefaultFallback(), "/")
}

// MigrateLegacy moves the global upstream settings of older releases to the given provider.
func (s Settings) MigrateLegacy(id string) {
	for old, name := range map[string]string{"upstream_base": "base", "upstream_fallback": "fallback"} {
		v, ok := s.KV.GetSetting(old)
		if !ok {
			continue
		}
		if _, have := s.Get(id, name); !have {
			_ = s.Set(id, name, v)
		}
		_ = s.KV.DeleteSetting(old)
	}
}

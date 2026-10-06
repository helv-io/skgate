package keyed

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/provider"
	"github.com/helv-io/skgate/internal/store"
)

// Provider is one preset, configured with an API key. It implements provider.Provider (without the OAuth sign-in,
// which it declines) and the proxy's optional abilities Ready, StaticKey and, for Gemini, FilterModels.
//
// Settings: provider.<id>.enabled ("1" once added), provider.<id>.key (sealed), provider.<id>.base (override).
type Provider struct {
	Preset Preset
	DB     *store.DB
}

// New returns the provider for every preset, in preset order. A preset counts as added once it is enabled.
func New(db *store.DB) []provider.Provider {
	var out []provider.Provider
	for _, pr := range Presets {
		p := &Provider{Preset: pr, DB: db}
		if pr.Adapter == "anthropic" {
			out = append(out, &Anthropic{Provider: p})
		} else {
			out = append(out, p)
		}
	}
	return out
}

// Keyed reports whether a provider is one of these.
func Keyed(p any) (*Provider, bool) {
	switch v := p.(type) {
	case *Provider:
		return v, true
	case *Anthropic:
		return v.Provider, true
	}
	return nil, false
}

func (p *Provider) ID() string              { return p.Preset.ID }
func (p *Provider) Name() string            { return p.Preset.Name }
func (p *Provider) DefaultBase() string     { return p.Preset.Base }
func (p *Provider) DefaultFallback() string { return "" }

// Headers implements provider.Provider: none are needed.
func (p *Provider) Headers(string) map[string]string { return nil }

func (p *Provider) keyName() string { return "provider." + p.Preset.ID + ".key" }

// Key is the stored API key ("" when none; an error when it cannot be decrypted).
func (p *Provider) Key() (string, error) {
	v, _, err := p.DB.GetSecret(p.keyName())
	return v, err
}

// SaveKey stores the API key sealed; an empty key removes it.
func (p *Provider) SaveKey(key string) error {
	if key = strings.TrimSpace(key); key == "" {
		return p.DB.DeleteSetting(p.keyName())
	}
	return p.DB.SetSecret(p.keyName(), key)
}

// Enabled reports whether the provider was added.
func (p *Provider) Enabled() bool {
	v, _ := p.DB.GetSetting("provider." + p.Preset.ID + ".enabled")
	return v == "1"
}

// SetEnabled adds or removes the provider.
func (p *Provider) SetEnabled(on bool) error {
	if !on {
		return p.DB.DeleteSetting("provider." + p.Preset.ID + ".enabled")
	}
	return p.DB.SetSetting("provider."+p.Preset.ID+".enabled", "1")
}

// Remove forgets the key, the base override and the enabled flag. Aliases and the helper keep their own cleanup.
func (p *Provider) Remove() {
	for _, n := range []string{"enabled", "key", "base", "models", "prefix"} {
		_ = p.DB.DeleteSetting("provider." + p.Preset.ID + "." + n)
	}
}

// Ready implements the proxy's Readier: added, and holding a key when one is needed.
func (p *Provider) Ready() bool {
	if !p.Enabled() {
		return false
	}
	if !p.Preset.NeedsKey {
		return true
	}
	k, err := p.Key()
	return err == nil && k != ""
}

// StaticKey tells the proxy a 401 is final: there is no token to refresh.
func (p *Provider) StaticKey() bool { return true }

// Token implements provider.Provider: the API key. A server that needs none yields "", and the proxy sends no header.
func (p *Provider) Token(context.Context) (string, error) {
	k, err := p.Key()
	if err != nil {
		return "", errors.New("the saved API key cannot be opened (check SECRETS_KEY)")
	}
	if k == "" && p.Preset.NeedsKey {
		return "", errors.New("no API key saved for " + p.Preset.Name)
	}
	return k, nil
}

// ForceRefresh implements provider.Provider: a key cannot be refreshed.
func (p *Provider) ForceRefresh(ctx context.Context) (string, error) { return p.Token(ctx) }

// Refresh, SignOut and the sign-in flows implement provider.Provider; a key has none.
func (p *Provider) Refresh(context.Context) error { return nil }
func (p *Provider) SignOut()                      {}
func (p *Provider) StartDevice(context.Context) (provider.DeviceFlow, error) {
	return provider.DeviceFlow{}, errors.New("this provider uses an API key")
}
func (p *Provider) Device() provider.DeviceFlow         { return provider.DeviceFlow{} }
func (p *Provider) CancelDevice()                       {}
func (p *Provider) StartBrowser(context.Context) string { return "" }
func (p *Provider) BrowserPending() bool                { return false }
func (p *Provider) FinishBrowser(context.Context, string, string) error {
	return errors.New("this provider uses an API key")
}

// Run implements provider.Provider: nothing to keep fresh.
func (p *Provider) Run(context.Context) {}

// Status implements provider.Provider: SignedIn means ready; the key is shown masked.
func (p *Provider) Status() provider.Status {
	s := provider.Status{SignedIn: p.Ready()}
	if k, err := p.Key(); err != nil {
		s.State, s.LastError = "secret_error", "the saved API key cannot be opened (check SECRETS_KEY)"
	} else {
		s.AccessMasked = httputil.Mask(k)
	}
	return s
}

// Info implements provider.Provider.
func (p *Provider) Info() []provider.InfoRow {
	rows := []provider.InfoRow{{Name: "Default base", Value: p.Preset.Base}}
	if p.Preset.Adapter == "anthropic" {
		rows = append(rows, provider.InfoRow{Name: "API", Value: "Anthropic Messages, translated to the OpenAI API"})
	}
	return rows
}

// FilterModels implements the proxy's ListFilter: Gemini lists ids as "models/<id>", which its chat endpoint
// does not take back, so the prefix is dropped.
func (p *Provider) FilterModels(raw []byte) []byte {
	if p.Preset.ID != "gemini" {
		return raw
	}
	var top map[string]json.RawMessage
	var data []map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil || json.Unmarshal(top["data"], &data) != nil {
		return raw
	}
	for _, d := range data {
		var id string
		if json.Unmarshal(d["id"], &id) == nil {
			d["id"], _ = json.Marshal(strings.TrimPrefix(id, "models/"))
		}
	}
	top["data"], _ = json.Marshal(data)
	out, err := json.Marshal(top)
	if err != nil {
		return raw
	}
	return out
}

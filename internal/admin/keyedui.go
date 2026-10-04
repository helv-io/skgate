package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/helv-io/skgate/internal/provider"
	"github.com/helv-io/skgate/internal/provider/keyed"
)

// keyedOf returns the key-based provider of the request, or answers 404.
func keyedOf(w http.ResponseWriter, r *http.Request) (*keyed.Provider, bool) {
	k, ok := keyed.Keyed(providerOf(r))
	if !ok {
		http.NotFound(w, r)
	}
	return k, ok
}

// testConnection loads the provider's model list: the one request that proves the base URL and the key.
func (a *Admin) testConnection(ctx context.Context, p provider.Provider) (int, error) {
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ids, err := a.Proxy.FetchModelsOf(c, p.ID())
	return len(ids), err
}

func connectionResult(name string, n int, err error) (ok, bad string) {
	if err != nil {
		return "", name + ": connection failed: " + err.Error()
	}
	return fmt.Sprintf("%s: connected, %d models", name, n), ""
}

// settingBase stores the base URL override (none when it is the preset's own).
func (a *Admin) settingBase(k *keyed.Provider, base string) {
	if base == "" || base == strings.TrimRight(k.Preset.Base, "/") {
		_ = a.Set.Delete(k.ID(), "base")
		return
	}
	_ = a.Set.Set(k.ID(), "base", base)
}

// checkConnection validates the base URL and key a form sent for k. The error is shown as it is.
func checkConnection(k *keyed.Provider, base, key string, haveKey bool) (string, error) {
	base = cleanBase(base)
	if base == "" {
		base = cleanBase(k.Preset.Base)
	}
	if base == "" {
		return "", fmt.Errorf("enter the base URL of the API")
	}
	if !validBase(base) {
		return "", fmt.Errorf("the base URL must be an absolute http(s) address")
	}
	if k.Preset.NeedsKey && key == "" && !haveKey {
		return "", fmt.Errorf("enter the API key")
	}
	return base, nil
}

// keyedAdd adds a provider from a preset: base URL, API key, then a connection test. It saves even when the test
// fails (the server may start later) and says so.
func (a *Admin) keyedAdd(w http.ResponseWriter, r *http.Request) {
	pr, ok := keyed.PresetByID(r.PostFormValue("preset"))
	if !ok {
		a.back(w, r, "/admin", "", "choose a provider")
		return
	}
	p, _ := a.Providers.Get(pr.ID)
	k, _ := keyed.Keyed(p)
	key := strings.TrimSpace(r.PostFormValue("key"))
	_, have := k.Key()
	haveKey := have == nil && k.Ready()
	base, err := checkConnection(k, r.PostFormValue("base"), key, haveKey)
	if err != nil {
		a.back(w, r, "/admin#add-provider", "", err.Error())
		return
	}
	a.settingBase(k, base)
	if key != "" {
		_ = k.SaveKey(key)
	}
	_ = k.SetEnabled(true)
	n, terr := a.testConnection(r.Context(), k)
	ok2, bad := connectionResult(k.Name(), n, terr)
	if bad != "" {
		bad = k.Name() + " added, but the connection failed: " + strings.TrimPrefix(bad, k.Name()+": connection failed: ")
	}
	a.back(w, r, "/admin", ok2, bad)
}

// keyedSave changes the base URL and, when one is typed, the API key; then tests the connection.
func (a *Admin) keyedSave(w http.ResponseWriter, r *http.Request) {
	k, ok := keyedOf(w, r)
	if !ok {
		return
	}
	key := strings.TrimSpace(r.PostFormValue("key"))
	_, kerr := k.Key()
	base, err := checkConnection(k, r.PostFormValue("base"), key, kerr == nil && k.Ready())
	if err != nil {
		a.back(w, r, dialogHash(k.ID()), "", err.Error())
		return
	}
	a.settingBase(k, base)
	if key != "" {
		_ = k.SaveKey(key)
	}
	n, terr := a.testConnection(r.Context(), k)
	okMsg, bad := connectionResult(k.Name(), n, terr)
	if bad != "" {
		bad = "saved, but the connection failed: " + strings.TrimPrefix(bad, k.Name()+": connection failed: ")
	} else {
		okMsg = "saved; " + strings.TrimPrefix(okMsg, k.Name()+": ")
	}
	a.back(w, r, dialogHash(k.ID()), okMsg, bad)
}

func (a *Admin) keyedTest(w http.ResponseWriter, r *http.Request) {
	k, ok := keyedOf(w, r)
	if !ok {
		return
	}
	n, err := a.testConnection(r.Context(), k)
	okMsg, bad := connectionResult(k.Name(), n, err)
	a.back(w, r, dialogHash(k.ID()), okMsg, bad)
}

// keyedRemove forgets a provider with its key, base URL and aliases. A helper model that pointed into it is cleared.
func (a *Admin) keyedRemove(w http.ResponseWriter, r *http.Request) {
	k, ok := keyedOf(w, r)
	if !ok {
		return
	}
	def := a.Providers.Default().ID()
	helper := a.Set.Model(def)
	if ids, _, known := a.Proxy.Models.Get(k.ID()); isAlias(a.Set.Aliases(k.ID()), helper) || known && contains(ids, helper) {
		_ = a.Set.SetModel(def, "")
	}
	_ = a.Set.Delete(k.ID(), "aliases")
	k.Remove()
	a.Proxy.Models.Delete(k.ID())
	a.back(w, r, "/admin", k.Name()+" removed", "")
}

// readyProviders lists the providers that can take requests now.
func (a *Admin) readyProviders() []provider.Provider {
	var out []provider.Provider
	for _, p := range a.Providers.List() {
		if a.Proxy != nil && a.Proxy.IsReady(p.ID()) {
			out = append(out, p)
		}
	}
	return out
}

// knownModel reports whether name is something a request can use: an alias of any provider, or a model one of the
// ready providers lists (as far as its list is loaded).
func (a *Admin) knownModel(name string) bool {
	for _, p := range a.Providers.List() {
		if isAlias(a.Set.Aliases(p.ID()), name) {
			return true
		}
	}
	for _, p := range a.readyProviders() {
		if ids, _, ok := a.Proxy.Models.Get(p.ID()); ok && contains(ids, name) {
			return true
		}
	}
	return false
}

// allAliases lists the aliases of every provider.
func (a *Admin) allAliases() []provider.Alias {
	var out []provider.Alias
	for _, p := range a.Providers.List() {
		out = append(out, a.Set.Aliases(p.ID())...)
	}
	return out
}

// aliasRow is one line of the overview of where each alias points.
type aliasRow struct{ Name, Target, Provider string }

func (a *Admin) aliasRows() []aliasRow {
	var out []aliasRow
	for _, p := range a.Providers.List() {
		for _, al := range a.Set.Aliases(p.ID()) {
			out = append(out, aliasRow{al.Name, al.Target, p.Name()})
		}
	}
	return out
}

// presetView is one option of the Add provider dialog.
type presetView struct {
	keyed.Preset
	Added bool
}

func (a *Admin) presetViews() []presetView {
	var out []presetView
	for _, pr := range keyed.Presets {
		p, _ := a.Providers.Get(pr.ID)
		k, _ := keyed.Keyed(p)
		out = append(out, presetView{Preset: pr, Added: k != nil && k.Enabled()})
	}
	return out
}

// baseHost shows a base URL as host (and port) only.
func baseHost(base string) string {
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		return u.Host
	}
	return base
}

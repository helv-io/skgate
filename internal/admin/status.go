package admin

import (
	"fmt"
	"net/http"
	"time"

	"github.com/helv-io/skgate/internal/provider"
	"github.com/helv-io/skgate/internal/timefmt"
)

// statusData is the status page: one card per provider.
type statusData struct{ Providers []providerView }

// providerView is a provider as the status page and its dialog show it.
type providerView struct {
	ID, Name, CSRF string
	Inline         bool // the MCP helper model form posts in place (no page reload)
	S              provider.Status
	Dev            provider.DeviceFlow
	State          pillView // sign-in pill; the hover text carries the detail
	Expiry         string   // "in 59m", "expired" or ""
	Info           []provider.InfoRow

	Base, Fallback, DefaultBase string

	CanModels   bool
	Models      []string
	ModelsKnown bool
	ModelsAt    string
	Model       string
	ModelPill   pillView   // MCP helper model summary
	Effort      effortView // reasoning effort of the MCP helper model's calls
	ChatEffort  effortView // reasoning effort added to proxied chat requests
	Aliases     []aliasView
	AliasPill   pillView
}

// effortView is one reasoning-effort dropdown.
type effortView struct {
	Name, Value string
	Options     []effortOption
}

type effortOption struct{ Value, Label string }

func effortOf(name, value string) effortView {
	v := effortView{Name: name, Value: value}
	for _, e := range provider.Efforts {
		l := "Effort: " + e
		if e == "default" {
			l = "Effort: default (provider decides)"
		}
		v.Options = append(v.Options, effortOption{e, l})
	}
	return v
}

type aliasView struct {
	provider.Alias
	Pill pillView
}

// stateOf renders the sign-in state as one pill; every detail goes in the hover text.
func stateOf(s provider.Status) pillView {
	switch {
	case s.State == "tier_blocked":
		return pillView{"bad", "blocked", tipJoin("the account is not entitled to this API", s.LastError)}
	case s.State == "reauth" && !s.SignedIn:
		return pillView{"bad", "sign in again", tipJoin("the sign-in expired or was revoked", s.LastError)}
	case !s.SignedIn:
		return pillView{"bad", "not signed in", tipJoin("", s.LastError)}
	case s.ExpiresIn <= 0:
		return pillView{"warn", "expired", tipJoin("the access token expired and refreshes on the next request", s.LastError)}
	}
	return pillView{"ok", "signed in", tipJoin("", s.LastError)}
}

func tipJoin(a, b string) string {
	switch {
	case a != "" && b != "":
		return a + ": " + b
	case b != "":
		return "last error: " + b
	}
	return a
}

func expiryText(s provider.Status) string {
	if !s.SignedIn {
		return ""
	}
	if s.ExpiresIn <= 0 {
		return "expired"
	}
	return "in " + s.ExpiresIn.Round(time.Minute).String()
}

func (a *Admin) providerView(r *http.Request, p provider.Provider) providerView {
	id := p.ID()
	v := providerView{ID: id, Name: p.Name(), S: p.Status(), Dev: p.Device(), Info: p.Info(),
		Base: a.Set.Base(p), Fallback: a.Set.Fallback(p), DefaultBase: p.DefaultBase(), CanModels: a.proxyFor(id) != nil}
	v.State, v.Expiry = stateOf(v.S), expiryText(v.S)
	if v.CanModels {
		var at time.Time
		v.Models, at, v.ModelsKnown = a.models(r.Context(), p)
		if v.ModelsKnown {
			v.ModelsAt = timefmt.Minute(at)
		}
	}
	v.Model = a.Set.Model(id)
	v.Effort = effortOf("effort", a.Set.Effort(id))
	v.ChatEffort = effortOf("effort", a.Set.ChatEffort(id))
	switch {
	case v.Model == "":
		v.ModelPill = pillView{"off", "no model", "pick a model in the details to enable Suggest configuration"}
	case v.ModelsKnown && !contains(v.Models, v.Model):
		v.ModelPill = pillView{"warn", v.Model, "no longer in the provider's model list"}
	default:
		v.ModelPill = pillView{"ok", v.Model, "used as the MCP helper model"}
	}
	stale := 0
	for _, al := range a.Set.Aliases(id) {
		av := aliasView{Alias: al, Pill: pillView{"ok", "ok", ""}}
		if msg := provider.AliasIssue(al, v.Models, v.ModelsKnown); msg != "" {
			av.Pill = pillView{"warn", "stale", msg}
			stale++
		} else if !v.ModelsKnown {
			av.Pill = pillView{"off", "unchecked", "the model list is not loaded"}
		}
		v.Aliases = append(v.Aliases, av)
	}
	switch {
	case len(v.Aliases) == 0:
		v.AliasPill = pillView{"off", "none", "no model aliases"}
	case stale > 0:
		v.AliasPill = pillView{"warn", fmt.Sprintf("%d of %d stale", stale, len(v.Aliases)), "an alias points at a model the provider no longer lists"}
	default:
		v.AliasPill = pillView{"ok", fmt.Sprint(len(v.Aliases)), "aliases resolve to listed models"}
	}
	return v
}

func (a *Admin) status(w http.ResponseWriter, r *http.Request) {
	var d statusData
	csrf, _ := a.Session(r)
	for _, p := range a.Providers.List() {
		v := a.providerView(r, p)
		v.CSRF = csrf
		d.Providers = append(d.Providers, v)
	}
	a.render(w, r, "status", page{Title: "Status", Nav: "status", Data: d})
}

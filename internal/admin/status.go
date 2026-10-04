package admin

import (
	"fmt"
	"net/http"
	"regexp"
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
	Choices     []modelChoice // Models, then the aliases defined in skgate (Model aliases), each with the model it selects
	Groups      []choiceGroup // the same choices as the picker shows them: Your aliases, Models, Vendor aliases
	ModelHeavy  bool          // the chosen helper model (or the model its alias selects) looks like a heavy reasoning model
	ModelsKnown bool
	ModelsAt    string
	Model       string
	ModelPill   pillView   // MCP helper model summary
	Effort      effortView // reasoning of the MCP helper model's calls
	EffortPill  pillView   // the same, as shown next to the helper model
	Timeout     int        // seconds the MCP helper model may stay silent
	Frontier    int        // the timeout suggested for heavy models, shown for those only
	AliasTarget string     // the model a new alias points at by default: the one the helper model uses
	Aliases     []aliasView
	AliasPill   pillView
}

// effortView is the reasoning dropdown of the helper model.
type effortView struct {
	Name, Value string
	Options     []effortOption
}

type effortOption struct{ Value, Label string }

func effortOf(name, value string) effortView {
	v := effortView{Name: name, Value: value}
	for _, e := range provider.Efforts {
		l := "Reasoning: " + e
		if e == "auto" {
			l = "Reasoning: auto (model decides)"
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
	case s.State == "secret_error":
		return pillView{"bad", "cannot decrypt", tipJoin("", s.LastError)}
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
	v.Choices = modelChoices(v.Models, a.Set.Aliases(id), v.Model)
	v.Groups = groupChoices(v.Choices)
	v.AliasTarget = v.Model
	for _, al := range a.Set.Aliases(id) {
		if al.Name == v.Model {
			v.AliasTarget = al.Target
		}
	}
	for _, c := range v.Choices {
		v.ModelHeavy = v.ModelHeavy || c.Selected && c.Frontier
	}
	v.Effort = effortOf("effort", a.Set.Effort(id))
	effortTip := "Reasoning of the helper model"
	if v.Effort.Value == "auto" {
		effortTip = "Reasoning of the helper model: the model decides"
	}
	v.EffortPill = pillView{Class: "off", Text: "auto", Tip: effortTip}
	if v.Effort.Value != "auto" {
		v.EffortPill.Class, v.EffortPill.Text = "ok", v.Effort.Value
	}
	v.Timeout, v.Frontier = int(a.Set.HelperTimeout(id)/time.Second), provider.FrontierTimeoutSecs
	switch {
	case v.Model == "":
		v.ModelPill = pillView{"off", "no model", "pick a model in the details to enable Suggest configuration"}
	case v.ModelsKnown && !contains(v.Models, v.Model) && !isAlias(a.Set.Aliases(id), v.Model):
		v.ModelPill = pillView{"warn", modelWithReasoning(v.Model, v.Effort.Value), "no longer in the provider's model list"}
	default:
		v.ModelPill = pillView{"ok", modelWithReasoning(v.Model, v.Effort.Value), "used as the MCP helper model"}
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
		v.AliasPill = pillView{"off", "no aliases", "no model aliases"}
	case stale > 0:
		v.AliasPill = pillView{"warn", fmt.Sprintf("%d of %d %s stale", stale, len(v.Aliases), plural(len(v.Aliases), "alias", "aliases")), "an alias points at a model the provider no longer lists"}
	default:
		v.AliasPill = pillView{"ok", fmt.Sprintf("%d %s", len(v.Aliases), plural(len(v.Aliases), "alias", "aliases")), "aliases resolve to listed models"}
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

// modelChoice is one option of the helper model picker: a model id, or an alias the provider offers for one.
type modelChoice struct {
	Value, Label string
	Frontier     bool
	Selected     bool
	Own          bool // an alias defined in skgate
}

// choiceGroup is one <optgroup> of the helper model picker.
type choiceGroup struct {
	Label   string
	Choices []modelChoice
}

// vendorAlias matches the ids a provider lists as moving names for another model ("grok-4-latest").
var vendorAlias = regexp.MustCompile(`-latest(-|$)`)

// groupChoices sorts the picker's choices into Your aliases (defined in skgate), Models, and Vendor aliases (the
// provider's own "-latest" names), in that order, leaving out an empty group. The order inside a group is kept.
func groupChoices(list []modelChoice) []choiceGroup {
	own := choiceGroup{Label: "Your aliases"}
	models := choiceGroup{Label: "Models"}
	vendor := choiceGroup{Label: "Vendor aliases"}
	for _, c := range list {
		switch {
		case c.Own:
			own.Choices = append(own.Choices, c)
		case vendorAlias.MatchString(c.Value):
			vendor.Choices = append(vendor.Choices, c)
		default:
			models.Choices = append(models.Choices, c)
		}
	}
	var out []choiceGroup
	for _, g := range []choiceGroup{own, models, vendor} {
		if len(g.Choices) > 0 {
			out = append(out, g)
		}
	}
	return out
}

// modelChoices lists the provider's models followed by the aliases defined in skgate (the Model aliases of the same
// screen), each as "alias (alias of model)". Choosing an alias stores it as it is; skgate resolves it to the model
// when the helper calls the provider, as it does for /v1 requests. A chosen value that is neither stays selectable as
// "(unlisted)".
func modelChoices(ids []string, aliases []provider.Alias, chosen string) []modelChoice {
	var out []modelChoice
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, modelChoice{Value: id, Label: id, Frontier: provider.LooksFrontier(id), Selected: id == chosen})
	}
	for _, al := range aliases {
		if seen[al.Name] {
			continue
		}
		seen[al.Name] = true
		out = append(out, modelChoice{Value: al.Name, Label: al.Name + " (alias of " + al.Target + ")",
			Frontier: provider.LooksFrontier(al.Name) || provider.LooksFrontier(al.Target), Selected: al.Name == chosen, Own: true})
	}
	if chosen != "" && !seen[chosen] {
		out = append(out, modelChoice{Value: chosen, Label: chosen + " (unlisted)", Frontier: provider.LooksFrontier(chosen), Selected: true})
	}
	return out
}

// isAlias reports whether name is one of the skgate-defined aliases.
func isAlias(aliases []provider.Alias, name string) bool {
	for _, al := range aliases {
		if al.Name == name {
			return true
		}
	}
	return false
}

// modelWithReasoning is the text of the helper model pill: the model and its reasoning, as one phrase
// ("grok-4.7 · reasoning low", "grok-4.7 · reasoning auto").
func modelWithReasoning(model, effort string) string {
	return model + " \u00b7 reasoning " + effort
}

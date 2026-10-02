package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/suggest"
)

// suggestState reports whether "Suggest configuration" can run, and what the page offers instead of
// a dead button: the provider's MCP helper model picker. The manual form never depends on it.
func (a *Admin) suggestState(r *http.Request) suggestState {
	p := a.Providers.Default()
	if p == nil || a.Proxy == nil {
		return suggestState{Why: "no provider"}
	}
	v := a.providerView(r, p)
	v.CSRF, _ = a.Session(r)
	v.Inline = true
	st := suggestState{P: &v}
	switch {
	case !v.S.SignedIn:
		st.Why = "sign in on the status page"
	case v.Model == "":
		st.Why = "pick an MCP helper model first"
	default:
		st.Enabled = true
	}
	return st
}

// controlsHTML renders the Suggest controls (button, MCP helper model picker and its dialog) for the
// current state: the fragment the client script swaps in after a model was picked.
func (a *Admin) controlsHTML(r *http.Request) (string, error) {
	var b strings.Builder
	err := a.tpl["upstreams"].ExecuteTemplate(&b, "suggest_controls", a.suggestState(r))
	return b.String(), err
}

// upstreamSuggest answers with a validated suggestion as JSON for the client script to put into the
// form. It saves nothing. POST only (guard checks the CSRF token).
func (a *Admin) upstreamSuggest(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, msg string) { httputil.JSON(w, status, map[string]any{"error": msg}) }
	if ok, why := a.MCP.ManagedState(); !ok {
		fail(http.StatusConflict, "managed upstreams: "+why)
		return
	}
	st := a.suggestState(r)
	if !st.Enabled {
		fail(http.StatusConflict, st.Why)
		return
	}
	src, err := suggest.ParseSource(r.PostFormValue("source"))
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	var runners []string
	for _, c := range a.MCP.Commands() {
		runners = append(runners, c.Name)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	svc := suggest.Service{Fetch: suggest.NewFetcher(), LLM: a.Proxy}
	if a.SuggestFetch != nil {
		svc.Fetch = a.SuggestFetch
	}
	token := r.PostFormValue("git_token")
	if token == "" && r.PostFormValue("alias_existing") != "" { // editing: the stored token applies
		if u, ok := a.MCP.Upstreams.Get(r.PostFormValue("alias_existing")); ok {
			token = u.GitToken
		}
	}
	res, err := svc.Suggest(ctx, a.Set.Model(a.Providers.Default().ID()), src, token, runners)
	if err != nil {
		fail(http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(res)
}

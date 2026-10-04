package admin

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/provider"
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
	case !v.HelperReady:
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

// suggestCap is the least overall limit of one suggestion (twice the helper timeout when that is longer); the
// model call also stops after a silent spell, the helper timeout.
const suggestCap = 5 * time.Minute

// ndjson is the streamed form of the answer: stage events, then a result or an error line.
const ndjson = "application/x-ndjson"

// upstreamSuggest answers with a validated suggestion as JSON for the client script to put into the
// form. It saves nothing. POST only (guard checks the CSRF token).
func (a *Admin) upstreamSuggest(w http.ResponseWriter, r *http.Request) {
	t0 := time.Now()
	fail := func(status int, msg string) {
		log.Printf("suggest: request refused HTTP %d after %s: %s", status, time.Since(t0).Round(time.Millisecond), msg)
		httputil.JSON(w, status, map[string]any{"error": msg})
	}
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
	if err != nil { // the input itself is not logged: it may carry credentials
		fail(http.StatusBadRequest, err.Error())
		return
	}
	var runners []string
	for _, c := range a.MCP.Commands() {
		runners = append(runners, c.Name)
	}
	// The user's timeout is how long the model may stay silent; the overall limit leaves room for a slow answer.
	idle := a.Set.HelperTimeout(a.Providers.Default().ID())
	if a.SuggestIdle > 0 {
		idle = a.SuggestIdle
	}
	limit := max(suggestCap, 2*idle)
	if a.SuggestCap > 0 {
		limit = a.SuggestCap
	}
	ctx, cancel := context.WithTimeout(r.Context(), limit)
	defer cancel()
	fetcher := suggest.NewFetcher()
	fetcher.GitHubToken = a.Cfg.GitHubToken
	svc := suggest.Service{Fetch: fetcher, LLM: a.Proxy, Idle: idle,
		Effort: provider.EffortParam(a.Set.Effort(a.Providers.Default().ID()))}
	if a.SuggestFetch != nil {
		svc.Fetch = a.SuggestFetch
	}
	token := r.PostFormValue("git_token")
	if token == "" && r.PostFormValue("alias_existing") != "" { // editing: the stored token applies
		if u, ok := a.MCP.Upstreams.Get(r.PostFormValue("alias_existing")); ok {
			token = u.GitToken
		}
	}
	svc.Logf = log.Printf
	stream := strings.Contains(r.Header.Get("Accept"), ndjson)
	var send func(v any)
	if stream { // one JSON object per line, flushed as each stage starts
		send = func(v any) {
			_ = json.NewEncoder(w).Encode(v)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		w.Header().Set("Content-Type", ndjson)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		svc.Progress = func(e suggest.Event) { send(e) }
	}
	log.Printf("suggest: request source=%s token=%t", src.Label(), token != "")
	res, err := svc.Suggest(ctx, a.Set.Model(a.Providers.Default().ID()), src, token, runners)
	var te *suggest.TimeoutError
	timeout := func() map[string]any {
		return map[string]any{"kind": te.Kind, "stage": te.Stage, "where": te.Where(), "secs": int(te.After.Round(time.Second) / time.Second), "effort": cmp.Or(svc.Effort, "auto")}
	}
	if stream {
		if errors.As(err, &te) {
			send(map[string]any{"error": err.Error(), "timeout": timeout()})
		} else if err != nil {
			send(map[string]string{"error": err.Error()})
		} else {
			send(map[string]any{"result": res})
		}
		return
	}
	if errors.As(err, &te) {
		log.Printf("suggest: request refused HTTP %d after %s: %s", http.StatusGatewayTimeout, time.Since(t0).Round(time.Millisecond), err)
		httputil.JSON(w, http.StatusGatewayTimeout, map[string]any{"error": err.Error(), "timeout": timeout()})
		return
	}
	if err != nil {
		fail(http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(res)
}

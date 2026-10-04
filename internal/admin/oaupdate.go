package admin

import (
	"crypto/subtle"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/helv-io/skgate/internal/mcp"
	"github.com/helv-io/skgate/internal/openapi"
	"github.com/helv-io/skgate/internal/timefmt"
)

// OpenAPI upstreams are updated by hand, never on a schedule. The Update button on the tools page reads the
// description again from its address, runs it through the SI layer (the same checks and repair as "Check
// description"), and shows what would change. Nothing is replaced until the person confirms; cancel leaves
// everything as it was. The review screen is a stashed result (see results.go), so a refresh repeats nothing.

// maxListed bounds how many operations a review screen names per kind.
const maxListed = 40

type oaUpdateData struct {
	Alias               string
	Token               string // the result's own token, set when the screen is drawn
	Cfg                 mcp.OpenAPIConfig
	Added, Removed, Chg []string
	MoreAdded           int
	MoreRemoved         int
	MoreChanged         int
	ToolsBefore         int
	ToolsAfter          int
	Layer               string // what the SI layer did, in one line
	Repairs             int
}

// pruneSelection keeps the tool selection (which operations are on, their names and texts) for the operations
// that still exist. New operations stay off.
func pruneSelection(sel openapi.Selection, ops []openapi.Op) openapi.Selection {
	have := map[string]bool{}
	for _, o := range ops {
		if o.Skip == "" {
			have[o.Key] = true
		}
	}
	out := openapi.Selection{Enabled: map[string]bool{}, Overrides: map[string]openapi.Override{}}
	for k, v := range sel.Enabled {
		if have[k] {
			out.Enabled[k] = v
		}
	}
	for k, v := range sel.Overrides {
		if have[k] {
			out.Overrides[k] = v
		}
	}
	return out
}

func capList(l []string) ([]string, int) {
	if len(l) <= maxListed {
		return l, 0
	}
	return l[:maxListed], len(l) - maxListed
}

// oaSpecUpdate reads the description again and shows the review screen. It stores nothing.
func (a *Admin) oaSpecUpdate(w http.ResponseWriter, r *http.Request) {
	alias := r.PathValue("alias")
	back := "/admin/upstreams/" + alias + "/tools"
	u, ok := a.MCP.Upstreams.Get(alias)
	if !ok || !u.IsOpenAPI() {
		a.back(w, r, "/admin/upstreams", "", "that upstream is not an OpenAPI upstream")
		return
	}
	st, err := a.MCP.Upstreams.OpenAPI(alias)
	if err != nil {
		a.back(w, r, back, "", err.Error())
		return
	}
	if st.Config.SpecURL == "" {
		a.back(w, r, back, "", oaNoUpdateReason)
		return
	}
	raw, err := openapi.Fetch(r.Context(), st.Config.SpecURL)
	if err != nil {
		a.back(w, r, back, "", err.Error())
		return
	}
	doc, err := openapi.Parse(raw)
	if err != nil {
		a.back(w, r, back, "", err.Error())
		return
	}
	doc.Source = st.Config.SpecURL
	hash := openapi.Hash(raw)
	if st.Config.SpecHash == hash || (st.Config.SpecHash == "" && string(doc.JSON()) == st.Config.Spec) {
		a.back(w, r, back, "No changes", "")
		return
	}
	d := oaUpdateData{Alias: alias}
	issues := doc.Validate()
	for _, is := range issues {
		if is.Fatal {
			a.back(w, r, back, "", "the new description is invalid: "+is.Message)
			return
		}
	}
	switch {
	case len(issues) == 0:
		d.Layer = "No problems"
	default:
		as, ctx, cancel, why := a.assistFor(r)
		if why != "" {
			d.Layer = fmt.Sprintf("%d %s, not repaired: %s", len(issues), plural(len(issues), "problem", "problems"), why)
			break
		}
		patches, err := as.Repair(ctx, doc, issues)
		cancel()
		if err != nil {
			log.Printf("openapi: update repair refused: %v", err)
			d.Layer = fmt.Sprintf("%d %s, repair failed", len(issues), plural(len(issues), "problem", "problems"))
			break
		}
		fixed, changes, err := doc.Apply(patches)
		if err != nil {
			log.Printf("openapi: update repair patches refused: %v", err)
			d.Layer = fmt.Sprintf("%d %s, repair did not apply", len(issues), plural(len(issues), "problem", "problems"))
			break
		}
		doc, d.Repairs = fixed, len(changes)
		rest := len(doc.Validate())
		d.Layer = fmt.Sprintf("Fixed %d, %d left", len(changes), rest)
	}
	ops := doc.Operations()
	sel := pruneSelection(st.Config.Selection, ops)
	d.Cfg = mcp.OpenAPIConfig{Spec: string(doc.JSON()), SpecURL: st.Config.SpecURL, Selection: sel, FetchedAt: time.Now().Unix(), SpecHash: hash}
	df := openapi.DiffOps(st.Ops, ops)
	d.Added, d.MoreAdded = capList(df.Added)
	d.Removed, d.MoreRemoved = capList(df.Removed)
	d.Chg, d.MoreChanged = capList(df.Changed)
	d.ToolsBefore, d.ToolsAfter = len(st.Tools), len(openapi.Tools(ops, sel))
	http.Redirect(w, r, a.stash(r, "oaupdate", d, toast{}), http.StatusSeeOther)
}

// takeResult returns a stashed result of this session and removes it, so a confirmation applies once.
func (a *Admin) takeResult(r *http.Request, token string) (any, bool) {
	csrf, _ := a.Session(r)
	a.results.mu.Lock()
	defer a.results.mu.Unlock()
	res := a.results.m[token]
	if res == nil || time.Now().After(res.expires) || subtle.ConstantTimeCompare([]byte(res.session), []byte(csrf)) != 1 {
		return nil, false
	}
	delete(a.results.m, token)
	return res.data, true
}

// oaSpecApply replaces the stored description with the reviewed one. The tool selection is taken again from what
// is stored now, so tool edits made while the screen was open are kept.
func (a *Admin) oaSpecApply(w http.ResponseWriter, r *http.Request) {
	alias := r.PathValue("alias")
	back := "/admin/upstreams/" + alias + "/tools"
	v, ok := a.takeResult(r, r.PostFormValue("token"))
	d, isUpd := v.(oaUpdateData)
	if !ok || !isUpd || d.Alias != alias {
		a.back(w, r, back, "", "the update expired, press Update again")
		return
	}
	st, err := a.MCP.Upstreams.OpenAPI(alias)
	if err != nil {
		a.back(w, r, back, "", err.Error())
		return
	}
	doc, err := openapi.ParseStored([]byte(d.Cfg.Spec))
	if err != nil {
		a.back(w, r, back, "", err.Error())
		return
	}
	cfg := d.Cfg
	cfg.SpecURL = st.Config.SpecURL
	cfg.Selection = pruneSelection(st.Config.Selection, doc.Operations())
	if err := a.MCP.Upstreams.SetOpenAPI(alias, cfg); err != nil {
		a.back(w, r, back, "", err.Error())
		return
	}
	a.MCP.ForgetHealth(alias)
	n := a.MCP.Upstreams.OpenAPIToolCount(alias)
	a.back(w, r, back, fmt.Sprintf("%s updated, %d %s exposed", alias, n, plural(n, "tool", "tools")), "")
}

// oaSpecCancel drops a reviewed update. Nothing was stored.
func (a *Admin) oaSpecCancel(w http.ResponseWriter, r *http.Request) {
	alias := r.PathValue("alias")
	a.takeResult(r, r.PostFormValue("token"))
	a.back(w, r, "/admin/upstreams/"+alias+"/tools", "Update cancelled", "")
}

// oaNoUpdateReason is why a pasted description has no Update.
const oaNoUpdateReason = "no address to update from"

// oaUpdatedLine is the status of the last read from the address: "Updated <time>" or nothing yet.
func oaUpdatedLine(c mcp.OpenAPIConfig) string {
	if c.FetchedAt == 0 {
		return ""
	}
	return "Updated " + timefmt.DateTime(time.Unix(c.FetchedAt, 0))
}

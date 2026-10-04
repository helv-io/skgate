package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/mcp"
	"github.com/helv-io/skgate/internal/openapi"
	"github.com/helv-io/skgate/internal/provider"
)

// OpenAPI upstreams: a REST API described by an OpenAPI document, served as MCP tools by skgate itself. This file
// is the admin side: reading a description (pasted or fetched), checking and repairing it with the assistant, the
// add/edit fields, and the tools page where the admin picks which operations become tools.

// verbOrder is the order of the groups on the tools page: reads first, then the verbs that change things.
var verbOrder = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE"}

// writeVerb reports whether a verb changes data. Those start switched off.
func writeVerb(v string) bool { return v != "GET" && v != "HEAD" && v != "OPTIONS" && v != "TRACE" }

// oaBodyLimit is how large a form may be on the pages that carry a pasted description (a URL avoids it).
const oaBodyLimit = 24 << 20

// oaForm is what the add/edit form shows for an OpenAPI upstream.
type oaForm struct {
	SpecURL string
	HasSpec bool
	Title   string
	Ops     int
	Tools   int
	Level   string
	Servers []string
	AI      bool // the assistant can run
	AIWhy   string
}

func (a *Admin) oaFormFor(r *http.Request, u mcp.Upstream) *oaForm {
	f := &oaForm{}
	st := a.suggestState(r)
	f.AI, f.AIWhy = st.Enabled, st.Why
	if u.Alias != "" && u.IsOpenAPI() {
		if s, err := a.MCP.Upstreams.OpenAPI(u.Alias); err == nil {
			f.HasSpec, f.SpecURL, f.Title, f.Ops, f.Tools = true, s.Config.SpecURL, s.Doc.Title(), len(s.Ops), len(s.Tools)
			f.Level = mcp.ToolLevel(f.Tools)
			for _, sv := range s.Doc.Servers(s.Config.SpecURL) {
				f.Servers = append(f.Servers, sv.URL)
			}
		}
	}
	return f
}

// readSpec returns the description a form carries: the pasted text when there is any, else the document at the URL.
func readSpec(r *http.Request) (*openapi.Doc, string, error) {
	text, specURL := strings.TrimSpace(r.PostFormValue("oa_spec_text")), strings.TrimSpace(r.PostFormValue("oa_spec_url"))
	var raw []byte
	switch {
	case text != "":
		raw = []byte(text)
	case specURL != "":
		b, err := openapi.Fetch(r.Context(), specURL)
		if err != nil {
			return nil, specURL, err
		}
		raw = b
	default:
		return nil, specURL, errors.New("give the address of the OpenAPI description or paste it")
	}
	d, err := openapi.Parse(raw)
	if err != nil {
		return nil, specURL, err
	}
	d.Source = specURL
	return d, specURL, nil
}

// defaultSelection enables the reading operations (GET and HEAD) of a new upstream, unless there are more of them
// than a model handles well: then nothing is on and the admin picks. Operations that change data are never on.
func defaultSelection(ops []openapi.Op) openapi.Selection {
	sel := openapi.Selection{Enabled: map[string]bool{}, Overrides: map[string]openapi.Override{}}
	var reads []string
	for _, o := range ops {
		if o.Skip == "" && !writeVerb(o.Method) {
			reads = append(reads, o.Key)
		}
	}
	if len(reads) <= mcp.ToolsWarn {
		for _, k := range reads {
			sel.Enabled[k] = true
		}
	}
	return sel
}

// saveOpenAPI adds or updates an OpenAPI upstream from the form. The description is read before anything is stored.
func (a *Admin) saveOpenAPI(w http.ResponseWriter, r *http.Request, u mcp.Upstream, edit bool, form string) {
	openAPIInput(r, &u)
	var cfg mcp.OpenAPIConfig
	var doc *openapi.Doc
	var old mcp.Upstream
	if edit {
		var ok bool
		if old, ok = a.MCP.Upstreams.Get(u.Alias); !ok {
			a.back(w, r, form, "", "unknown alias")
			return
		}
		if old.KindOrRemote() != u.Kind {
			a.back(w, r, form, "", "the type cannot be changed; create a new upstream")
			return
		}
		if st, err := a.MCP.Upstreams.OpenAPI(u.Alias); err == nil {
			cfg = st.Config
		}
	}
	text, specURL := strings.TrimSpace(r.PostFormValue("oa_spec_text")), strings.TrimSpace(r.PostFormValue("oa_spec_url"))
	// A new upstream always reads its description. An edit keeps the stored one unless new text was pasted, the
	// address changed, or the address is to be fetched again.
	fresh := !edit || text != "" || (specURL != "" && (specURL != cfg.SpecURL || r.PostFormValue("oa_refetch") == "1"))
	switch {
	case fresh:
		d, specURL, err := readSpec(r)
		if err != nil {
			a.back(w, r, form, "", err.Error())
			return
		}
		doc = d
		cfg.Spec, cfg.SpecURL = string(d.JSON()), specURL
		if !edit {
			cfg.Selection = defaultSelection(d.Operations())
		}
	default:
		if cfg.Spec == "" {
			a.back(w, r, form, "", "give the address of the OpenAPI description or paste it")
			return
		}
		d, err := openapi.ParseStored([]byte(cfg.Spec))
		if err != nil {
			a.back(w, r, form, "", err.Error())
			return
		}
		doc = d
	}
	if strings.TrimSpace(u.URL) == "" {
		sv := doc.Servers(cfg.SpecURL)
		if len(sv) == 0 {
			a.back(w, r, form, "", "the description names no server: give the base URL of the API")
			return
		}
		u.URL = sv[0].URL
	}
	var err error
	if edit {
		err = a.MCP.Upstreams.Update(u, true)
	} else {
		err = a.MCP.Upstreams.Create(u)
	}
	if err != nil {
		a.back(w, r, form, "", err.Error())
		return
	}
	if err := a.MCP.Upstreams.SetOpenAPI(u.Alias, cfg); err != nil {
		if !edit {
			_ = a.MCP.Upstreams.Delete(u.Alias)
		}
		a.back(w, r, form, "", err.Error())
		return
	}
	a.MCP.ForgetHealth(u.Alias)
	_ = a.DB.SetSetting(settingLastInclude, map[bool]string{true: "1", false: "0"}[u.IncludeInMCP])
	n := a.MCP.Upstreams.OpenAPIToolCount(u.Alias)
	if !edit || fresh {
		msg := fmt.Sprintf("%s saved: %d %s exposed. Choose the tools it offers", u.Alias, n, plural(n, "tool", "tools"))
		if issues := len(doc.Validate()); issues > 0 {
			msg += fmt.Sprintf("; the description has %d %s (see Edit for a check and a repair)", issues, plural(issues, "problem", "problems"))
		}
		a.back(w, r, "/admin/upstreams/"+u.Alias+"/tools", msg, "")
		return
	}
	a.back(w, r, "/admin/upstreams", u.Alias+" saved", "")
}

// openAPIInput reads the OpenAPI fields of the form into u (the base URL, the credential).
func openAPIInput(r *http.Request, u *mcp.Upstream) {
	u.Kind = mcp.KindOpenAPI
	u.URL = strings.TrimSpace(r.PostFormValue("oa_url"))
	u.AuthKind = r.PostFormValue("oa_auth_kind")
	u.AuthName = strings.TrimSpace(r.PostFormValue("oa_auth_name"))
	u.AuthValue = r.PostFormValue("oa_auth_value")
}

// --- the tools page ---

type toolRow struct {
	Key, Verb, Path     string
	Name, DefName       string
	Desc, DefDesc       string
	Enabled, Deprecated bool
	Skip                string
	Tags                string
}

type verbGroup struct {
	Verb    string
	Write   bool
	Rows    []toolRow
	Enabled int
}

type toolsData struct {
	U      upstreamView
	Groups []verbGroup
	Total  int // operations that can be tools
	On     int
	Skip   int
	Level  string
	Good   int
	Warn   int
	AI     bool
	AIWhy  string
	Title  string
}

func (a *Admin) upstreamTools(w http.ResponseWriter, r *http.Request) {
	alias := r.PathValue("alias")
	u, ok := a.MCP.Upstreams.Get(alias)
	if !ok || !u.IsOpenAPI() {
		a.back(w, r, "/admin/upstreams", "", "that upstream is not an OpenAPI upstream")
		return
	}
	st, err := a.MCP.Upstreams.OpenAPI(alias)
	if err != nil {
		a.back(w, r, "/admin/upstreams", "", err.Error())
		return
	}
	d := toolsData{U: a.view(u), Good: mcp.ToolsGood, Warn: mcp.ToolsWarn, Title: st.Doc.Title()}
	sug := a.suggestState(r)
	d.AI, d.AIWhy = sug.Enabled, sug.Why
	byVerb := map[string]*verbGroup{}
	for _, o := range st.Ops {
		g := byVerb[o.Method]
		if g == nil {
			g = &verbGroup{Verb: o.Method, Write: writeVerb(o.Method)}
			byVerb[o.Method] = g
		}
		ov := st.Config.Selection.Overrides[o.Key]
		row := toolRow{Key: o.Key, Verb: o.Method, Path: o.Path, DefName: openapi.DefaultName(o), DefDesc: openapi.DefaultDescription(o),
			Deprecated: o.Deprecated, Skip: o.Skip, Tags: strings.Join(o.Tags, ", ")}
		row.Name, row.Desc = row.DefName, row.DefDesc
		if ov.Name != "" {
			row.Name = ov.Name
		}
		if ov.Description != "" {
			row.Desc = ov.Description
		}
		if o.Skip != "" {
			d.Skip++
		} else {
			d.Total++
			row.Enabled = st.Config.Selection.Enabled[o.Key]
			if row.Enabled {
				g.Enabled++
				d.On++
			}
		}
		g.Rows = append(g.Rows, row)
	}
	known := map[string]bool{}
	for _, v := range verbOrder {
		known[v] = true
		if g := byVerb[v]; g != nil {
			d.Groups = append(d.Groups, *g)
		}
	}
	var rest []string
	for v := range byVerb {
		if !known[v] {
			rest = append(rest, v)
		}
	}
	sort.Strings(rest)
	for _, v := range rest {
		d.Groups = append(d.Groups, *byVerb[v])
	}
	d.Level = mcp.ToolLevel(d.On)
	a.render(w, r, "upstream_tools", page{Title: "Tools of " + alias, Nav: "upstreams", Data: d})
}

func (a *Admin) upstreamToolsSave(w http.ResponseWriter, r *http.Request) {
	alias := r.PathValue("alias")
	back := "/admin/upstreams/" + alias + "/tools"
	st, err := a.MCP.Upstreams.OpenAPI(alias)
	if u, ok := a.MCP.Upstreams.Get(alias); !ok || !u.IsOpenAPI() || err != nil {
		a.back(w, r, "/admin/upstreams", "", "that upstream is not an OpenAPI upstream")
		return
	}
	byKey := map[string]openapi.Op{}
	for _, o := range st.Ops {
		byKey[o.Key] = o
	}
	sel := openapi.Selection{Enabled: map[string]bool{}, Overrides: map[string]openapi.Override{}}
	for _, k := range r.PostForm["on"] {
		if o, ok := byKey[k]; ok && o.Skip == "" {
			sel.Enabled[k] = true
		}
	}
	keys, names, descs := r.PostForm["key"], r.PostForm["name"], r.PostForm["desc"]
	if len(names) != len(keys) || len(descs) != len(keys) {
		a.back(w, r, back, "", "the form is incomplete; reload the page")
		return
	}
	for i, k := range keys {
		o, ok := byKey[k]
		if !ok {
			continue
		}
		name, desc := strings.TrimSpace(names[i]), strings.Join(strings.Fields(descs[i]), " ")
		ov := openapi.Override{}
		if name != "" && name != openapi.DefaultName(o) {
			if !openapi.ValidName(name) {
				a.back(w, r, back, "", fmt.Sprintf("the name %q is not valid: use letters, digits, underscore and dash, at most %d characters", clip(name), openapi.MaxNameLen))
				return
			}
			ov.Name = name
		}
		if desc != "" && desc != openapi.DefaultDescription(o) {
			ov.Description = clipRunes(desc, openapi.MaxDescription)
		}
		if ov != (openapi.Override{}) {
			sel.Overrides[k] = ov
		}
	}
	cfg := st.Config
	cfg.Selection = sel
	if err := a.MCP.Upstreams.SetOpenAPI(alias, cfg); err != nil {
		a.back(w, r, back, "", err.Error())
		return
	}
	n := a.MCP.Upstreams.OpenAPIToolCount(alias)
	msg := fmt.Sprintf("%s: %d %s exposed", alias, n, plural(n, "tool", "tools"))
	switch mcp.ToolLevel(n) {
	case "warn":
		msg += ". That is a lot: models pick worse tools with many to choose from"
	case "bad":
		msg += ". That is far too many: models pick worse tools, and run slower and costlier. Expose only what you need"
	}
	a.back(w, r, back, msg, "")
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// --- the assistant ---

// assistFor returns the assistant when the helper model can run, else the reason it cannot.
func (a *Admin) assistFor(r *http.Request) (openapi.Assist, context.Context, context.CancelFunc, string) {
	st := a.suggestState(r)
	if !st.Enabled {
		return openapi.Assist{}, nil, nil, st.Why
	}
	p := a.Providers.Default()
	idle := a.Set.HelperTimeout(p.ID())
	ctx, cancel := context.WithTimeout(r.Context(), max(suggestCap/2, 2*idle))
	return openapi.Assist{LLM: a.Proxy, Model: a.Set.Model(p.ID()), Effort: provider.EffortParam(a.Set.Effort(p.ID()))}, ctx, cancel, ""
}

type issueView struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

func issueViews(is []openapi.Issue) []issueView {
	out := make([]issueView, 0, len(is))
	for _, i := range is {
		out = append(out, issueView{i.Code, i.Path, i.Message})
	}
	return out
}

// openAPICheck reads a description and reports what it holds and what is wrong with it. It saves nothing.
func (a *Admin) openAPICheck(w http.ResponseWriter, r *http.Request) {
	d, specURL, err := readSpec(r)
	if err != nil {
		httputil.JSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ops := d.Operations()
	var servers []string
	for _, s := range d.Servers(specURL) {
		servers = append(servers, s.URL)
	}
	sug := a.suggestState(r)
	reads, writes := 0, 0
	for _, o := range ops {
		if o.Skip != "" {
			continue
		}
		if writeVerb(o.Method) {
			writes++
		} else {
			reads++
		}
	}
	httputil.JSON(w, http.StatusOK, map[string]any{"ok": true, "title": d.Title(), "operations": len(ops), "reads": reads, "writes": writes,
		"servers": servers, "issues": issueViews(d.Validate()), "ai": sug.Enabled, "aiWhy": sug.Why})
}

// openAPIRepair asks the assistant for fixes to the problems the reader found, applies them to a copy and returns
// the changes for approval together with the repaired description. Nothing is saved.
func (a *Admin) openAPIRepair(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, msg string) { httputil.JSON(w, status, map[string]any{"error": msg}) }
	d, _, err := readSpec(r)
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	issues := d.Validate()
	for _, is := range issues {
		if is.Fatal {
			fail(http.StatusBadRequest, is.Message)
			return
		}
	}
	if len(issues) == 0 {
		fail(http.StatusBadRequest, "the description has no problems to repair")
		return
	}
	as, ctx, cancel, why := a.assistFor(r)
	if why != "" {
		fail(http.StatusConflict, why)
		return
	}
	defer cancel()
	t0 := time.Now()
	patches, err := as.Repair(ctx, d, issues)
	if err != nil {
		log.Printf("openapi: repair refused after %s: %v", time.Since(t0).Round(time.Millisecond), err)
		fail(http.StatusBadGateway, err.Error())
		return
	}
	fixed, changes, err := d.Apply(patches)
	if err != nil {
		log.Printf("openapi: repair patches refused: %v", err)
		fail(http.StatusBadGateway, "the proposed repair does not apply: "+err.Error())
		return
	}
	rest := fixed.Validate()
	log.Printf("openapi: repair proposed %d changes, %d problems before, %d after, in %s", len(changes), len(issues), len(rest), time.Since(t0).Round(time.Millisecond))
	pretty, _ := json.MarshalIndent(fixed.Raw, "", "  ")
	httputil.JSON(w, http.StatusOK, map[string]any{"changes": changes, "before": len(issues), "remaining": issueViews(rest), "spec": string(pretty)})
}

// upstreamToolsSuggest proposes names and one-line descriptions for the ticked tools. It saves nothing.
func (a *Admin) upstreamToolsSuggest(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, msg string) { httputil.JSON(w, status, map[string]any{"error": msg}) }
	st, err := a.MCP.Upstreams.OpenAPI(r.PathValue("alias"))
	if err != nil {
		fail(http.StatusNotFound, err.Error())
		return
	}
	want := map[string]bool{}
	for _, k := range r.PostForm["key"] {
		want[k] = true
	}
	var ops []openapi.Op
	for _, o := range st.Ops {
		if want[o.Key] && o.Skip == "" {
			ops = append(ops, o)
		}
	}
	if len(ops) == 0 {
		fail(http.StatusBadRequest, "tick the tools to describe first")
		return
	}
	if len(ops) > openapi.MaxDescribeOps {
		fail(http.StatusBadRequest, fmt.Sprintf("describe at most %d tools at a time", openapi.MaxDescribeOps))
		return
	}
	as, ctx, cancel, why := a.assistFor(r)
	if why != "" {
		fail(http.StatusConflict, why)
		return
	}
	defer cancel()
	res, err := as.Describe(ctx, ops)
	if err != nil {
		log.Printf("openapi: describe refused: %v", err)
		fail(http.StatusBadGateway, err.Error())
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]any{"tools": res})
}

// oaViewOf summarizes an OpenAPI upstream for the list. A description that cannot be read shows no tools.
func (a *Admin) oaViewOf(u mcp.Upstream) *oaView {
	v := &oaView{}
	if st, err := a.MCP.Upstreams.OpenAPI(u.Alias); err == nil {
		v.Title, v.SpecURL, v.Ops, v.Tools = st.Doc.Title(), st.Config.SpecURL, len(st.Ops), len(st.Tools)
	}
	v.Level = mcp.ToolLevel(v.Tools)
	tip := toolTip(v.Tools)
	switch {
	case v.Tools == 0:
		v.Pill = pillView{"off", "no tools", "no operation is enabled: choose the tools this upstream offers"}
	default:
		v.Pill = pillView{v.Level, fmt.Sprintf("%d %s", v.Tools, plural(v.Tools, "tool", "tools")), tip}
	}
	return v
}

// toolTip says how a model copes with that many tools.
func toolTip(n int) string {
	switch mcp.ToolLevel(n) {
	case "warn":
		return "a lot of tools: models pick worse tools and get slower and costlier with many. Expose only what you need"
	case "bad":
		return "far too many tools: models pick worse tools, and run slower and costlier. Expose only what you need"
	}
	return "a good number of tools"
}

// openAPITotal is the tool count of every enabled OpenAPI upstream together, as a pill (nil when none exists).
func (a *Admin) openAPITotal() *pillView {
	list, _ := a.MCP.Upstreams.List()
	n, ups := 0, 0
	for _, u := range list {
		if u.IsOpenAPI() && u.Enabled {
			ups++
			n += a.MCP.Upstreams.OpenAPIToolCount(u.Alias)
		}
	}
	if ups == 0 {
		return nil
	}
	return &pillView{mcp.ToolLevel(n), fmt.Sprintf("%d %s", n, plural(n, "tool", "tools")), toolTip(n)}
}

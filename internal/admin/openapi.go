package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
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
func writeVerb(v string) bool { return v != "GET" && v != "HEAD" }

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
	As      string // the Advanced line: how the stored upstream sends its key
	AI      bool   // the assistant can run
	AIWhy   string
	// Check is false when the stored description has no operation that can check the connection: the form then
	// offers no way to skip it. Before a description is known it is true.
	Check bool
}

func (a *Admin) oaFormFor(r *http.Request, u mcp.Upstream) *oaForm {
	f := &oaForm{As: oaAs(u), Check: true}
	st := a.suggestState(r)
	f.AI, f.AIWhy = st.Enabled, st.Why
	if u.Alias != "" && u.IsOpenAPI() {
		if s, err := a.MCP.Upstreams.OpenAPI(u.Alias); err == nil {
			f.HasSpec, f.SpecURL, f.Title, f.Ops, f.Tools = true, s.Config.SpecURL, s.Doc.Title(), len(s.Ops), len(s.Tools)
			f.Level = mcp.ToolLevel(f.Tools)
			f.Check = s.HasConnectionCheck()
			for _, sv := range s.Doc.Servers(s.Config.SpecURL) {
				f.Servers = append(f.Servers, sv.URL)
			}
		}
	}
	return f
}

// specRead is a description a form carries and where it came from.
type specRead struct {
	Doc *openapi.Doc
	// URL is the address the description was read from ("" for pasted text). When the form held a base address
	// or a page, it is the place the search settled on.
	URL string
	// Base is the address entered when the search found the description under it ("" for pasted text).
	Base string
}

// readSpec returns the description a form carries: the pasted text when there is any, else what is found at the
// address. The address may be the description itself, a base address or a documentation page.
func readSpec(r *http.Request) (specRead, error) {
	text, specURL := strings.TrimSpace(r.PostFormValue("oa_spec_text")), strings.TrimSpace(r.PostFormValue("oa_spec_url"))
	var raw []byte
	var out specRead
	switch {
	case text != "":
		raw = []byte(text)
	case specURL != "":
		f, err := openapi.Discover(r.Context(), specURL)
		if err != nil {
			return specRead{}, err
		}
		raw, out.URL, out.Base = f.Raw, f.URL, f.Base
	default:
		return specRead{}, errors.New("enter an address or paste the description")
	}
	d, err := openapi.Parse(raw)
	if err != nil {
		return specRead{}, err
	}
	d.Source = out.URL
	if text == "" {
		d.SourceHash = openapi.Hash(raw)
	}
	out.Doc = d
	return out, nil
}

// baseFor chooses the base URL of an API whose form gave none: the first server of the description, unless that
// names this machine while the description came from elsewhere. A description with no server, or a relative one
// read from no address, gets the address it was read from (scheme, host and port, with any path the search used).
func baseFor(doc *openapi.Doc, specURL, base string) string {
	if base == "" && specURL != "" {
		if u, err := url.Parse(specURL); err == nil && u.Host != "" {
			base = u.Scheme + "://" + u.Host
		}
	}
	if sv := doc.Servers(specURL); len(sv) > 0 && (strings.HasPrefix(sv[0].URL, "http://") || strings.HasPrefix(sv[0].URL, "https://")) {
		u, err := url.Parse(sv[0].URL)
		local := err == nil && (u.Hostname() == "localhost" || u.Hostname() == "0.0.0.0" || strings.HasPrefix(u.Hostname(), "127."))
		if bu, berr := url.Parse(base); berr == nil && err == nil && bu.Hostname() == u.Hostname() {
			local = false // the description was read from that very host
		}
		if !local || base == "" {
			return sv[0].URL
		}
	}
	return base
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
	var found string // the address a search found the description under
	var old mcp.Upstream
	if edit {
		var ok bool
		if old, ok = a.MCP.Upstreams.Get(u.Alias); !ok {
			a.back(w, r, form, "", "unknown alias")
			return
		}
		if old.KindOrRemote() != u.Kind {
			a.back(w, r, form, "", "the type cannot be changed")
			return
		}
		if st, err := a.MCP.Upstreams.OpenAPI(u.Alias); err == nil {
			cfg = st.Config
		}
	}
	text, specURL := strings.TrimSpace(r.PostFormValue("oa_spec_text")), strings.TrimSpace(r.PostFormValue("oa_spec_url"))
	// A new upstream always reads its description. An edit keeps the stored one unless new text was pasted, the
	// address changed, or the address is to be fetched again.
	fresh := !edit || text != "" || (specURL != "" && specURL != cfg.SpecURL)
	switch {
	case fresh:
		rd, err := readSpec(r)
		if err != nil {
			a.back(w, r, form, "", err.Error())
			return
		}
		d, specURL := rd.Doc, rd.URL
		doc, found = d, rd.Base
		cfg.Spec, cfg.SpecURL = string(d.JSON()), specURL
		cfg.FetchedAt, cfg.SpecHash = 0, ""
		if d.SourceHash != "" {
			cfg.FetchedAt, cfg.SpecHash = time.Now().Unix(), d.SourceHash
		}
		if !edit {
			cfg.Selection = defaultSelection(d.Operations())
		}
	default:
		if cfg.Spec == "" {
			a.back(w, r, form, "", "enter an address or paste the description")
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
		if u.URL = baseFor(doc, cfg.SpecURL, found); u.URL == "" {
			a.back(w, r, form, "", "the description has no server, set a base URL in Advanced")
			return
		}
	}
	var prior *mcp.Upstream
	if edit {
		prior = &old
	}
	if err := applyOAAuth(r, &u, prior); err != nil {
		a.back(w, r, form, "", err.Error())
		return
	}
	if err := u.Validate(); err != nil {
		a.back(w, r, form, "", err.Error())
		return
	}
	// Test first: the server must answer and take the key before anything is stored. A failure returns to the
	// same form, which keeps what was entered. "Add without checking" / "Save without checking" posts oa_skip_check=1
	// and saves without calling the server. An edit asks only when what it
	// reaches the server with changed: the key, the base URL, the description or being switched on.
	changed := !edit || fresh || u.URL != old.URL || r.PostFormValue("oa_auth_value") != "" ||
		u.AuthKind != old.AuthKind || u.AuthName != old.AuthName || (!old.Enabled && u.Enabled)
	var tested mcp.TestResult
	if u.Enabled && !a.NoSaveTest && changed && r.PostFormValue("oa_skip_check") != "1" {
		draft := cfg
		if !edit {
			draft.Selection = defaultSelection(doc.Operations())
		}
		if tested = a.MCP.TestOpenAPIDraft(r.Context(), u, draft); tested.Error != "" {
			a.back(w, r, form, "", tested.Error)
			return
		}
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
	if u.AuthKind == mcp.AuthAuto && tested.Way != "" {
		_ = a.MCP.Upstreams.SetDetected(u.Alias, tested.Way, "")
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
		msg := u.Alias + " saved"
		if !edit {
			msg = u.Alias + " added"
		}
		if n == 0 {
			msg += ", pick its tools"
		}
		if issues := len(doc.Validate()); issues > 0 {
			msg += fmt.Sprintf(". %d %s in the description, Edit shows them", issues, plural(issues, "problem", "problems"))
		}
		a.back(w, r, "/admin/upstreams/"+u.Alias+"/tools", msg+queryNote(tested), "")
		return
	}
	a.back(w, r, "/admin/upstreams", u.Alias+" saved"+queryNote(tested), "")
}

// queryNote is the short warning appended to a saved message when the key travels in the web address.
func queryNote(tr mcp.TestResult) string {
	for _, w := range tr.Warnings {
		if w == mcp.QueryKeyWarning {
			return ": " + w
		}
	}
	return ""
}

// openAPIInput reads the OpenAPI fields of the form into u: the base URL when one is given. The key is read by
// applyOAAuth, which needs the stored upstream.
func openAPIInput(r *http.Request, u *mcp.Upstream) {
	u.Kind = mcp.KindOpenAPI
	u.URL = strings.TrimSpace(r.PostFormValue("oa_url"))
}

// oaAs is how the Advanced line shows the way a stored upstream sends its key: empty when skgate works it out.
func oaAs(u mcp.Upstream) string {
	switch u.AuthKind {
	case mcp.AuthBearer, mcp.AuthBasic:
		return u.AuthKind
	case mcp.AuthHeader:
		return u.AuthName
	case mcp.AuthQuery:
		return "?" + u.AuthName
	}
	return ""
}

// applyOAAuth reads the key and the Advanced line of the form into u. A key alone is sent the way the
// description says, or is found by trying; the Advanced line forces a way: bearer, basic, none, a header name, or
// ?name for a query parameter. An empty key keeps the stored one when the way allows it.
func applyOAAuth(r *http.Request, u *mcp.Upstream, old *mcp.Upstream) error {
	value := strings.TrimSpace(r.PostFormValue("oa_auth_value"))
	as := strings.TrimSpace(r.PostFormValue("oa_auth_as"))
	u.AuthName, u.AuthValue, u.DetectedKind, u.DetectedNote = "", value, "", ""
	// the stored key, when the new way can use it
	stored := ""
	if old != nil && old.IsOpenAPI() && old.AuthKind != mcp.AuthNone && old.AuthKind != mcp.AuthBasic {
		stored = old.AuthValue
	}
	switch low := strings.ToLower(as); {
	case low == "none":
		u.AuthKind, u.AuthValue = mcp.AuthNone, ""
	case low == "bearer":
		u.AuthKind = mcp.AuthBearer
	case low == "basic":
		u.AuthKind = mcp.AuthBasic
		user, pass, ok := strings.Cut(value, ":")
		switch {
		case value == "" && old != nil && old.AuthKind == mcp.AuthBasic:
			u.AuthName, u.AuthValue = old.AuthName, old.AuthValue
		case !ok || user == "" || pass == "":
			return errors.New("basic needs user:password")
		default:
			u.AuthName, u.AuthValue = user, pass
		}
		return nil
	case strings.HasPrefix(as, "?"):
		u.AuthKind, u.AuthName = mcp.AuthQuery, strings.TrimPrefix(as, "?")
	case as != "":
		u.AuthKind, u.AuthName = mcp.AuthHeader, as
	case value == "" && stored == "":
		u.AuthKind = mcp.AuthNone
	default:
		u.AuthKind = mcp.AuthAuto
	}
	if u.AuthKind != mcp.AuthNone && value == "" {
		u.AuthValue = stored
	}
	// the same key and way as before: what was found to work still holds
	if old != nil && old.AuthKind == u.AuthKind && old.AuthName == u.AuthName && old.AuthValue == u.AuthValue {
		u.DetectedKind = old.DetectedKind
	}
	return nil
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
	// SpecURL is where the description is read from ("" for a pasted one); Updated says when it was last read.
	SpecURL  string
	Updated  string
	NoUpdate string // why Update is off ("" when it is on)
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
	d := toolsData{U: a.view(u), Good: mcp.ToolsGood, Warn: mcp.ToolsWarn, Title: st.Doc.Title(), SpecURL: st.Config.SpecURL, Updated: oaUpdatedLine(st.Config)}
	if d.SpecURL == "" {
		d.NoUpdate = oaNoUpdateReason
	}
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
	if u, ok := a.MCP.Upstreams.Get(alias); !ok || !u.IsOpenAPI() {
		a.back(w, r, "/admin/upstreams", "", "that upstream is not an OpenAPI upstream")
		return
	}
	st, err := a.MCP.Upstreams.OpenAPI(alias)
	if err != nil {
		a.back(w, r, "/admin/upstreams", "", err.Error())
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
		a.back(w, r, back, "", "reload the page")
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
				a.back(w, r, back, "", fmt.Sprintf("invalid name %q: letters, digits, _ and -, up to %d", clip(name), openapi.MaxNameLen))
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
		msg += ". That is a lot"
	case "bad":
		msg += ". That is too many"
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
	rd, err := readSpec(r)
	if err != nil {
		httputil.JSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	d, specURL := rd.Doc, rd.URL
	ops := d.Operations()
	var servers []string
	for _, s := range d.Servers(specURL) {
		servers = append(servers, s.URL)
	}
	if len(servers) == 0 && rd.Base != "" {
		servers = append(servers, rd.Base)
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
		"servers": servers, "probe": len(d.ProbeOps(ops)) > 0, "issues": issueViews(d.Validate()), "ai": sug.Enabled, "aiWhy": sug.Why})
}

// openAPIRepair asks the assistant for fixes to the problems the reader found, applies them to a copy and returns
// the changes for approval together with the repaired description. Nothing is saved.
func (a *Admin) openAPIRepair(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, msg string) { httputil.JSON(w, status, map[string]any{"error": msg}) }
	rd, err := readSpec(r)
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	d := rd.Doc
	issues := d.Validate()
	for _, is := range issues {
		if is.Fatal {
			fail(http.StatusBadRequest, is.Message)
			return
		}
	}
	if len(issues) == 0 {
		fail(http.StatusBadRequest, "no problems to repair")
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
		fail(http.StatusBadGateway, "the repair does not apply: "+err.Error())
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
		fail(http.StatusBadRequest, "switch on tools first")
		return
	}
	if len(ops) > openapi.MaxDescribeOps {
		fail(http.StatusBadRequest, fmt.Sprintf("at most %d tools at a time", openapi.MaxDescribeOps))
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

// maxTryArgs is the largest argument text the tool tester takes.
const maxTryArgs = 256 << 10

// toolTryInfo answers the tool tester with what a client sees of one switched-on tool: its name, description and
// input schema.
func (a *Admin) toolTryInfo(w http.ResponseWriter, r *http.Request) {
	t, ok := a.MCP.OpenAPITool(r.PathValue("alias"), r.PostFormValue("name"))
	if !ok {
		httputil.JSON(w, http.StatusOK, map[string]any{"error": "that tool is off, save the tools first"})
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]any{"name": t["name"], "description": t["description"], "schema": t["inputSchema"]})
}

// toolTryRun calls one switched-on tool the way a client does and returns the answer or the error. It is the admin
// pressing Run: the upstream is really called.
func (a *Admin) toolTryRun(w http.ResponseWriter, r *http.Request) {
	alias, name := r.PathValue("alias"), r.PostFormValue("name")
	fail := func(msg string) { httputil.JSON(w, http.StatusOK, map[string]any{"ok": false, "error": msg}) }
	u, ok := a.MCP.Upstreams.Get(alias)
	switch {
	case !ok || !u.IsOpenAPI():
		fail("unknown upstream")
		return
	case !u.Enabled:
		fail("switch the upstream on first")
		return
	}
	if _, ok := a.MCP.OpenAPITool(alias, name); !ok {
		fail("that tool is off, save the tools first")
		return
	}
	args := map[string]any{}
	if txt := strings.TrimSpace(r.PostFormValue("args")); txt != "" {
		if len(txt) > maxTryArgs {
			fail("the arguments are too long")
			return
		}
		if err := json.Unmarshal([]byte(txt), &args); err != nil || args == nil {
			fail("the arguments must be a JSON object")
			return
		}
	}
	t0 := time.Now()
	text, isErr, err := a.MCP.CallOpenAPITool(r.Context(), alias, name, args)
	ms := time.Since(t0).Round(time.Millisecond)
	log.Printf("admin: tool run %s %s error=%t in %s", alias, name, err != nil || isErr, ms)
	if err != nil {
		fail(err.Error())
		return
	}
	text = indentAnswer(text)
	httputil.JSON(w, http.StatusOK, map[string]any{"ok": true, "isError": isErr, "text": text, "ms": ms.Milliseconds()})
}

// indentAnswer shows the JSON in an answer indented. A call answers with the status line and headers, a blank line
// and the body; only the body changes.
func indentAnswer(text string) string {
	head, body := "", text
	if strings.HasPrefix(text, "HTTP ") {
		if i := strings.Index(text, "\n\n"); i >= 0 {
			head, body = text[:i+2], text[i+2:]
		}
	}
	var v any
	if json.Unmarshal([]byte(strings.TrimSpace(body)), &v) != nil {
		return text
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return text
	}
	return head + string(pretty)
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
		v.Pill = pillView{"off", "no tools", "no tools on"}
	default:
		v.Pill = pillView{v.Level, fmt.Sprintf("%d %s", v.Tools, plural(v.Tools, "tool", "tools")), tip}
	}
	return v
}

// toolTip says how a model copes with that many tools.
func toolTip(n int) string {
	switch mcp.ToolLevel(n) {
	case "warn":
		return "a lot of tools, fewer work better"
	case "bad":
		return "too many tools, fewer work better"
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

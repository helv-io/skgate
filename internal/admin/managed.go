package admin

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/helv-io/skgate/internal/mcp"
	"github.com/helv-io/skgate/internal/timefmt"
)

// upstreamProcess runs a process action (start, stop, restart, update, clear-logs) on a managed
// upstream and returns to the upstreams list or the process page.
func (a *Admin) upstreamProcess(w http.ResponseWriter, r *http.Request) {
	alias, action := r.PathValue("alias"), r.PostFormValue("action")
	to := "/admin/upstreams"
	if r.PostFormValue("to") == "logs" {
		to = "/admin/upstreams/" + alias + "/logs"
	}
	u, ok := a.MCP.Upstreams.Get(alias)
	if !ok {
		a.back(w, r, "/admin/upstreams", "", "unknown alias")
		return
	}
	msg, err := a.MCP.ProcessAction(u, action)
	if err != nil {
		a.back(w, r, to, "", fmt.Sprintf("%s: %s", alias, err))
		return
	}
	a.MCP.Log.Printf("managed_action alias=%s action=%s", alias, action)
	a.back(w, r, to, fmt.Sprintf("%s: %s", alias, msg), "")
}

type logLine struct {
	T         string
	Src, Text string
	Class     string
}

type logsData struct {
	U     upstreamView
	Lines []logLine
	N     int
	Total int
}

// upstreamLogs shows the status and the captured output of a managed process.
func (a *Admin) upstreamLogs(w http.ResponseWriter, r *http.Request) {
	alias := r.PathValue("alias")
	u, ok := a.MCP.Upstreams.Get(alias)
	if !ok || !u.Managed() {
		a.back(w, r, "/admin/upstreams", "", "unknown managed upstream")
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n < 1 || n > 5000 {
		n = 300
	}
	all := a.MCP.ProcessLogs(alias, 0)
	d := logsData{U: a.view(u), N: n, Total: len(all)}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	for _, l := range all {
		c := ""
		switch l.Src {
		case "err":
			c = "warn"
		case "sys":
			c = "dim"
		}
		d.Lines = append(d.Lines, logLine{T: timefmt.Second(l.T), Src: l.Src, Text: l.Text, Class: c})
	}
	a.render(w, r, "upstream_logs", page{Title: "Process " + alias, Nav: "upstreams", Data: d})
}

type importData struct {
	Text    string
	Results []mcp.ImportResult
	Done    bool
	Managed bool
	Why     string
	Include bool
}

// upstreamImport shows the JSON import form (GET) and creates upstreams from pasted JSON (POST).
func (a *Admin) upstreamImport(w http.ResponseWriter, r *http.Request) {
	ok, why := a.MCP.ManagedState()
	d := importData{Managed: ok, Why: why}
	if r.Method == http.MethodPost {
		d.Text = r.PostFormValue("json")
		d.Include = r.PostFormValue("include") == "1"
		items, err := mcp.ParseImport(d.Text)
		if err != nil {
			a.back(w, r, "/admin/upstreams/import", "", err.Error())
			return
		}
		d.Results, d.Done = a.MCP.Upstreams.StoreImport(items, ok, d.Include), true
		created := 0
		for _, res := range d.Results {
			if res.Status == mcp.ImportCreated {
				created++
				a.MCP.SyncManaged(res.Alias)
			}
		}
		a.MCP.Log.Printf("upstream_import created=%d total=%d", created, len(d.Results))
		t := toast{toastOK, fmt.Sprintf("%d of %d imported", created, len(d.Results))}
		if created == 0 {
			t = toast{toastBad, "nothing imported"}
		}
		http.Redirect(w, r, a.stash(r, "import", d, t), http.StatusSeeOther)
		return
	}
	v, _ := a.DB.GetSetting(settingLastInclude)
	d.Include = v == "1"
	a.render(w, r, "upstream_import", page{Title: "Import upstreams", Nav: "upstreams", Data: d})
}

// upstreamExport downloads the upstreams as JSON (secret values are left out).
func (a *Admin) upstreamExport(w http.ResponseWriter, r *http.Request) {
	list, _ := a.MCP.Upstreams.List()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="skgate-upstreams.json"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(mcp.ExportJSON(list))
}

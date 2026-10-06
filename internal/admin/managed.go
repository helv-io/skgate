package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/helv-io/skgate/internal/managed"
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

// logLine is one line of a process log, as the page shows it and as the live stream sends it.
type logLine struct {
	ID    uint64 `json:"id"`
	Run   int    `json:"run"`
	Ms    int64  `json:"ms"`   // when, in Unix milliseconds (the page shows it as a clock time or as "5 s ago")
	Abs   string `json:"abs"`  // the clock time in the configured zone
	Full  string `json:"full"` // date and time in the configured zone
	Src   string `json:"src"`
	Level string `json:"lvl,omitempty"`
	Text  string `json:"text"`
	Trunc bool   `json:"trunc,omitempty"`
	Start bool   `json:"start,omitempty"`
	// Mark is the divider label before a run's first line (page only): "since last start" or "previous run".
	Mark string `json:"-"`
}

func toLogLine(l managed.Line) logLine {
	return logLine{ID: l.ID, Run: l.Run, Ms: l.T.UnixMilli(), Abs: timefmt.Second(l.T), Full: timefmt.Log(l.T), Src: l.Src,
		Level: l.Level, Text: l.Text, Trunc: l.Trunc, Start: l.Start}
}

type logsData struct {
	U      upstreamView
	Lines  []logLine
	LastID uint64
	Max    int  // lines the page keeps
	Levels bool // some line carries a level, so the level filter is offered
}

// upstreamLogs shows the status and the captured output of a managed process; the page then follows the live
// stream (see upstreamLogStream).
func (a *Admin) upstreamLogs(w http.ResponseWriter, r *http.Request) {
	alias := r.PathValue("alias")
	u, ok := a.MCP.Upstreams.Get(alias)
	if !ok || !u.Managed() {
		a.back(w, r, "/admin/upstreams", "", "unknown managed upstream")
		return
	}
	d := logsData{U: a.view(u), Max: a.Cfg.ManagedLogLines}
	if d.U.Proc != nil { // already on the process page: the pill is the status, not a link back to itself
		d.U.Proc.Href = ""
	}
	if ring, ok := a.MCP.ProcessLog(alias); ok {
		lines := ring.Last(0)
		lastStart := -1
		for i, l := range lines {
			d.Lines = append(d.Lines, toLogLine(l))
			d.LastID = l.ID
			d.Levels = d.Levels || l.Level != ""
			if l.Start {
				lastStart = i
			}
		}
		for i := range d.Lines {
			if d.Lines[i].Start {
				d.Lines[i].Mark = "previous run"
				if i == lastStart {
					d.Lines[i].Mark = "since last start"
				}
			}
		}
	}
	a.render(w, r, "upstream_logs", page{Title: "Process " + alias, Nav: "upstreams", Data: d})
}

// upstreamLogStream is the live view of a process log: server-sent events, one "line" event per line (its id is
// the line's, so a reconnecting browser resumes with Last-Event-ID) and a "reset" event when the log was
// cleared. It exists only while the page is open; nothing is kept per viewer.
func (a *Admin) upstreamLogStream(w http.ResponseWriter, r *http.Request) {
	ring, ok := a.MCP.ProcessLog(r.PathValue("alias"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	after, _ := strconv.ParseUint(r.Header.Get("Last-Event-ID"), 10, 64)
	if after == 0 {
		after, _ = strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	}
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "retry: 3000\n\n")
	_ = rc.Flush()
	gen := ring.Gen()
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		wake := ring.Wait() // taken before reading, so a line added in between wakes the next round
		lines, g, reset := ring.Since(after, gen)
		gen = g
		if reset {
			if _, err := io.WriteString(w, "event: reset\ndata: {}\n\n"); err != nil {
				return
			}
		}
		for _, l := range lines {
			b, _ := json.Marshal(toLogLine(l))
			if _, err := fmt.Fprintf(w, "id: %d\nevent: line\ndata: %s\n\n", l.ID, b); err != nil {
				return
			}
			after = l.ID
		}
		if reset || len(lines) > 0 {
			_ = rc.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-wake:
		case <-tick.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			_ = rc.Flush()
		}
	}
}

// upstreamLogDownload sends the kept output of a process as a text file.
func (a *Admin) upstreamLogDownload(w http.ResponseWriter, r *http.Request) {
	alias := r.PathValue("alias")
	ring, ok := a.MCP.ProcessLog(alias)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+alias+`-output.txt"`)
	w.Header().Set("Cache-Control", "no-store")
	for _, l := range ring.Last(0) {
		_, _ = fmt.Fprintf(w, "%s [%s] %s\n", timefmt.Log(l.T), l.Src, l.Text)
	}
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
		a.showOnce(w, r, "upstream_import", "import_result", "import", d, t)
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

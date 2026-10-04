package app

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// groupOf returns the buttons (their labels) of one action group of the process page.
func groupOf(t *testing.T, page, name string) []string {
	t.Helper()
	i := strings.Index(page, `aria-label="`+name+`">`)
	if i < 0 {
		t.Fatalf("no %s group:\n%s", name, page)
	}
	end := strings.Index(page[i:], "</div></div>")
	var labels []string
	for _, m := range regexp.MustCompile(`<button class="act[^"]*">([^<]*)</button>|<a class="act" href="[^"]*">([^<]*)</a>`).FindAllStringSubmatch(page[i:i+end], -1) {
		labels = append(labels, m[1]+m[2])
	}
	return labels
}

// The process page groups its buttons: Lifecycle shows only what applies to the state (start when stopped, restart
// and stop when running, nothing to start when disabled), Maintenance has update (and check for git), Go to has the
// links, and clear logs is alone in a Danger group, last.
func TestProcessPageGroupsItsButtons(t *testing.T) {
	_, br, csrf := managedApp(t)
	br.post("/admin/upstreams/save", stdioForm(csrf, "tools", nil))
	_, page := br.get("/admin/upstreams/tools/logs")
	if got := strings.Join(groupOf(t, page, "Lifecycle"), ","); got != "start" {
		t.Errorf("stopped: lifecycle = %q, want start", got)
	}
	br.get("/admin/upstreams/tools/test") // starts it
	_, page = br.get("/admin/upstreams/tools/logs")
	if got := strings.Join(groupOf(t, page, "Lifecycle"), ","); got != "restart,stop" {
		t.Errorf("running: lifecycle = %q, want restart,stop", got)
	}
	if !strings.Contains(page, `data-confirm="Stop the process? It stays stopped until started."`) {
		t.Error("stop still asks first")
	}
	if got := strings.Join(groupOf(t, page, "Maintenance"), ","); got != "update" {
		t.Errorf("maintenance (stdio) = %q, want update", got)
	}
	if got := strings.Join(groupOf(t, page, "Go to"), ","); got != "test,refresh,back" {
		t.Errorf("go to = %q", got)
	}
	if got := strings.Join(groupOf(t, page, "Danger"), ","); got != "clear logs" || !strings.Contains(page, `<button class="act danger">clear logs</button>`) {
		t.Errorf("danger = %q", got)
	}
	last := -1
	for _, m := range []string{">Lifecycle<", ">Maintenance<", ">Go to<", ">Danger<"} {
		i := strings.Index(page, m)
		if i < 0 || i < last {
			t.Fatalf("group %s is missing or out of order", m)
		}
		last = i
	}
	// a disabled upstream cannot be started: the group says so
	br.post("/admin/upstreams/save", stdioForm(csrf, "off", url.Values{"enabled": {}}))
	_, page = br.get("/admin/upstreams/off/logs")
	if len(groupOf(t, page, "Lifecycle")) != 0 || !strings.Contains(page, "disabled: enable the upstream to start it") {
		t.Errorf("disabled: nothing to start:\n%s", page)
	}
}

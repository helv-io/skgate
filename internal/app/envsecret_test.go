package app

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// envInputs returns the value input of every env row on a page, in order, with its row's name and flag.
func envRows(t *testing.T, page string) []map[string]string {
	t.Helper()
	rows, _ := pairBlocks(t, page)
	var out []map[string]string
	for _, row := range strings.Split(rows[2], `<div class="pair">`)[1:] {
		m := map[string]string{}
		for _, in := range regexp.MustCompile(`<input [^>]*>`).FindAllString(row, -1) {
			attr := func(k string) string {
				if x := regexp.MustCompile(` ` + k + `="([^"]*)"`).FindStringSubmatch(in); x != nil {
					return x[1]
				}
				return ""
			}
			switch attr("name") {
			case "env_name":
				m["name"] = attr("value")
			case "env_secret":
				m["flag"] = attr("value")
			case "env_value":
				m["type"], m["value"], m["placeholder"], m["autocomplete"] = attr("type"), attr("value"), attr("placeholder"), attr("autocomplete")
			}
		}
		out = append(out, m)
	}
	return out
}

// Secret values are masked password fields that browsers do not fill; the rest show in clear. The flag is
// kept when the upstream is saved and edited, whatever the name looks like.
func TestEnvRowsKeepTheirSecretFlag(t *testing.T) {
	a, br, csrf := managedApp(t)
	form := stdioForm(csrf, "tools", url.Values{
		"env_name":   {"BASE_URL", "API_KEY", "KEY_FILE", "ENDPOINT", "FAKE_MCP", ""},
		"env_secret": {"0", "1", "0", "1", "", ""},
		"env_value":  {"http://svc:8000/mcp", "k-1234567890", "/etc/key.pem", "hidden-endpoint-1", "1", ""}})
	if r, _ := br.post("/admin/upstreams/save", form); flashKind(r) != "ok" {
		t.Fatal(flashOf(r))
	}
	u, _ := a.MCP.Upstreams.Get("tools")
	want := map[string]bool{"BASE_URL": false, "API_KEY": true, "KEY_FILE": false, "ENDPOINT": true, "FAKE_MCP": false} // secret?
	for n, secret := range want {
		if u.EnvSecret(n) != secret {
			t.Errorf("%s: secret=%v, want %v (a row without a flag follows its name)", n, u.EnvSecret(n), secret)
		}
	}
	_, edit := br.get("/admin/upstreams/tools/edit")
	if !strings.Contains(edit, `data-secret-pattern="`) || strings.Count(edit, "data-secret-pattern") != 1 {
		t.Fatal("the env list, and only it, carries the secret pattern")
	}
	rows := envRows(t, edit)
	if len(rows) != 5 {
		t.Fatalf("rows: %v", rows)
	}
	for _, r := range rows {
		secret := want[r["name"]]
		switch {
		case secret && (r["type"] != "password" || r["autocomplete"] != "new-password" || r["flag"] != "1" || strings.Contains(r["value"], "hidden-endpoint") || !strings.HasPrefix(r["value"], "****")):
			t.Errorf("secret row %v", r)
		case !secret && (r["type"] != "text" || r["flag"] != "0" || r["value"] == "" || strings.HasPrefix(r["value"], "****")):
			t.Errorf("plain row %v", r)
		}
	}
	if strings.Contains(edit, "k-1234567890") || !strings.Contains(edit, "http://svc:8000/mcp") {
		t.Fatal("secrets must not be on the page and plain values must")
	}
	// the rendered rows saved unchanged keep every value and every flag
	var names, flags, vals []string
	for _, r := range rows {
		names, flags, vals = append(names, r["name"]), append(flags, r["flag"]), append(vals, r["value"])
	}
	save := stdioForm(csrf, "tools", url.Values{"mode": {"edit"}, "env_name": names, "env_secret": flags, "env_value": vals})
	if r, _ := br.post("/admin/upstreams/save", save); flashKind(r) != "ok" {
		t.Fatal(flashOf(r))
	}
	u2, _ := a.MCP.Upstreams.Get("tools")
	if len(u2.Env) != 5 || u2.Env[1].Value != "k-1234567890" || u2.Env[3].Value != "hidden-endpoint-1" || u2.EnvSecret("KEY_FILE") || !u2.EnvSecret("ENDPOINT") {
		t.Fatalf("%+v %v", u2.Env, u2.PlainEnv)
	}
}

// Rows left empty are not stored, so nothing has to be deleted; a new blank row says Optional.
func TestEmptyEnvRowsAreIgnoredAndNotRequired(t *testing.T) {
	a, br, csrf := managedApp(t)
	form := stdioForm(csrf, "tools", url.Values{
		"env_name":  {"FAKE_MCP", "OPTIONAL_ONE", "OPTIONAL_TOKEN", "BASE_URL"},
		"env_value": {"1", "", "", "http://svc"}})
	if r, _ := br.post("/admin/upstreams/save", form); flashKind(r) != "ok" {
		t.Fatal(flashOf(r))
	}
	u, _ := a.MCP.Upstreams.Get("tools")
	if len(u.Env) != 2 || u.Env[0].Name != "FAKE_MCP" || u.Env[1].Name != "BASE_URL" {
		t.Fatalf("stored: %+v", u.Env)
	}
	_, add := br.get("/admin/upstreams/new")
	blank := envRows(t, add)
	if len(blank) != 1 || blank[0]["placeholder"] != "Optional" || blank[0]["value"] != "" || blank[0]["flag"] != "" {
		t.Fatalf("a blank row: %v", blank)
	}
	if strings.Contains(add, `name="env_value" value="YOUR_`) || strings.Contains(add, "required") && strings.Contains(add, `name="env_value"`) && regexp.MustCompile(`name="env_value"[^>]* required`).MatchString(add) {
		t.Fatal("an env value is never required by the form")
	}
}

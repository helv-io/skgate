package app

import (
	"net/url"
	"regexp"
	"testing"
)

var addFormInclude = regexp.MustCompile(`name="include" value="1" data-include (checked)?[^>]*>\s*include in /mcp`)

func includePreselected(t *testing.T, br *browser) bool {
	t.Helper()
	_, page := br.get("/admin/upstreams")
	m := addFormInclude.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("add form has no include checkbox")
	}
	return m[1] == "checked"
}

func TestIncludeDefaultRemembersLastChoice(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	up := fakeUpstream(t)
	if includePreselected(t, br) {
		t.Fatal("first time the default must be off")
	}
	save := func(mode, alias string, include bool) {
		v := url.Values{"csrf": {csrf}, "mode": {mode}, "alias": {alias}, "url": {up.URL}, "auth_kind": {"none"}, "enabled": {"1"}}
		if include {
			v.Set("include", "1")
		}
		if r, _ := br.post("/admin/upstreams/save", v); r.StatusCode != 303 || flashKind(r) != "ok" {
			t.Fatalf("save %s: %d %q", alias, r.StatusCode, flashKind(r))
		}
	}
	save("new", "a", true)
	if !includePreselected(t, br) {
		t.Error("after creating an included upstream the default must be on")
	}
	save("new", "b", false)
	if includePreselected(t, br) {
		t.Error("after creating a not-included upstream the default must be off")
	}
	save("edit", "a", true)
	if !includePreselected(t, br) {
		t.Error("editing counts as the most recent choice")
	}
	// a failed save (duplicate alias) must not change it, and neither does the toggle button
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "alias": {"a"}, "url": {up.URL}, "auth_kind": {"none"}})
	br.post("/admin/upstreams/toggle", url.Values{"csrf": {csrf}, "alias": {"a"}, "flag": {"include"}})
	if !includePreselected(t, br) {
		t.Error("only successful saves update the remembered choice")
	}
	if v, _ := a.DB.GetSetting("last_include_in_mcp"); v != "1" {
		t.Errorf("stored %q", v)
	}
}

package app

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var rowDivRE = regexp.MustCompile(`<div class="pair( single)?">`)

var pairsRowsRE = regexp.MustCompile(`(?s)<div class="pairs-rows" data-pairs-rows>(.*?)</div>\s*<template data-pairs-template>(.*?)</template>`)

// pairBlocks returns, per dynamic list on the page (headers, arguments, environment), the rendered
// rows and the template row.
func pairBlocks(t *testing.T, page string) (rows, tpls []string) {
	t.Helper()
	for _, m := range pairsRowsRE.FindAllStringSubmatch(page, -1) {
		rows, tpls = append(rows, m[1]), append(tpls, m[2])
	}
	if len(rows) != 3 {
		t.Fatalf("expected three dynamic lists (headers, arguments, environment), found %d", len(rows))
	}
	return rows, tpls
}

func countRows(block string) (n, removable int) {
	return len(rowDivRE.FindAllString(block, -1)), strings.Count(block, "data-pairs-remove")
}

// A fresh form starts each list with one blank row that has no delete control, and offers an Add
// control plus a template whose row is deletable. The list has no fixed size.
func TestPairListStartsWithOneUndeletableRow(t *testing.T) {
	_, _, br, _ := signedIn(t, nil)
	for _, path := range []string{"/admin/upstreams"} {
		_, page := br.get(path)
		rows, tpls := pairBlocks(t, page)
		for i, name := range []string{"headers", "arguments", "environment"} {
			if n, rm := countRows(rows[i]); n != 1 || rm != 0 {
				t.Errorf("%s: %d rows, %d deletable; want 1 and 0", name, n, rm)
			}
			if n, rm := countRows(tpls[i]); n != 1 || rm != 1 {
				t.Errorf("%s template: %d rows, %d deletable; want 1 and 1", name, n, rm)
			}
		}
		if strings.Count(page, "data-pairs-add") != 3 {
			t.Errorf("each list needs an Add control")
		}
	}
}

// More than three stored values render as that many rows; only the first is undeletable, and the
// masked values stay masked.
func TestPairListRendersEveryStoredRow(t *testing.T) {
	a, br, csrf := managedApp(t)
	names, vals := []string{"FAKE_MCP"}, []string{"1"}
	for i := 0; i < 9; i++ {
		names, vals = append(names, fmt.Sprintf("VAR_%d", i)), append(vals, fmt.Sprintf("secret-value-%04d", i))
	}
	form := stdioForm(csrf, "many", url.Values{"env_name": names, "env_value": vals})
	if r, _ := br.post("/admin/upstreams/save", form); flashKind(r) != "ok" {
		t.Fatal(flashOf(r))
	}
	if u, _ := a.MCP.Upstreams.Get("many"); len(u.Env) != 10 {
		t.Fatalf("stored %d env vars, want 10", len(u.Env))
	}
	_, edit := br.get("/admin/upstreams/many/edit")
	rows, _ := pairBlocks(t, edit)
	if n, rm := countRows(rows[2]); n != 10 || rm != 9 {
		t.Fatalf("env: %d rows, %d deletable; want 10 and 9", n, rm)
	}
	if n, rm := countRows(rows[0]); n != 1 || rm != 0 {
		t.Fatalf("headers: %d rows, %d deletable; want 1 and 0", n, rm)
	}
	first := strings.SplitN(rows[2], `class="pair"`, 3)[1]
	if strings.Contains(first, "data-pairs-remove") || !strings.Contains(first, `value="FAKE_MCP"`) {
		t.Fatalf("the first row must be undeletable: %s", first)
	}
	if strings.Contains(edit, "secret-value-0003") || !strings.Contains(edit, "************0003") {
		t.Fatal("values must stay masked")
	}
}

// Saving the rendered (masked) rows unchanged keeps every stored value; blank rows anywhere in the
// submission are ignored, and a row removed client-side is gone.
func TestPairListSaveIgnoresBlankRowsAndKeepsMasked(t *testing.T) {
	a, br, csrf := managedApp(t)
	names := []string{"FAKE_MCP", "", "A", "", "B", "C", "", "D", "E", "F", "G", ""}
	vals := []string{"1", "", "value-a-1111", "x", "value-b-2222", "value-c-3333", "", "value-d-4444", "value-e-5555", "value-f-6666", "value-g-7777", ""}
	if r, _ := br.post("/admin/upstreams/save", stdioForm(csrf, "blanks", url.Values{"env_name": names, "env_value": vals})); flashKind(r) != "ok" {
		t.Fatal(flashOf(r))
	}
	u, _ := a.MCP.Upstreams.Get("blanks")
	if len(u.Env) != 8 || u.Env[1].Name != "A" || u.Env[7].Name != "G" {
		t.Fatalf("%+v", u.Env)
	}
	// resubmit with masked values, one row deleted (B), one edited (C), one appended
	form := stdioForm(csrf, "blanks", url.Values{"mode": {"edit"},
		"env_name":  {"FAKE_MCP", "A", "C", "D", "E", "F", "G", "NEW", ""},
		"env_value": {"1", "************1111", "changed", "************4444", "", "************6666", "************7777", "n", ""}})
	if r, _ := br.post("/admin/upstreams/save", form); flashKind(r) != "ok" {
		t.Fatal(flashOf(r))
	}
	u, _ = a.MCP.Upstreams.Get("blanks")
	got := map[string]string{}
	for _, kv := range u.Env {
		got[kv.Name] = kv.Value
	}
	want := map[string]string{"FAKE_MCP": "1", "A": "value-a-1111", "C": "changed", "D": "value-d-4444", "E": "value-e-5555", "F": "value-f-6666", "G": "value-g-7777", "NEW": "n"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

// All-blank rows store nothing, and a list with no stored rows renders one blank row again.
func TestPairListAllBlankStoresNothing(t *testing.T) {
	a, br, csrf := managedApp(t)
	form := stdioForm(csrf, "none", url.Values{"env_name": {"", "", "", "", ""}, "env_value": {"", "", "", "", ""}})
	if r, _ := br.post("/admin/upstreams/save", form); flashKind(r) != "ok" {
		t.Fatal(flashOf(r))
	}
	if u, _ := a.MCP.Upstreams.Get("none"); len(u.Env) != 0 {
		t.Fatalf("%+v", u.Env)
	}
	_, edit := br.get("/admin/upstreams/none/edit")
	rows, _ := pairBlocks(t, edit)
	if n, rm := countRows(rows[2]); n != 1 || rm != 0 {
		t.Fatalf("%d rows, %d deletable", n, rm)
	}
}

// Custom headers of a remote upstream use the same component and have no row cap of three.
func TestPairListCustomHeadersManyRows(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	up := fakeUpstream(t)
	var names, vals []string
	for i := 0; i < 7; i++ {
		names, vals = append(names, fmt.Sprintf("X-H-%d", i)), append(vals, fmt.Sprintf("hv-secret-%04d", i))
		names, vals = append(names, ""), append(vals, "")
	}
	v := url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"remote"}, "alias": {"hdrs"}, "url": {up.URL}, "auth_kind": {"none"}, "enabled": {"1"},
		"hdr_name": names, "hdr_value": vals}
	if r, _ := br.post("/admin/upstreams/save", v); flashKind(r) != "ok" {
		t.Fatal(flashOf(r))
	}
	if u, _ := a.MCP.Upstreams.Get("hdrs"); len(u.Headers) != 7 {
		t.Fatalf("%d headers stored", len(u.Headers))
	}
	_, edit := br.get("/admin/upstreams/hdrs/edit")
	rows, _ := pairBlocks(t, edit)
	if n, rm := countRows(rows[0]); n != 7 || rm != 6 {
		t.Fatalf("headers: %d rows, %d deletable; want 7 and 6", n, rm)
	}
}

// Arguments use the same component: one undeletable row to start, every stored argument as a row,
// blank rows ignored, order kept, and a space inside an argument survives.
func TestArgsRowsRenderSaveAndOrder(t *testing.T) {
	a, br, csrf := managedApp(t)
	args := []string{"-y", "", "@scope/pkg@1.2.3", "", "--flag=a b", "--x", "", "--y", "--z", "--w"}
	if r, _ := br.post("/admin/upstreams/save", stdioForm(csrf, "argy", url.Values{"args": args})); flashKind(r) != "ok" {
		t.Fatal(flashOf(r))
	}
	u, _ := a.MCP.Upstreams.Get("argy")
	if got := strings.Join(u.Args, "|"); got != "-y|@scope/pkg@1.2.3|--flag=a b|--x|--y|--z|--w" {
		t.Fatalf("%q", got)
	}
	_, edit := br.get("/admin/upstreams/argy/edit")
	rows, _ := pairBlocks(t, edit)
	if n, rm := countRows(rows[1]); n != 7 || rm != 6 {
		t.Fatalf("args: %d rows, %d deletable; want 7 and 6", n, rm)
	}
	first := strings.SplitN(rows[1], `class="pair single"`, 3)[1]
	if strings.Contains(first, "data-pairs-remove") || !strings.Contains(first, `value="-y"`) {
		t.Fatalf("first row: %s", first)
	}
	// nothing but blank rows stores no arguments
	if r, _ := br.post("/admin/upstreams/save", stdioForm(csrf, "noargs", url.Values{"args": {"", ""}})); flashKind(r) != "ok" {
		t.Fatal(flashOf(r))
	}
	if u, _ := a.MCP.Upstreams.Get("noargs"); len(u.Args) != 0 {
		t.Fatalf("%q", u.Args)
	}
	_, e2 := br.get("/admin/upstreams/noargs/edit")
	r2, _ := pairBlocks(t, e2)
	if n, rm := countRows(r2[1]); n != 1 || rm != 0 {
		t.Fatalf("empty args: %d rows, %d deletable", n, rm)
	}
}

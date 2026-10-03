package mcp

import (
	"errors"
	"strings"
	"testing"
)

// Only always-on managed servers can be on /mcp; on-demand ones are coerced out, whatever was asked.
func TestOnDemandManagedIsNeverOnMCP(t *testing.T) {
	e := newEnv(t, nil)
	mk := func(alias, life string) Upstream {
		return Upstream{Alias: alias, Kind: KindStdio, Command: "/bin/true", Enabled: true, IncludeInMCP: true, Lifecycle: life}
	}
	for alias, life := range map[string]string{"od": "on-demand", "blank": "", "ao": "always"} {
		if err := e.srv.Upstreams.Create(mk(alias, life)); err != nil {
			t.Fatal(err)
		}
	}
	e.srv.Upstreams.Create(Upstream{Alias: "rem", URL: "http://127.0.0.1:1/mcp", AuthKind: AuthNone, Enabled: true, IncludeInMCP: true})
	got := map[string]bool{}
	for _, a := range []string{"od", "blank", "ao", "rem"} {
		u, _ := e.srv.Upstreams.Get(a)
		got[a] = u.IncludeInMCP
	}
	if got["od"] || got["blank"] || !got["ao"] || !got["rem"] {
		t.Fatalf("stored flags: %v", got)
	}
	// switching an always-on server to on-demand takes it off /mcp
	u, _ := e.srv.Upstreams.Get("ao")
	u.Lifecycle = "on-demand"
	if err := e.srv.Upstreams.Update(u, true); err != nil {
		t.Fatal(err)
	}
	if u, _ = e.srv.Upstreams.Get("ao"); u.IncludeInMCP {
		t.Fatal("on-demand after an edit must leave /mcp")
	}
	// the toggle refuses on-demand managed servers, accepts always-on and remote ones
	if _, err := e.srv.Upstreams.SetFlag("od", FlagInclude, true); !errors.Is(err, ErrOnDemandInMCP) || !strings.Contains(err.Error(), "always-on") {
		t.Fatalf("toggle on-demand: %v", err)
	}
	u, _ = e.srv.Upstreams.Get("ao")
	u.Lifecycle = "always"
	e.srv.Upstreams.Update(u, true)
	if _, err := e.srv.Upstreams.SetFlag("ao", FlagInclude, true); err != nil {
		t.Fatalf("toggle always-on: %v", err)
	}
	if _, err := e.srv.Upstreams.SetFlag("rem", FlagInclude, false); err != nil {
		t.Fatal(err)
	}
	// even a stored flag on an on-demand row is ignored by the aggregation
	if _, err := e.db.Exec(`UPDATE upstreams SET include_in_mcp=1 WHERE alias IN ('od','blank')`); err != nil {
		t.Fatal(err)
	}
	inc, err := e.srv.Upstreams.Included()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, u := range inc {
		names = append(names, u.Alias)
	}
	if strings.Join(names, ",") != "ao" {
		t.Fatalf("aggregated: %v", names)
	}
}

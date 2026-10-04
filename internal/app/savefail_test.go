package app

import (
	"net/url"
	"testing"
)

// A write the database refuses must not be reported as done.
func TestAdminReportsAFailedDelete(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	if _, err := a.DB.Exec(`DROP TABLE oauth_tokens`); err != nil {
		t.Fatal(err)
	}
	r, _ := br.post("/admin/clients/delete", url.Values{"csrf": {csrf}, "id": {"nobody"}})
	if flashKind(r) != "bad" {
		t.Errorf("client delete with a broken database was reported as %q", flashKind(r))
	}
	r, _ = br.post("/admin/upstreams/nope/delete", url.Values{"csrf": {csrf}})
	if flashKind(r) != "bad" {
		t.Errorf("deleting an unknown upstream was reported as %q", flashKind(r))
	}
}

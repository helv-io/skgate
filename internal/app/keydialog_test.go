package app

import (
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// browserCookies hands the signed-in session of a test browser to puppeteer (page.setCookie takes this JSON).
func browserCookies(br *browser, base string) string {
	u, _ := url.Parse(base)
	type cookie struct {
		Name  string `json:"name"`
		Value string `json:"value"`
		URL   string `json:"url"`
	}
	var cookies []cookie
	for _, c := range br.c.Jar.Cookies(u) {
		cookies = append(cookies, cookie{c.Name, c.Value, base})
	}
	cj, _ := json.Marshal(cookies)
	return string(cj)
}

// In a real browser (optional, like the layout sweep): the key dialog's own forms work. Regenerate and revoke ask
// first from inside the dialog; Cancel and Escape go back to it, Confirm really submits, Save stores the form.
func TestKeyDialogFormsInBrowser(t *testing.T) {
	chrome, pp := os.Getenv("SKGATE_CHROME"), os.Getenv("SKGATE_PUPPETEER")
	node, err := exec.LookPath("node")
	if chrome == "" || pp == "" || err != nil {
		t.Skip("set SKGATE_CHROME and SKGATE_PUPPETEER (and install node) to run the key dialog check in a browser")
	}
	a, ts, br, csrf := signedIn(t, nil)
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"first"}})
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"second"}})
	cj := browserCookies(br, ts.URL)
	// the list is newest first: "second" is the first row, renamed and then revoked by the script
	script, _ := filepath.Abs(filepath.Join("testdata", "keydialog.js"))
	out, err := exec.Command(node, script, ts.URL, chrome, filepath.Join(pp, "node_modules", "puppeteer-core"), cj).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ALL OK") {
		t.Fatalf("%v\n%s", err, out)
	}
	ks, _ := a.Keys.List()
	if len(ks) != 1 || ks[0].Label != "first" {
		t.Fatalf("the revoked key was never used, so it is gone and the other stays: %+v", ks)
	}
}

// In a real browser: text the expiry parser cannot read marks the field invalid (red border, aria-invalid) and the
// form does not submit, even with its folded Limits section closed (it opens to show the field); clearing the text
// lets the key be created.
func TestUnreadableExpiryBlocksSubmitInBrowser(t *testing.T) {
	chrome, pp := os.Getenv("SKGATE_CHROME"), os.Getenv("SKGATE_PUPPETEER")
	node, err := exec.LookPath("node")
	if chrome == "" || pp == "" || err != nil {
		t.Skip("set SKGATE_CHROME and SKGATE_PUPPETEER (and install node) to run the expiry check in a browser")
	}
	a, ts, br, _ := signedIn(t, nil)
	script, _ := filepath.Abs(filepath.Join("testdata", "expiryblock.js"))
	out, err := exec.Command(node, script, ts.URL, chrome, filepath.Join(pp, "node_modules", "puppeteer-core"), browserCookies(br, ts.URL)).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ALL OK") {
		t.Fatalf("%v\n%s", err, out)
	}
	ks, _ := a.Keys.List()
	if len(ks) != 1 || ks[0].Label != "blocked then fixed" {
		t.Fatalf("exactly the key created after clearing the text exists: %+v", ks)
	}
}

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runBrowserScript runs testdata/<name> with node and puppeteer against the test server, signed in as the test
// browser, and fails unless it prints ALL OK. The check is optional like the layout sweep: it needs
// SKGATE_CHROME, SKGATE_PUPPETEER and node.
func runBrowserScript(t *testing.T, name, base string, br *browser, args ...string) {
	t.Helper()
	chrome, pp := os.Getenv("SKGATE_CHROME"), os.Getenv("SKGATE_PUPPETEER")
	node, err := exec.LookPath("node")
	if chrome == "" || pp == "" || err != nil {
		t.Skip("set SKGATE_CHROME and SKGATE_PUPPETEER (and install node) to run the " + name + " check in a browser")
	}
	script, _ := filepath.Abs(filepath.Join("testdata", name))
	argv := append([]string{script, base, chrome, filepath.Join(pp, "node_modules", "puppeteer-core"), browserCookies(br, base)}, args...)
	out, err := exec.Command(node, argv...).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ALL OK") {
		t.Fatalf("%v\n%s", err, out)
	}
}

package app

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// fixedWaits is how many fixed sleeps each browser script (Chrome, through puppeteer) still has. A fixed sleep after a
// click is a race: it passes on a fast machine and fails on a slow one, and the failure names an assertion that has
// nothing wrong with it (a revoked key's row "still there" because the page had not read itself again yet). A script
// waits on something the page shows instead: idle.js (the page's own data-busy mark) after a save or a post,
// waitForSelector or waitForFunction for anything else. The numbers only go down: a script that gets a new sleep fails
// this test, and so does one that lost a sleep without lowering its number here, so they cannot creep back.
// A wait that is the point of the test (nothing may happen in the next seconds) and a short poll are counted too.
var fixedWaits = map[string]int{
	"codebox.js":     9,
	"descline.js":    4,
	"expiryblock.js": 3,
	"importjson.js":  2,
	"inplace.js":     2,
	"keydialog.js":   10,
	"menubrowser.js": 7,
	"results.js":     2,
	"sistatus.js":    4,
	"sweep.js":       5,
	"toolpick.js":    2,
	"update_tap.js":  2,
}

var fixedWait = regexp.MustCompile(`\b(?:wait|sleep|tick)\(\s*\d+\s*\)|setTimeout\(\s*\w+\s*,\s*\d+\s*\)`)

func TestBrowserScriptsWaitOnWhatTheyCheck(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.js"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no browser scripts found: %v", err)
	}
	sort.Strings(files)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(f)
		src := string(raw)
		var code []string
		for _, l := range strings.Split(src, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(l), "//") {
				code = append(code, l)
			}
		}
		body := strings.Join(code, "\n")
		// the form being saved leaves the page when its dialog closes or its row goes, so a script that looks for it
		// concludes "done" too early; the page's own mark (idle.js) covers the whole save
		if strings.Contains(body, "data-saving") {
			t.Errorf("%s reads data-saving to see whether a save is over; use require(\"./idle.js\")(pg)", name)
		}
		if !strings.Contains(src, "puppeteer") { // the jsdom scripts drive their own clock
			continue
		}
		if strings.Contains(body, "waitForTimeout") {
			t.Errorf("%s uses waitForTimeout, a fixed sleep; wait on what the page shows", name)
		}
		n := len(fixedWait.FindAllString(body, -1))
		switch want := fixedWaits[name]; {
		case n > want:
			t.Errorf("%s has %d fixed waits, %d are allowed: wait on something the page shows (idle.js after a save or post, waitForSelector or waitForFunction otherwise), not on time", name, n, want)
		case n < want:
			t.Errorf("%s is down to %d fixed waits: lower its number in fixedWaits (from %d)", name, n, want)
		}
	}
}

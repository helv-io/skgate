package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cleanEnv drops GIT_DIR and friends so the test never touches the repository it runs from.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	return env
}

func TestClassify(t *testing.T) {
	for in, want := range map[string]entry{
		"Fix the thing.":                     {"Fixed", "Fix the thing"},
		"Add a button":                       {"Added", "Add a button"},
		"Remove the checkbox":                {"Removed", "Remove the checkbox"},
		"Harden the fetcher against SSRF":    {"Security", "Harden the fetcher against SSRF"},
		"Real security schemes are fine now": {"Changed", "Real security schemes are fine now"},
		"A credential no longer leaks":       {"Security", "A credential no longer leaks"},
		"A tool is no longer listed twice":   {"Fixed", "A tool is no longer listed twice"},
		"Buttons start with a capital":       {"Changed", "Buttons start with a capital"},
		"Explain AI use in Google AI Studio": {"Changed", "Explain SI use in Google AI Studio"},
		"skgate says SI":                     {"Changed", "skgate says SI"},
	} {
		if got := classify(in); got != want {
			t.Errorf("%q: got %+v want %+v", in, got, want)
		}
	}
}

func TestSplitAndLinks(t *testing.T) {
	versions := []string{"0.2.0", "0.1.0"}
	secs := []string{render("v0.2.0", "2026-01-02", []entry{{"Added", "Add b"}}), render("v0.1.0", "2026-01-01", nil)}
	txt := assemble("- Hand-written note\n\n", secs, versions)
	for _, want := range []string{"## [Unreleased]\n\n- Hand-written note", "## [0.2.0] - 2026-01-02\n\n### Added\n\n- Add b\n", "### Changed\n\n- Maintenance only",
		"[Unreleased]: " + repoURL + "/compare/v0.2.0...HEAD\n", "[0.2.0]: " + repoURL + "/compare/v0.1.0...v0.2.0\n", "[0.1.0]: " + repoURL + "/releases/tag/v0.1.0\n"} {
		if !strings.Contains(txt, want) {
			t.Errorf("missing %q in\n%s", want, txt)
		}
	}
	un, gotSecs, gotVers := splitFile(txt)
	if un != "- Hand-written note\n\n" || len(gotSecs) != 2 || strings.Join(gotVers, ",") != "0.2.0,0.1.0" {
		t.Fatalf("split: %q %d %v", un, len(gotSecs), gotVers)
	}
	if assemble(un, gotSecs, gotVers) != txt {
		t.Error("splitting and assembling again changes the file")
	}
}

// A tag adds its section once; folded tags go into the next release; tests-only commits and version bumps stay out.
func TestTagModeInARepository(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(cleanEnv(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	commit := func(file, msg string) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, file)), 0o755)
		os.WriteFile(filepath.Join(dir, file), []byte(msg), 0o644)
		run("add", "-A")
		run("commit", "-q", "-m", msg)
	}
	run("init", "-q")
	commit("internal/a.go", "Add the first feature")
	run("tag", "v0.1.0")
	commit("internal/b.go", "Fix a crash in the second")
	commit("internal/b_test.go", "Test the second")
	commit("docs/x.md", "Docs only")
	commit("internal/c.go", "Hand-picked wording\n\nChangelog: Security: Close a hole")
	run("tag", "v0.1.1")
	commit("internal/d.go", "Add the third feature")
	commit("server.json", "Version 0.2.0")
	run("tag", "v0.2.0")
	os.WriteFile(filepath.Join(dir, "folded.txt"), []byte("v0.1.1 # never released\n"), 0o644)
	cl := filepath.Join(dir, "CHANGELOG.md")
	cwd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(cwd)
	for _, kv := range []string{"GIT_DIR", "GIT_WORK_TREE"} {
		if v, ok := os.LookupEnv(kv); ok {
			os.Unsetenv(kv)
			defer os.Setenv(kv, v)
		}
	}
	for _, tag := range []string{"v0.1.0", "v0.2.0", "v0.2.0"} {
		os.Args = []string{"changelog", "-tag", tag, "-file", cl, "-folded", filepath.Join(dir, "folded.txt")}
		resetFlags()
		main()
	}
	b, _ := os.ReadFile(cl)
	txt := string(b)
	if strings.Count(txt, "## [0.2.0]") != 1 || strings.Count(txt, "## [0.1.0]") != 1 {
		t.Fatalf("each version once:\n%s", txt)
	}
	for _, want := range []string{"- Add the first feature", "- Fix a crash in the second", "- Close a hole", "- Add the third feature", "compare/v0.1.0...v0.2.0"} {
		if !strings.Contains(txt, want) {
			t.Errorf("missing %q in\n%s", want, txt)
		}
	}
	for _, bad := range []string{"Test the second", "Docs only", "Version 0.2.0", "0.1.1"} {
		if strings.Contains(txt, bad) {
			t.Errorf("must not contain %q", bad)
		}
	}
	if strings.Index(txt, "## [0.2.0]") > strings.Index(txt, "## [0.1.0]") {
		t.Error("newest first")
	}
}

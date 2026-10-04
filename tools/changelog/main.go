// Command changelog writes CHANGELOG.md in the Keep a Changelog format (https://keepachangelog.com/en/1.1.0/).
//
//	go run ./tools/changelog -all [-notes releases.json]   write the whole file from the tags and their commits
//	go run ./tools/changelog -tag v1.2.3                   add the section of one tag to the file (nothing if it is there)
//
// A release is a version tag (vX.Y.Z). Its section lists the commits since the previous release, newest first, one
// line each, sorted into Added, Changed, Fixed, Security and Removed by the first word of the subject. A commit that
// touches only tests, docs, CI or tools is left out, and so are version bumps and merges. A commit can say what it
// wants in its message with a line "Changelog: <Section>: <text>" or "Changelog: skip". Tags listed in folded.txt
// were never released; their commits go into the next release. With -notes, a release's hand-written GitHub notes
// (the JSON of the releases API) are used instead of its commits when they exist. Text says SI where it would say AI,
// except in names (OpenAI, Google AI Studio).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

const repoURL = "https://github.com/helv-io/skgate"

var sections = []string{"Added", "Changed", "Fixed", "Security", "Removed"}

const preamble = `# Changelog

All notable changes to skgate are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This file is written by ` + "`tools/changelog`" + `: when a version tag is pushed, a workflow adds that version's section and commits it to master. See docs/development.md.

`

var semver = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

func git(args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "TZ=UTC", "LC_ALL=C")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "git %s: %v\n%s", strings.Join(args, " "), err, errb.String())
		os.Exit(1)
	}
	return strings.TrimRight(out.String(), "\n")
}

// releaseTags returns the version tags, oldest first, without the folded ones.
func releaseTags(folded map[string]bool) []string {
	var out []string
	for _, t := range strings.Fields(git("tag", "--list", "v*", "--sort=v:refname")) {
		if semver.MatchString(t) && !folded[t] {
			out = append(out, t)
		}
	}
	return out
}

type entry struct{ section, text string }

var (
	securityRE = regexp.MustCompile(`(?i)\b(vulnerab\w*|cve-\d+|go-\d{4}-\d+|harden\w*|ssrf|xss|csrf|injection|leak\w*|exfiltrat\w*|security (fix|issue|hole))\b`)
	fixedRE    = regexp.MustCompile(`^(Fix|Fixes|Fixed|Stop|Stops|Avoid|Prevent|Correct|Corrects|Restore|Restores)\b|\bno longer\b`)
	removedRE  = regexp.MustCompile(`^(Remove|Removes|Drop|Drops|Delete)\b`)
	addedRE    = regexp.MustCompile(`^(Add|Adds|New|Support|Supports|Introduce|Introduces)\b`)
	aiWord     = regexp.MustCompile(`\bAI\b`)
)

// say turns the word AI into SI in text of ours; names that contain it (Google AI Studio) stay.
func say(s string) string {
	const keep = "\x00"
	s = strings.ReplaceAll(s, "Google AI Studio", "Google"+keep+"Studio")
	s = aiWord.ReplaceAllString(s, "SI")
	return strings.ReplaceAll(s, "Google"+keep+"Studio", "Google AI Studio")
}

// classify sorts one line of text into a section and tidies it into a sentence without a final period.
func classify(text string) entry {
	text = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(text), "."))
	if text == "" {
		return entry{}
	}
	if !strings.HasPrefix(text, "skgate") {
		text = strings.ToUpper(text[:1]) + text[1:]
	}
	sec := "Changed"
	switch {
	case securityRE.MatchString(text):
		sec = "Security"
	case fixedRE.MatchString(text):
		sec = "Fixed"
	case removedRE.MatchString(text):
		sec = "Removed"
	case addedRE.MatchString(text):
		sec = "Added"
	}
	return entry{sec, say(text)}
}

var (
	skipSubject = regexp.MustCompile(`^(Version \d|Merge |Release |Gofmt$)`)
	trailerRE   = regexp.MustCompile(`(?m)^Changelog:\s*(.+)$`)
	// a file that only tests, documents, builds or tests the project
	quietFile = regexp.MustCompile(`(_test\.go$|/testdata/|\.md$|^docs/|^\.github/|^tools/|^AGENT\.md$|^\.gitignore$|^go\.(mod|sum)$|\.example$)`)
)

// commitEntries lists the entries of the commits in a range (or up to a tag when from is empty), newest first.
func commitEntries(from, to string) []entry {
	rng := to
	if from != "" {
		rng = from + ".." + to
	}
	raw := git("log", "--no-merges", "--format=%H%x1f%s%x1f%b%x1e", rng)
	var out []entry
	for _, rec := range strings.Split(raw, "\x1e") {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		f := strings.SplitN(rec, "\x1f", 3)
		if len(f) < 3 || skipSubject.MatchString(f[1]) {
			continue
		}
		if m := trailerRE.FindStringSubmatch(f[2]); m != nil {
			v := strings.TrimSpace(m[1])
			if strings.EqualFold(v, "skip") {
				continue
			}
			if sec, txt, ok := strings.Cut(v, ":"); ok {
				for _, s := range sections {
					if strings.EqualFold(strings.TrimSpace(sec), s) {
						out = append(out, entry{s, say(strings.TrimSpace(txt))})
						goto next
					}
				}
			}
		}
		if !touchesProduct(f[0]) {
			continue
		}
		if e := classify(f[1]); e.text != "" {
			out = append(out, e)
		}
	next:
	}
	return out
}

func touchesProduct(hash string) bool {
	for _, p := range strings.Split(git("diff-tree", "--root", "--no-commit-id", "--name-only", "-r", hash), "\n") {
		if p != "" && !quietFile.MatchString(p) {
			return true
		}
	}
	return false
}

// noteEntries reads the bullets of a hand-written release note.
func noteEntries(body string) []entry {
	var out []entry
	for _, l := range strings.Split(body, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "- ") && !strings.HasPrefix(l, "* ") {
			continue
		}
		l = strings.TrimSpace(l[2:])
		if e := classify(l); e.text != "" {
			out = append(out, e)
		}
	}
	return out
}

func date(tag string) string { return git("log", "-1", "--format=%cd", "--date=format:%Y-%m-%d", tag) }

func render(version, when string, es []entry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## [%s] - %s\n\n", strings.TrimPrefix(version, "v"), when)
	n := 0
	for _, s := range sections {
		var lines []string
		for _, e := range es {
			if e.section == s {
				lines = append(lines, "- "+e.text+"\n")
			}
		}
		if len(lines) == 0 {
			continue
		}
		n++
		fmt.Fprintf(&b, "### %s\n\n%s\n", s, strings.Join(lines, ""))
	}
	if n == 0 {
		b.WriteString("### Changed\n\n- Maintenance only\n\n")
	}
	return b.String()
}

type release struct{ version, body string }

var headRE = regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\] - .*$`)

// links writes the reference links under the sections, newest first.
func links(versions []string) string {
	var b strings.Builder
	if len(versions) > 0 {
		fmt.Fprintf(&b, "[Unreleased]: %s/compare/v%s...HEAD\n", repoURL, versions[0])
	} else {
		fmt.Fprintf(&b, "[Unreleased]: %s/commits/master\n", repoURL)
	}
	for i, v := range versions {
		if i+1 < len(versions) {
			fmt.Fprintf(&b, "[%s]: %s/compare/v%s...v%s\n", v, repoURL, versions[i+1], v)
		} else {
			fmt.Fprintf(&b, "[%s]: %s/releases/tag/v%s\n", v, repoURL, v)
		}
	}
	return b.String()
}

// assemble puts the file together from the Unreleased text and the sections (newest first).
func assemble(unreleased string, secs []string, versions []string) string {
	return preamble + "## [Unreleased]\n\n" + unreleased + strings.Join(secs, "") + links(versions)
}

// splitFile cuts an existing file into the Unreleased text and its version sections.
func splitFile(txt string) (unreleased string, secs []string, versions []string) {
	i := strings.Index(txt, "## [Unreleased]")
	if i < 0 {
		return "", nil, nil
	}
	rest := txt[i+len("## [Unreleased]"):]
	rest = strings.TrimLeft(rest, "\n")
	if j := strings.Index(rest, "\n[Unreleased]:"); j >= 0 { // drop the links, they are rewritten
		rest = rest[:j+1]
	} else if strings.HasPrefix(rest, "[Unreleased]:") {
		rest = ""
	}
	locs := headRE.FindAllStringSubmatchIndex(rest, -1)
	if len(locs) == 0 {
		return rest, nil, nil
	}
	unreleased = rest[:locs[0][0]]
	for k, l := range locs {
		end := len(rest)
		if k+1 < len(locs) {
			end = locs[k+1][0]
		}
		secs = append(secs, strings.TrimRight(rest[l[0]:end], "\n")+"\n\n")
		versions = append(versions, rest[l[2]:l[3]])
	}
	return unreleased, secs, versions
}

// resetFlags lets a test call main more than once.
func resetFlags() { flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError) }

func main() {
	all := flag.Bool("all", false, "write the whole file")
	tag := flag.String("tag", "", "add the section of this tag")
	notes := flag.String("notes", "", "GitHub releases JSON whose notes replace the commit lines of a release")
	file := flag.String("file", "CHANGELOG.md", "the changelog")
	foldedFile := flag.String("folded", "tools/changelog/folded.txt", "tags that were never released")
	flag.Parse()

	folded := map[string]bool{}
	if b, err := os.ReadFile(*foldedFile); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(strings.SplitN(l, "#", 2)[0]); l != "" {
				folded[l] = true
			}
		}
	}
	bodies := map[string]string{}
	if *notes != "" {
		b, err := os.ReadFile(*notes)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		var rs []struct{ Tag, Body string }
		if err := json.Unmarshal(b, &rs); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, r := range rs {
			bodies[r.Tag] = r.Body
		}
	}
	tags := releaseTags(folded)
	section := func(i int) string {
		prev := ""
		if i > 0 {
			prev = tags[i-1]
		}
		var es []entry
		if body, ok := bodies[tags[i]]; ok && len(noteEntries(body)) > 0 {
			es = noteEntries(body)
		} else {
			es = commitEntries(prev, tags[i])
		}
		return render(tags[i], date(tags[i]), es)
	}
	switch {
	case *all:
		var secs, versions []string
		for i := len(tags) - 1; i >= 0; i-- {
			secs = append(secs, section(i))
			versions = append(versions, strings.TrimPrefix(tags[i], "v"))
		}
		write(*file, assemble("", secs, versions))
	case *tag != "":
		i := -1
		for k, t := range tags {
			if t == *tag {
				i = k
			}
		}
		if i < 0 {
			fmt.Fprintf(os.Stderr, "%s is not a release tag\n", *tag)
			os.Exit(1)
		}
		old, _ := os.ReadFile(*file)
		unreleased, secs, versions := splitFile(string(old))
		for _, v := range versions {
			if v == strings.TrimPrefix(*tag, "v") {
				fmt.Println(*tag, "is already in", *file)
				return
			}
		}
		secs = append([]string{section(i)}, secs...)
		versions = append([]string{strings.TrimPrefix(*tag, "v")}, versions...)
		write(*file, assemble(unreleased, secs, versions))
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func write(path, txt string) {
	txt = strings.TrimRight(txt, "\n") + "\n"
	if err := os.WriteFile(path, []byte(txt), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

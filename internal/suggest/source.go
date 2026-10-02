// Package suggest proposes a managed MCP upstream configuration for a source (a git repository or a
// package) with the help of a language model. Everything the model returns is validated strictly; the
// result only pre-fills a form for review and is never saved here.
package suggest

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Kind is what a source is.
type Kind string

// Source kinds. KindPackage is a bare name that is looked up on npm, then PyPI.
const (
	KindGit         Kind = "git"
	KindNPM         Kind = "npm"
	KindPyPI        Kind = "pypi"
	KindPackage     Kind = "package"
	KindUnsupported Kind = "unsupported"
)

// Source is a parsed "MCP source URL / package" input.
type Source struct {
	Kind    Kind
	Raw     string
	Name    string // package name, or owner/repo for git
	Version string // version given in the input, if any
	// Git sources.
	Host, Path, Ref, Subdir string
	// Unsupported sources: the ecosystem and the tool the host would need.
	Ecosystem, Needs string
}

// CloneURL is the https address of a git source without any tree or branch part.
func (s Source) CloneURL() string { return "https://" + s.Host + "/" + s.Path }

var (
	npmNameRE  = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)
	pypiNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	versionRE  = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]{0,63}$`)
)

// ecosystems that skgate's image cannot run, by host or prefix.
type eco struct{ name, needs string }

var unsupportedHosts = map[string]eco{
	"crates.io": {"crates.io", "cargo"}, "docs.rs": {"crates.io", "cargo"}, "lib.rs": {"crates.io", "cargo"},
	"pkg.go.dev": {"Go modules", "go"}, "proxy.golang.org": {"Go modules", "go"},
	"hub.docker.com": {"Docker", "docker"}, "docker.io": {"Docker", "docker"}, "ghcr.io": {"Docker", "docker"}, "quay.io": {"Docker", "docker"},
	"nuget.org": {"NuGet", "dotnet"}, "www.nuget.org": {"NuGet", "dotnet"},
	"rubygems.org":     {"RubyGems", "gem"},
	"search.maven.org": {"Maven", "mvn"}, "mvnrepository.com": {"Maven", "mvn"}, "repo1.maven.org": {"Maven", "mvn"},
	"jsr.io": {"JSR", "deno"}, "deno.land": {"Deno", "deno"},
}

var unsupportedPrefix = map[string]eco{
	"cargo": {"crates.io", "cargo"}, "crate": {"crates.io", "cargo"}, "go": {"Go modules", "go"},
	"docker": {"Docker", "docker"}, "nuget": {"NuGet", "dotnet"}, "gem": {"RubyGems", "gem"},
	"maven": {"Maven", "mvn"}, "jsr": {"JSR", "deno"}, "deno": {"Deno", "deno"},
}

// ParseSource classifies the input. Accepted: https git URLs (GitHub, GitLab, Gitea, Bitbucket and other
// hosts, with optional /tree/<ref>/<dir>), git@host:owner/repo, host/owner/repo, npm and PyPI package names
// (name, name@1.2.3, name==1.2.3, npm:, pypi:, npmjs.com and pypi.org URLs), and the registries of
// ecosystems the image cannot run, which come back as KindUnsupported.
func ParseSource(in string) (Source, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return Source{}, errors.New("enter a git repository URL or a package name")
	}
	if len(in) > 500 || strings.ContainsAny(in, " \t\r\n\x00") {
		return Source{}, errors.New("the source must be one URL or package name without spaces")
	}
	s := Source{Raw: in}
	if m := regexp.MustCompile(`^git@([^:/]+):(.+)$`).FindStringSubmatch(in); m != nil {
		in = "https://" + m[1] + "/" + m[2]
	}
	if pre, rest, ok := strings.Cut(in, ":"); ok && !strings.HasPrefix(rest, "//") && len(pre) < 8 {
		switch pre {
		case "npm":
			return npmSource(s, rest)
		case "pypi", "uvx":
			return pypiSource(s, rest)
		}
		if e, ok := unsupportedPrefix[pre]; ok {
			s.Kind, s.Ecosystem, s.Needs, s.Name = KindUnsupported, e.name, e.needs, rest
			return s, nil
		}
	}
	if strings.HasPrefix(in, "http://") {
		return s, errors.New("use an https address")
	}
	if !strings.HasPrefix(in, "https://") {
		first, _, _ := strings.Cut(in, "/")
		if strings.Contains(in, "/") && strings.Contains(first, ".") && !strings.HasPrefix(in, "@") {
			in = "https://" + in // github.com/owner/repo
		}
	}
	if strings.HasPrefix(in, "https://") {
		return gitOrRegistry(s, in)
	}
	switch {
	case strings.HasPrefix(in, "@"), strings.Contains(in, "@"):
		return npmSource(s, in)
	case strings.Contains(in, "=="):
		return pypiSource(s, in)
	}
	if !npmNameRE.MatchString(in) && !pypiNameRE.MatchString(in) {
		return s, fmt.Errorf("%q is not a package name or an https URL", in)
	}
	s.Kind, s.Name = KindPackage, in
	return s, nil
}

func npmSource(s Source, spec string) (Source, error) {
	name, ver := spec, ""
	if i := strings.LastIndex(spec, "@"); i > 0 {
		name, ver = spec[:i], spec[i+1:]
	}
	if !npmNameRE.MatchString(strings.ToLower(name)) || (ver != "" && !versionRE.MatchString(ver)) {
		return s, fmt.Errorf("%q is not a valid npm package", spec)
	}
	s.Kind, s.Name, s.Version = KindNPM, name, ver
	return s, nil
}

func pypiSource(s Source, spec string) (Source, error) {
	name, ver := spec, ""
	if a, b, ok := strings.Cut(spec, "=="); ok {
		name, ver = a, b
	}
	if !pypiNameRE.MatchString(name) || (ver != "" && !versionRE.MatchString(ver)) {
		return s, fmt.Errorf("%q is not a valid PyPI package", spec)
	}
	s.Kind, s.Name, s.Version = KindPyPI, name, ver
	return s, nil
}

func gitOrRegistry(s Source, in string) (Source, error) {
	u, err := url.Parse(in)
	if err != nil || u.Host == "" {
		return s, errors.New("not a valid URL")
	}
	host := strings.ToLower(u.Host)
	segs := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(segs) == 1 && segs[0] == "" {
		segs = nil
	}
	switch host {
	case "www.npmjs.com", "npmjs.com", "registry.npmjs.org":
		if len(segs) >= 2 && segs[0] == "package" {
			segs = segs[1:]
		}
		if len(segs) > 0 && strings.HasPrefix(segs[0], "@") && len(segs) > 1 {
			return npmSource(s, strings.Join(segs[:2], "/"))
		}
		if len(segs) > 0 {
			return npmSource(s, segs[0])
		}
		return s, errors.New("no package in the npm URL")
	case "pypi.org":
		if len(segs) >= 2 && segs[0] == "project" {
			if len(segs) >= 3 {
				return pypiSource(s, segs[1]+"=="+segs[2])
			}
			return pypiSource(s, segs[1])
		}
		return s, errors.New("no package in the PyPI URL")
	}
	if e, ok := unsupportedHosts[host]; ok {
		s.Kind, s.Ecosystem, s.Needs, s.Name = KindUnsupported, e.name, e.needs, strings.Join(segs, "/")
		return s, nil
	}
	if len(segs) < 2 {
		return s, errors.New("a repository URL needs an owner and a repository name")
	}
	s.Kind, s.Host = KindGit, host
	path := segs
	marker := ""
	switch {
	case strings.Contains(host, "gitlab"):
		for i, p := range segs {
			if p == "-" {
				path, marker = segs[:i], "-"
				rest := segs[i+1:]
				if len(rest) >= 2 && (rest[0] == "tree" || rest[0] == "blob") {
					s.Ref, s.Subdir = rest[1], strings.Join(rest[2:], "/")
				}
				break
			}
		}
	default:
		path = segs[:2]
		rest := segs[2:]
		if len(rest) >= 2 && (rest[0] == "tree" || rest[0] == "blob" || rest[0] == "src") {
			rest = rest[1:]
			if host != "github.com" && host != "bitbucket.org" && len(rest) >= 2 && (rest[0] == "branch" || rest[0] == "tag" || rest[0] == "commit") {
				rest = rest[1:] // Gitea: /src/branch/<ref>
			}
			s.Ref, s.Subdir = rest[0], strings.Join(rest[1:], "/")
		}
	}
	_ = marker
	p := strings.TrimSuffix(strings.Join(path, "/"), ".git")
	if p == "" || strings.Count(p, "/") < 1 {
		return s, errors.New("a repository URL needs an owner and a repository name")
	}
	s.Path, s.Name = p, p
	if d, err := url.PathUnescape(s.Subdir); err == nil {
		s.Subdir = strings.Trim(d, "/")
	}
	return s, nil
}

// UnsupportedMessage explains why an unsupported source cannot be run here and what can.
func (s Source) UnsupportedMessage(runners []string) string {
	have := "no package runner is available"
	if len(runners) > 0 {
		have = "available runners: " + strings.Join(runners, ", ")
	}
	return fmt.Sprintf("%s sources need %s, which this image does not include (%s). Use an npm or PyPI package, or a git repository that builds with the available runners.",
		s.Ecosystem, s.Needs, have)
}

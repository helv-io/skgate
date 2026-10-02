package managed

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Package is the package a runner command (npx, uvx, ...) fetches and runs.
type Package struct {
	Runner  string // npx, bunx, pnpm dlx, uvx, uv tool run, pipx run
	Spec    string // as written, e.g. @scope/pkg@1.2.3 or pkg==1.2.3
	Name    string
	Version string // exact version when pinned, else the tag or range as written ("" when none)
	Pinned  bool   // an exact version: never auto-updated
}

var exactVersionRE = regexp.MustCompile(`^v?\d+(\.\d+){0,3}([-+][0-9A-Za-z.+-]+)?$`)

// runnerFlagsWithValue lists the options of each runner that consume the next argument.
var runnerFlagsWithValue = map[string]map[string]bool{
	"npx":    {"-p": true, "--package": true, "-c": true, "--call": true, "--cache": true, "--registry": true, "--prefix": true, "--userconfig": true, "-w": true, "--workspace": true},
	"uvx":    {"--from": true, "--with": true, "--with-requirements": true, "--with-editable": true, "-p": true, "--python": true, "--index": true, "--index-url": true, "--extra-index-url": true, "--default-index": true, "-i": true, "--directory": true, "--project": true, "--config-file": true, "--env-file": true, "-c": true, "--constraints": true, "--overrides": true, "--refresh-package": true, "-P": true, "--upgrade-package": true},
	"pnpm":   {"--package": true, "-p": true},
	"bunx":   {"-p": true, "--package": true},
	"pipx":   {"--spec": true, "--python": true, "--index-url": true, "-i": true},
	"uvtool": {"--from": true, "--with": true, "-p": true, "--python": true, "--index": true, "--index-url": true},
}

// PackageOf recognizes a package runner command line and returns the package it runs. Shell mode
// and unknown commands are not recognized (ok false).
func PackageOf(s Spec) (Package, bool) {
	if s.Shell {
		return Package{}, false
	}
	base := strings.TrimSuffix(filepath.Base(s.Command), ".cmd")
	args := s.Args
	var runner, kind string
	switch base {
	case "npx":
		runner, kind = "npx", "npx"
	case "bunx":
		runner, kind = "bunx", "bunx"
	case "uvx":
		runner, kind = "uvx", "uvx"
	case "pnpm":
		if len(args) > 0 && args[0] == "dlx" {
			runner, kind, args = "pnpm dlx", "pnpm", args[1:]
		}
	case "uv":
		if len(args) > 1 && args[0] == "tool" && args[1] == "run" {
			runner, kind, args = "uv tool run", "uvtool", args[2:]
		}
	case "pipx":
		if len(args) > 0 && args[0] == "run" {
			runner, kind, args = "pipx run", "pipx", args[1:]
		}
	}
	if runner == "" {
		return Package{}, false
	}
	withValue := runnerFlagsWithValue[kind]
	explicit := "" // --package / --from / --spec
	positional := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			if i+1 < len(args) && positional == "" {
				positional = args[i+1]
			}
			break
		}
		if strings.HasPrefix(a, "-") {
			name, val, hasVal := strings.Cut(a, "=")
			pkgFlag := name == "--package" || name == "--from" || name == "--spec" ||
				name == "-p" && (kind == "npx" || kind == "bunx" || kind == "pnpm") // -p is the Python version for uv
			if pkgFlag {
				if !hasVal && i+1 < len(args) {
					val, i = args[i+1], i+1
				}
				if explicit == "" {
					explicit = val
				}
				continue
			}
			if !hasVal && withValue[name] {
				i++
			}
			continue
		}
		if positional == "" {
			positional = a
			break
		}
	}
	spec := explicit
	if spec == "" {
		spec = positional
	}
	if spec == "" {
		return Package{}, false
	}
	p := Package{Runner: runner, Spec: spec}
	p.Name, p.Version, p.Pinned = splitPackageSpec(spec)
	return p, true
}

// splitPackageSpec splits name@version (npm, uvx) and name==version (pip) and reports whether the
// version is an exact one. Tags such as latest, ranges and specifiers with operators are not pins.
func splitPackageSpec(spec string) (name, version string, pinned bool) {
	spec = strings.TrimSpace(spec)
	if i := strings.Index(spec, "=="); i > 0 {
		name, version = spec[:i], strings.TrimSpace(spec[i+2:])
		return name, version, exactVersionRE.MatchString(version)
	}
	if i := strings.IndexAny(spec, "<>!~"); i > 0 {
		return spec[:i], spec[i:], false
	}
	at := strings.LastIndex(spec, "@")
	if at <= 0 { // no version, or only the scope's "@"
		return spec, "", false
	}
	name, version = spec[:at], spec[at+1:]
	return name, version, exactVersionRE.MatchString(version)
}

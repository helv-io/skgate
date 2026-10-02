// Package managed runs MCP servers as child processes of skgate ("managed upstreams") and
// bridges their stdio JSON-RPC stream to many concurrent HTTP clients.
package managed

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Lifecycle modes.
const (
	OnDemand = "on-demand" // start on the first request, stop after IdleTimeout without requests
	Always   = "always"    // start at boot, restart after a crash
)

// Defaults for a Spec.
const (
	DefaultStartupTimeout = 60 * time.Second
	DefaultIdleTimeout    = 10 * time.Minute
	MinAutoUpdate         = 5 * time.Minute
)

// KV is one name/value pair (environment variable or HTTP header).
type KV struct{ Name, Value string }

// GitSpec is the source repository of a git upstream.
type GitSpec struct {
	URL   string
	Ref   string // branch or tag; empty means the remote's default branch
	Token string // optional, for private repositories (never placed in argv)
}

// Spec describes how to run one managed upstream.
type Spec struct {
	Alias          string
	Command        string
	Args           []string
	Env            []KV
	Shell          bool   // run Command through /bin/sh -c (Args become $1..)
	WorkDir        string // absolute; empty means the per-alias default (repo dir for git)
	Install        string // shell command run before start when its inputs changed
	StartupTimeout time.Duration
	Lifecycle      string
	IdleTimeout    time.Duration
	Git            *GitSpec
	AutoUpdate     time.Duration // 0 = off; how often to check for and apply updates
}

var (
	envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	refRE     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@+-]{0,199}$`)
)

// Limits on user supplied lists.
const (
	MaxArgs     = 200
	MaxEnv      = 64
	MaxValueLen = 8192
)

// ValidateEnvName reports whether name is a usable environment variable name.
func ValidateEnvName(name string) error {
	if !envNameRE.MatchString(name) {
		return fmt.Errorf("env name %q must match [A-Za-z_][A-Za-z0-9_]*", name)
	}
	return nil
}

func hasCtl(s string, allowNL bool) bool {
	for _, c := range s {
		if c == 0 || (c < 0x20 && !(allowNL && (c == '\n' || c == '\t'))) || c == 0x7f {
			return true
		}
	}
	return false
}

// Validate checks the spec. Nothing is executed and no path is touched.
func (s Spec) Validate() error {
	if strings.TrimSpace(s.Command) == "" {
		return errors.New("command is required")
	}
	if hasCtl(s.Command, s.Shell) {
		return errors.New("command has control characters")
	}
	if len(s.Command) > MaxValueLen {
		return errors.New("command is too long")
	}
	if len(s.Args) > MaxArgs {
		return fmt.Errorf("at most %d args", MaxArgs)
	}
	for _, a := range s.Args {
		if hasCtl(a, true) || len(a) > MaxValueLen {
			return errors.New("an arg has control characters or is too long")
		}
	}
	if len(s.Env) > MaxEnv {
		return fmt.Errorf("at most %d env vars", MaxEnv)
	}
	seen := map[string]bool{}
	for _, kv := range s.Env {
		if err := ValidateEnvName(kv.Name); err != nil {
			return err
		}
		if seen[kv.Name] {
			return fmt.Errorf("env %s is set twice", kv.Name)
		}
		seen[kv.Name] = true
		if strings.ContainsRune(kv.Value, 0) || len(kv.Value) > MaxValueLen {
			return fmt.Errorf("env %s has an invalid value", kv.Name)
		}
	}
	if s.WorkDir != "" && (!filepath.IsAbs(s.WorkDir) || hasCtl(s.WorkDir, false)) {
		return errors.New("working dir must be an absolute path")
	}
	if hasCtl(s.Install, true) || len(s.Install) > MaxValueLen {
		return errors.New("install command has control characters or is too long")
	}
	if s.StartupTimeout < 0 || s.StartupTimeout > time.Hour || s.IdleTimeout < 0 || s.IdleTimeout > 30*24*time.Hour {
		return errors.New("timeout out of range")
	}
	switch s.Lifecycle {
	case "", OnDemand, Always:
	default:
		return errors.New("lifecycle must be on-demand or always")
	}
	if s.AutoUpdate < 0 || s.AutoUpdate > 0 && s.AutoUpdate < MinAutoUpdate || s.AutoUpdate > 366*24*time.Hour {
		return fmt.Errorf("auto-update interval must be 0 (off) or between %s and 366 days", MinAutoUpdate)
	}
	if s.Git != nil {
		return s.Git.Validate()
	}
	return nil
}

// Validate checks the repository settings.
func (g GitSpec) Validate() error {
	u, err := url.Parse(strings.TrimSpace(g.URL))
	if err != nil || g.URL == "" {
		return errors.New("repository URL is required")
	}
	switch u.Scheme {
	case "https", "http":
		if u.Host == "" {
			return errors.New("repository URL has no host")
		}
	case "file":
		if u.Path == "" {
			return errors.New("repository URL has no path")
		}
	default:
		return errors.New("repository URL must be https (or http); ssh is not supported")
	}
	if u.User != nil {
		return errors.New("no credentials in the repository URL: use the token field")
	}
	if hasCtl(g.URL, false) || strings.ContainsAny(g.URL, " \\") {
		return errors.New("repository URL has invalid characters")
	}
	if g.Ref != "" && (!refRE.MatchString(g.Ref) || strings.Contains(g.Ref, "..")) {
		return errors.New("ref must be a branch or tag name")
	}
	if hasCtl(g.Token, false) || len(g.Token) > 512 {
		return errors.New("token has invalid characters")
	}
	return nil
}

// runFingerprint changes when the running process must be replaced.
func (s Spec) runFingerprint() string {
	var b strings.Builder
	w := func(v string) { b.WriteString(v); b.WriteByte(0) }
	w(s.Command)
	for _, a := range s.Args {
		w(a)
	}
	w("|")
	for _, kv := range s.Env {
		w(kv.Name)
		w(kv.Value)
	}
	w(fmt.Sprint(s.Shell))
	w(s.WorkDir)
	w(s.Install)
	if s.Git != nil {
		w(s.Git.URL)
		w(s.Git.Ref)
	}
	return b.String()
}

func (s Spec) startupTimeout() time.Duration {
	if s.StartupTimeout > 0 {
		return s.StartupTimeout
	}
	return DefaultStartupTimeout
}

func (s Spec) idleTimeout() time.Duration {
	if s.IdleTimeout > 0 {
		return s.IdleTimeout
	}
	return DefaultIdleTimeout
}

func (s Spec) lifecycle() string {
	if s.Lifecycle == Always {
		return Always
	}
	return OnDemand
}

// installStamp identifies the inputs of the install step; when it changes the step reruns.
func (s Spec) installStamp(rev string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00", s.Install, rev, s.WorkDir)
	for _, kv := range s.Env {
		fmt.Fprintf(h, "%s=%s\x00", kv.Name, kv.Value)
	}
	return hex.EncodeToString(h.Sum(nil))
}

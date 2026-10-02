package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/managed"
)

// Upstream kinds.
const (
	KindRemote = "remote" // an MCP server reached over HTTP
	KindStdio  = "stdio"  // a command skgate runs and talks to over stdin/stdout
	KindGit    = "git"    // like stdio, from a checkout of a git repository
)

// KV is a name/value pair: an environment variable of a managed process or an outbound header.
type KV struct{ Name, Value string }

func toManaged(l []KV) []managed.KV {
	if len(l) == 0 {
		return nil
	}
	out := make([]managed.KV, len(l))
	for i, kv := range l {
		out[i] = managed.KV{Name: kv.Name, Value: kv.Value}
	}
	return out
}

// Managed reports whether skgate runs this upstream itself.
func (u Upstream) Managed() bool { return u.Kind == KindStdio || u.Kind == KindGit }

// KindOrRemote is the kind with the empty value read as remote.
func (u Upstream) KindOrRemote() string {
	if u.Kind == "" {
		return KindRemote
	}
	return u.Kind
}

// Spec converts a managed upstream into the process specification.
func (u Upstream) Spec() managed.Spec {
	s := managed.Spec{Alias: u.Alias, Command: u.Command, Args: u.Args, Env: toManaged(u.Env), Shell: u.Shell, WorkDir: u.WorkDir,
		Install: u.Install, StartupTimeout: time.Duration(u.StartupSecs) * time.Second, Lifecycle: u.Lifecycle,
		IdleTimeout: time.Duration(u.IdleSecs) * time.Second, AutoUpdate: time.Duration(u.AutoUpdateSecs) * time.Second}
	if u.Kind == KindGit {
		s.Git = &managed.GitSpec{URL: u.GitURL, Ref: u.GitRef, Token: u.GitToken}
	}
	return s
}

// AutoUpdateMins is the auto-update interval in minutes (0 = off).
func (u Upstream) AutoUpdateMins() int { return u.AutoUpdateSecs / 60 }

// AutoUpdatePreset reports whether the interval is one the form offers (off, or a listed choice).
func (u Upstream) AutoUpdatePreset() bool {
	switch u.AutoUpdateSecs {
	case 0, 900, 3600, 21600, 86400, 604800:
		return true
	}
	return false
}

// validateHeaders checks custom outbound headers.
func validateHeaders(h []KV) error {
	if len(h) > 32 {
		return errors.New("at most 32 custom headers")
	}
	seen := map[string]bool{}
	for _, kv := range h {
		if !validHeaderName(kv.Name) {
			return fmt.Errorf("header name %q is not valid", kv.Name)
		}
		key := strings.ToLower(kv.Name)
		if seen[key] {
			return fmt.Errorf("header %s is set twice", kv.Name)
		}
		seen[key] = true
		if strings.ContainsAny(kv.Value, "\r\n\x00") || len(kv.Value) > 8192 {
			return fmt.Errorf("header %s has an invalid value", kv.Name)
		}
	}
	return nil
}

// validateManaged checks the fields of a managed upstream.
func (u *Upstream) validateManaged() error {
	if u.URL != "" || u.AuthKind != "" && u.AuthKind != AuthNone || u.HostOverride != "" || len(u.Headers) > 0 {
		return errors.New("a managed upstream has no URL, auth, host override or headers")
	}
	if u.Kind == KindStdio && (u.GitURL != "" || u.GitRef != "" || u.GitToken != "") {
		return errors.New("repository settings belong to the git kind")
	}
	if u.Kind == KindGit && u.WorkDir != "" {
		return errors.New("a git upstream runs in its checkout; leave the working dir empty")
	}
	if u.AutoUpdateSecs < 0 || u.AutoUpdateSecs > 0 && time.Duration(u.AutoUpdateSecs)*time.Second < managed.MinAutoUpdate {
		return fmt.Errorf("auto-update interval must be off or at least %d minutes", int(managed.MinAutoUpdate/time.Minute))
	}
	if u.StartupSecs < 0 || u.IdleSecs < 0 {
		return errors.New("timeouts cannot be negative")
	}
	if !aliasRE.MatchString(u.Alias) {
		return errors.New("alias must be 1-63 chars of a-z, 0-9 and dash")
	}
	u.AuthKind = AuthNone
	if u.Lifecycle == "" {
		u.Lifecycle = managed.OnDemand
	}
	return u.Spec().Validate()
}

// encodeList stores a list as a sealed JSON value ("" when empty).
func (s *Upstreams) encodeKV(l []KV) string {
	if len(l) == 0 {
		return ""
	}
	type pair struct{ N, V string }
	p := make([]pair, len(l))
	for i, kv := range l {
		p[i] = pair{kv.Name, kv.Value}
	}
	b, _ := json.Marshal(p)
	return s.db.Secrets.Seal(string(b))
}

// decodeKV reads a sealed JSON list. ok is false when it could not be decrypted or parsed.
func (s *Upstreams) decodeKV(v string) (l []KV, ok bool) {
	if v == "" {
		return nil, true
	}
	plain, err := s.db.Secrets.Open(v)
	if err != nil {
		return nil, false
	}
	var p []struct{ N, V string }
	if json.Unmarshal([]byte(plain), &p) != nil {
		return nil, false
	}
	for _, x := range p {
		l = append(l, KV{Name: x.N, Value: x.V})
	}
	return l, true
}

func encodeArgs(a []string) string {
	if len(a) == 0 {
		return ""
	}
	b, _ := json.Marshal(a)
	return string(b)
}

func decodeArgs(v string) []string {
	if v == "" {
		return nil
	}
	var a []string
	if json.Unmarshal([]byte(v), &a) != nil {
		return nil
	}
	return a
}

// MergeSecrets fills secrets the edit form left blank from the stored upstream: an env var or
// header keeps its stored value when the submitted value is empty or still the masked display and
// a stored one of the same name exists; the git token is kept unless clearToken is set. A stored
// working dir or idle timeout (set before those fields were removed from the UI) is kept silently. Nothing carries
// over when the kind changed.
func MergeSecrets(old, u Upstream, clearToken bool) Upstream {
	if old.KindOrRemote() != u.KindOrRemote() {
		return u
	}
	keep := func(prev, cur []KV) []KV {
		byName := map[string]string{}
		for _, kv := range prev {
			byName[kv.Name] = kv.Value
		}
		out := make([]KV, len(cur))
		for i, kv := range cur {
			// The edit form shows values masked; a value left empty or unchanged (still the mask)
			// keeps the stored one.
			if old, ok := byName[kv.Name]; ok && (kv.Value == "" || kv.Value == httputil.Mask(old)) {
				kv.Value = old
			}
			out[i] = kv
		}
		return out
	}
	u.Env = keep(old.Env, u.Env)
	u.Headers = keep(old.Headers, u.Headers)
	u.WorkDir, u.IdleSecs = old.WorkDir, old.IdleSecs
	if u.GitToken == "" && !clearToken {
		u.GitToken = old.GitToken
	}
	return u
}

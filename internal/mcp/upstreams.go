package mcp

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/helv-io/skgate/internal/store"
)

// Outbound auth kinds for an upstream.
const (
	AuthAuto        = "auto" // probe the upstream and use the first method that works (detected_kind)
	AuthNone        = "none"
	AuthBearer      = "bearer"
	AuthHeader      = "header"
	AuthPassthrough = "passthrough"
)

var aliasRE = regexp.MustCompile(`^[a-z0-9-]{1,63}$`)

// Upstream is an MCP server skgate proxies to.
type Upstream struct {
	Alias     string
	URL       string
	AuthKind  string // auto, none, bearer, header or passthrough
	AuthName  string // header name for AuthHeader
	AuthValue string // token or header value (never rendered; sealed at rest)
	// SecretErr is set when a stored secret could not be decrypted (wrong SECRETS_KEY or key file).
	SecretErr bool
	// HostOverride, when set, is sent as the outbound Host (hostname[:port]) instead of the URL's host.
	HostOverride string
	Enabled      bool
	// IncludeInMCP merges this upstream into the aggregated bare /mcp endpoint (any number may be included).
	IncludeInMCP bool
	CreatedAt    time.Time
	// DetectedKind is the outcome of auth auto-detection (only meaningful when AuthKind is auto):
	// none, bearer, header, "oauth (unsupported)" or "failed". Empty means not detected yet.
	DetectedKind string
	// DetectedNote is a short human explanation of the detection result (never a secret).
	DetectedNote string
	// Kind is remote (default), stdio or git. The fields below apply to remote (Headers) or to the
	// managed kinds; see kinds.go.
	Kind string
	// Headers are extra outbound headers for a remote upstream (values sealed at rest, masked in the UI).
	Headers []KV
	// Managed process settings (Env values are sealed at rest and masked in the UI).
	Command        string
	Args           []string
	Env            []KV
	Shell          bool   // run Command through /bin/sh -c
	WorkDir        string // absolute; empty means the per-alias directory
	Install        string // shell command run before start when its inputs changed or on Update
	StartupSecs    int    // 0 = default
	IdleSecs       int    // on-demand idle stop, 0 = default
	AutoUpdateSecs int    // auto-update interval, 0 = off
	Lifecycle      string // on-demand (default) or always
	// Git settings (kind git). GitToken is sealed at rest and never rendered.
	GitURL, GitRef, GitToken string
}

// ToggleSlash adds a trailing slash to the path of rawURL when it has none, and removes it when it
// has one (query and fragment are kept).
func ToggleSlash(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if strings.HasSuffix(u.Path, "/") {
		u.Path = strings.TrimRight(u.Path, "/")
	} else {
		u.Path += "/"
	}
	u.RawPath = ""
	return u.String()
}

// Detection results stored in detected_kind besides none, bearer and header.
const (
	DetectedOAuth  = "oauth (unsupported)"
	DetectedFailed = "failed"
)

// EffectiveKind is the outbound auth kind used at runtime: the stored kind, or for auto the
// detected one. An auto upstream whose detection failed or found only OAuth sends no credentials.
func (u Upstream) EffectiveKind() string {
	if u.AuthKind != AuthAuto {
		return u.AuthKind
	}
	switch u.DetectedKind {
	case AuthBearer, AuthHeader:
		return u.DetectedKind
	}
	return AuthNone
}

// HasSecret reports whether a secret value is stored.
func (u Upstream) HasSecret() bool { return u.AuthValue != "" }

// Validate checks the upstream fields.
func (u *Upstream) Validate() error {
	if u.Lifecycle == "" {
		u.Lifecycle = "on-demand"
	}
	switch u.Kind {
	case "", KindRemote:
		u.Kind = KindRemote
	case KindStdio, KindGit:
		return u.validateManaged()
	default:
		return errors.New("kind must be remote, stdio or git")
	}
	if !aliasRE.MatchString(u.Alias) {
		return errors.New("alias must be 1-63 chars of a-z, 0-9 and dash")
	}
	if u.Command != "" || len(u.Args) > 0 || len(u.Env) > 0 || u.GitURL != "" || u.Install != "" || u.WorkDir != "" {
		return errors.New("process settings belong to the stdio and git kinds")
	}
	if err := validateHeaders(u.Headers); err != nil {
		return err
	}
	pu, err := url.Parse(u.URL)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
		return errors.New("url must be an absolute http(s) URL")
	}
	if !validHostOverride(u.HostOverride) {
		return errors.New("host override must be a hostname[:port] with no spaces or slashes")
	}
	switch u.AuthKind {
	case AuthNone, AuthPassthrough, AuthAuto:
	case AuthBearer:
		if u.AuthValue == "" {
			return errors.New("bearer auth needs a token")
		}
	case AuthHeader:
		if u.AuthValue == "" || !validHeaderName(u.AuthName) {
			return errors.New("header auth needs a valid header name and a value")
		}
	default:
		return errors.New("auth kind must be auto, none, bearer, header or passthrough")
	}
	if u.AuthKind == AuthAuto && u.AuthName != "" && !validHeaderName(u.AuthName) {
		return errors.New("header name is not valid")
	}
	return nil
}

var hostOverrideRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?(:[0-9]{1,5})?$`)

// validHostOverride accepts an empty value or hostname[:port].
func validHostOverride(h string) bool {
	if h == "" {
		return true
	}
	if !hostOverrideRE.MatchString(h) {
		return false
	}
	if i := strings.IndexByte(h, ':'); i >= 0 {
		if p, err := strconv.Atoi(h[i+1:]); err != nil || p < 1 || p > 65535 {
			return false
		}
	}
	return true
}

func validHeaderName(n string) bool {
	if n == "" || len(n) > 100 {
		return false
	}
	for _, c := range n {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	switch http.CanonicalHeaderKey(n) {
	case "Host", "Content-Length", "Connection", "Transfer-Encoding", "Upgrade", "Te", "Trailer",
		"Content-Type", "Accept", "Mcp-Session-Id", "Mcp-Protocol-Version":
		return false
	}
	return true
}

// Upstreams is the SQLite-backed registry.
type Upstreams struct{ db *store.DB }

// NewUpstreams returns the registry.
func NewUpstreams(db *store.DB) *Upstreams {
	s := &Upstreams{db: db}
	_ = s.NormalizeURLs() // best effort; retried on the next start
	return s
}

const upCols = `alias,url,auth_kind,auth_name,auth_value,enabled,include_in_mcp,created_at,host_override,detected_kind,detected_note,` +
	`kind,headers,command,args,env,shell,workdir,install_cmd,startup_secs,idle_secs,lifecycle,git_url,git_ref,git_token,auto_update_secs`

func (s *Upstreams) scan(sc interface{ Scan(...any) error }) (Upstream, error) {
	var u Upstream
	var en, inc, ts, sh int64
	var headers, env, args, gtok string
	if err := sc.Scan(&u.Alias, &u.URL, &u.AuthKind, &u.AuthName, &u.AuthValue, &en, &inc, &ts, &u.HostOverride, &u.DetectedKind, &u.DetectedNote,
		&u.Kind, &headers, &u.Command, &args, &env, &sh, &u.WorkDir, &u.Install, &u.StartupSecs, &u.IdleSecs, &u.Lifecycle, &u.GitURL, &u.GitRef, &gtok, &u.AutoUpdateSecs); err != nil {
		return u, err
	}
	u.Enabled, u.IncludeInMCP, u.CreatedAt, u.Shell = en != 0, inc != 0, time.Unix(ts, 0), sh != 0
	u.Args = decodeArgs(args)
	if v, err := s.db.Secrets.Open(u.AuthValue); err != nil {
		u.AuthValue, u.SecretErr = "", true
	} else {
		u.AuthValue = v
	}
	var ok1, ok2 bool
	u.Headers, ok1 = s.decodeKV(headers)
	u.Env, ok2 = s.decodeKV(env)
	if !ok1 || !ok2 {
		u.SecretErr = true
	}
	if v, err := s.db.Secrets.Open(gtok); err != nil {
		u.SecretErr = true
	} else {
		u.GitToken = v
	}
	return u, nil
}

// Get returns an upstream by alias.
func (s *Upstreams) Get(alias string) (Upstream, bool) {
	u, err := s.scan(s.db.QueryRow(`SELECT `+upCols+` FROM upstreams WHERE alias=?`, alias))
	return u, err == nil
}

// Included returns the enabled upstreams flagged "Include in /mcp", ordered by alias.
func (s *Upstreams) Included() ([]Upstream, error) {
	rows, err := s.db.Query(`SELECT ` + upCols + ` FROM upstreams WHERE include_in_mcp=1 AND enabled=1 ORDER BY alias`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Upstream
	for rows.Next() {
		u, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// List returns all upstreams ordered by alias.
func (s *Upstreams) List() ([]Upstream, error) {
	rows, err := s.db.Query(`SELECT ` + upCols + ` FROM upstreams ORDER BY alias`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Upstream
	for rows.Next() {
		u, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Create inserts a new upstream.
func (s *Upstreams) Create(u Upstream) error {
	if err := u.Validate(); err != nil {
		return err
	}
	if _, ok := s.Get(u.Alias); ok {
		return fmt.Errorf("alias %q already exists", u.Alias)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO upstreams(`+upCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		u.Alias, u.URL, u.AuthKind, u.AuthName, s.db.Secrets.Seal(u.AuthValue), b2i(u.Enabled), b2i(u.IncludeInMCP), time.Now().Unix(),
		u.HostOverride, u.DetectedKind, u.DetectedNote,
		u.Kind, s.encodeKV(u.Headers), u.Command, encodeArgs(u.Args), s.encodeKV(u.Env), b2i(u.Shell), u.WorkDir, u.Install,
		u.StartupSecs, u.IdleSecs, u.Lifecycle, u.GitURL, u.GitRef, s.db.Secrets.Seal(u.GitToken), u.AutoUpdateSecs)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Update replaces an upstream. When keepSecret is true and AuthValue is empty, the stored secret is kept
// (only if the auth kind is unchanged, so a secret is never silently reused for another kind).
func (s *Upstreams) Update(u Upstream, keepSecret bool) error {
	old, ok := s.Get(u.Alias)
	if !ok {
		return sql.ErrNoRows
	}
	if keepSecret && u.AuthValue == "" && old.AuthKind == u.AuthKind {
		u.AuthValue = old.AuthValue
	}
	if err := u.Validate(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Anything that can change what auto-detection would answer invalidates the stored result.
	if u.AuthKind == AuthAuto && old.AuthKind == AuthAuto && old.URL == u.URL && old.AuthName == u.AuthName &&
		old.AuthValue == u.AuthValue && old.HostOverride == u.HostOverride && sameKV(old.Headers, u.Headers) {
		u.DetectedKind, u.DetectedNote = old.DetectedKind, old.DetectedNote
	} else {
		u.DetectedKind, u.DetectedNote = "", ""
	}
	_, err = tx.Exec(`UPDATE upstreams SET url=?,auth_kind=?,auth_name=?,auth_value=?,enabled=?,include_in_mcp=?,host_override=?,detected_kind=?,detected_note=?,`+
		`kind=?,headers=?,command=?,args=?,env=?,shell=?,workdir=?,install_cmd=?,startup_secs=?,idle_secs=?,lifecycle=?,git_url=?,git_ref=?,git_token=?,auto_update_secs=? WHERE alias=?`,
		u.URL, u.AuthKind, u.AuthName, s.db.Secrets.Seal(u.AuthValue), b2i(u.Enabled), b2i(u.IncludeInMCP), u.HostOverride, u.DetectedKind, u.DetectedNote,
		u.Kind, s.encodeKV(u.Headers), u.Command, encodeArgs(u.Args), s.encodeKV(u.Env), b2i(u.Shell), u.WorkDir, u.Install,
		u.StartupSecs, u.IdleSecs, u.Lifecycle, u.GitURL, u.GitRef, s.db.Secrets.Seal(u.GitToken), u.AutoUpdateSecs, u.Alias)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Flag names accepted by SetFlag.
const (
	FlagEnabled = "enabled"
	FlagInclude = "include"
)

// SetFlag flips one boolean of an upstream (enabled, or include-in-/mcp) without touching any
// other field. It returns the upstream as stored afterwards.
func (s *Upstreams) SetFlag(alias, flag string, on bool) (Upstream, error) {
	col := ""
	switch flag {
	case FlagEnabled:
		col = "enabled"
	case FlagInclude:
		col = "include_in_mcp"
	default:
		return Upstream{}, errors.New("unknown flag")
	}
	res, err := s.db.Exec(`UPDATE upstreams SET `+col+`=? WHERE alias=?`, b2i(on), alias)
	if err != nil {
		return Upstream{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Upstream{}, errors.New("unknown alias")
	}
	u, _ := s.Get(alias)
	return u, nil
}

// SetDetected stores the auto-detection result for an upstream.
func (s *Upstreams) SetDetected(alias, kind, note string) error {
	_, err := s.db.Exec(`UPDATE upstreams SET detected_kind=?,detected_note=? WHERE alias=?`, kind, note, alias)
	return err
}

// SetWorkingURL replaces the stored URL of an upstream with the spelling that was found to work
// (the trailing slash added or removed). It only applies while the stored URL is still oldURL, so a
// concurrent edit is never overwritten. There is no separate, visible state: the URL is the truth.
func (s *Upstreams) SetWorkingURL(alias, oldURL, newURL string) (bool, error) {
	res, err := s.db.Exec(`UPDATE upstreams SET url=? WHERE alias=? AND url=? AND kind IN ('', 'remote')`, newURL, alias, oldURL)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// NormalizeURLs folds the trailing-slash spelling learned by earlier releases (url_variant) into the
// url column and clears it. It is idempotent: rows without a learned variant are untouched.
func (s *Upstreams) NormalizeURLs() error {
	rows, err := s.db.Query(`SELECT alias,url,url_variant FROM upstreams WHERE url_variant<>''`)
	if err != nil {
		return err
	}
	type row struct{ alias, url, variant string }
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.alias, &r.url, &r.variant); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, r)
	}
	rows.Close()
	for _, r := range todo {
		u := r.url
		if r.variant == "toggled" {
			u = ToggleSlash(r.url)
		}
		if _, err := s.db.Exec(`UPDATE upstreams SET url=?,url_variant='' WHERE alias=?`, u, r.alias); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes an upstream.
func (s *Upstreams) Delete(alias string) error {
	_, err := s.db.Exec(`DELETE FROM upstreams WHERE alias=?`, alias)
	return err
}

// applyOutbound sets outbound auth headers on req for upstream u. It never forwards the inbound
// Authorization header. Passthrough forwards only the client's X-Upstream-Authorization value.
func applyOutbound(req *http.Request, in *http.Request, u Upstream) {
	if u.HostOverride != "" {
		req.Host = u.HostOverride // Go sends req.Host, not the Host header map entry
	}
	req.Header.Del("Authorization")
	defer func() {
		for _, h := range u.Headers { // custom headers last: they may replace what auth set
			req.Header.Set(h.Name, h.Value)
		}
	}()
	switch u.EffectiveKind() {
	case AuthBearer:
		req.Header.Set("Authorization", "Bearer "+u.AuthValue)
	case AuthHeader:
		req.Header.Set(u.AuthName, u.AuthValue)
	case AuthPassthrough:
		if in == nil {
			break
		}
		if v := strings.TrimSpace(in.Header.Get("X-Upstream-Authorization")); v != "" {
			req.Header.Set("Authorization", v)
		}
	}
}

func sameKV(a, b []KV) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

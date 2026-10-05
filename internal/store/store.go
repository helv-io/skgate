// Package store wraps the SQLite database (pure Go, modernc.org/sqlite).
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/helv-io/skgate/internal/secrets"
)

// DB is the skgate database. Secrets seals values (upstream tokens, headers, env values) before
// they are stored.
type DB struct {
	*sql.DB
	Secrets *secrets.Box
	// SecretsSource names where the encryption key came from (for the startup log).
	SecretsSource string
}

const schema = `
CREATE TABLE IF NOT EXISTS settings (k TEXT PRIMARY KEY, v TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS vkeys (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  prefix TEXT NOT NULL,
  hash TEXT NOT NULL UNIQUE,
  label TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  last_used INTEGER NOT NULL DEFAULT 0,
  revoked_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS key_usage (
  key_id INTEGER PRIMARY KEY REFERENCES vkeys(id) ON DELETE CASCADE,
  prompt_tokens INTEGER NOT NULL DEFAULT 0,
  completion_tokens INTEGER NOT NULL DEFAULT 0,
  total_tokens INTEGER NOT NULL DEFAULT 0,
  requests INTEGER NOT NULL DEFAULT 0,
  mcp_requests INTEGER NOT NULL DEFAULT 0,
  last_used INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS upstreams (
  alias TEXT PRIMARY KEY,
  url TEXT NOT NULL,
  auth_kind TEXT NOT NULL DEFAULT 'none',
  auth_name TEXT NOT NULL DEFAULT '',
  auth_value TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  host_override TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS upstream_openapi (
  alias TEXT PRIMARY KEY,
  spec TEXT NOT NULL DEFAULT '',
  spec_url TEXT NOT NULL DEFAULT '',
  selection TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS oauth_clients (
  client_id TEXT PRIMARY KEY,
  secret_hash TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '',
  redirect_uris TEXT NOT NULL DEFAULT '[]',
  auth_method TEXT NOT NULL DEFAULT 'none',
  source TEXT NOT NULL DEFAULT 'dcr',
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS oauth_codes (
  hash TEXT PRIMARY KEY,
  client_id TEXT NOT NULL,
  redirect_uri TEXT NOT NULL,
  challenge TEXT NOT NULL,
  resource TEXT NOT NULL DEFAULT '',
  scope TEXT NOT NULL DEFAULT '',
  expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS oauth_tokens (
  hash TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  client_id TEXT NOT NULL,
  resource TEXT NOT NULL DEFAULT '',
  scope TEXT NOT NULL DEFAULT '',
  expires_at INTEGER NOT NULL
);
`

// KeyFileName is the encryption key file kept next to the database when SECRETS_KEY is not set.
const KeyFileName = "secrets.key"

// Open opens (creating if needed) the database at path and applies the schema. The secrets key is
// read from the key file next to the database (created on first use).
func Open(path string) (*DB, error) { return OpenWith(path, "") }

// OpenWith is Open with an explicit SECRETS_KEY value (empty means use the key file).
func OpenWith(path, secretsKey string) (*DB, error) {
	box, src, err := secrets.LoadOrCreate(secretsKey, filepath.Join(filepath.Dir(path), KeyFileName))
	if err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?" + url.Values{"_pragma": {"busy_timeout(5000)", "journal_mode(WAL)", "foreign_keys(1)"}}.Encode()
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// A single connection keeps SQLite simple and avoids lock contention; callers must not
	// hold rows open while issuing another query.
	sdb.SetMaxOpenConns(1)
	if _, err := sdb.Exec(schema); err != nil {
		sdb.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := migrate(sdb); err != nil {
		sdb.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	db := &DB{DB: sdb, Secrets: box, SecretsSource: src}
	if err := db.SealSettings(SealedSettings...); err != nil {
		sdb.Close()
		return nil, fmt.Errorf("seal secrets: %w", err)
	}
	if err := db.sealLegacy(); err != nil {
		sdb.Close()
		return nil, fmt.Errorf("seal secrets: %w", err)
	}
	return db, nil
}

// SealedSettings are the settings holding credentials; they are stored sealed (see GetSecret).
var SealedSettings = []string{"provider.grok.access", "provider.grok.refresh", "provider.grok.id"}

// GetSecret reads a sealed setting and opens it. A value stored as plaintext by an older release is returned
// as is. ok is false when the key is unset; err is set when the value cannot be decrypted (wrong key).
func (d *DB) GetSecret(key string) (value string, ok bool, err error) {
	raw, ok := d.GetSetting(key)
	if !ok {
		return "", false, nil
	}
	v, err := d.Secrets.Open(raw)
	return v, true, err
}

// SetSecret stores value sealed.
func (d *DB) SetSecret(key, value string) error { return d.SetSetting(key, d.Secrets.Seal(value)) }

// SealSettings seals the given settings that are stored as plaintext. It is idempotent: sealed and empty
// values are left alone.
func (d *DB) SealSettings(keys ...string) error {
	for _, k := range keys {
		raw, ok := d.GetSetting(k)
		if !ok || raw == "" || secrets.IsSealed(raw) {
			continue
		}
		if err := d.SetSecret(k, raw); err != nil {
			return err
		}
	}
	return nil
}

// sealLegacy encrypts upstream credentials stored as plaintext by older releases. It is
// idempotent: sealed values are skipped.
func (d *DB) sealLegacy() error {
	rows, err := d.Query(`SELECT alias,auth_value FROM upstreams WHERE auth_value<>'' AND auth_value NOT LIKE 'enc:v1:%'`)
	if err != nil {
		return err
	}
	type row struct{ alias, val string }
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.alias, &r.val); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, r)
	}
	rows.Close()
	for _, r := range todo {
		if _, err := d.Exec(`UPDATE upstreams SET auth_value=? WHERE alias=?`, d.Secrets.Seal(r.val), r.alias); err != nil {
			return err
		}
	}
	return nil
}

// migrate adds columns that older databases lack. Columns are nullable or defaulted so existing rows stay valid.
// upstreams.host_override is an optional outbound Host header value (empty means none).
// upstreams.detected_kind / detected_note hold the result of auth auto-detection (empty for manual modes).
// upstreams.include_in_mcp marks upstreams merged into the bare /mcp aggregator. It replaces the old single-upstream
// is_default flag: when the column is first added, rows that had is_default=1 become included (the legacy column stays
// in old databases, unused). Any number of upstreams may be included.
// upstreams.url_variant remembers which trailing-slash spelling of the URL works ("" unknown, "as-is" or "toggled").
// upstreams.kind is remote (URL), stdio (managed command) or git (managed command from a repository); existing rows are
// remote. headers, env and git_token hold sealed secrets (headers and env as sealed JSON lists); args is a JSON list.
// upstreams.auto_update_secs is the opt-in auto-update interval of a managed upstream (0 = off).
// key_usage holds cumulative per-key usage (tokens reported by the API proxy, request counts); its rows go with the key.
// upstream_openapi.fetched_at is the unix time the stored description was last read from its address (0 = never, or
// pasted) and spec_hash the SHA-256 of the text fetched then ("" = not recorded); a manual update compares with them.
// vkeys.last4 is the last 4 characters of a key, only for display (older keys have none).
// vkeys.rate_per_min (requests per minute) and vkeys.expires_at (unix time, 0 = never) are optional; old keys stay unlimited and never expire.
// vkeys.url_key (0/1) allows that key as ?key= on MCP endpoints; it replaced the global allow_query_key setting.
// vkeys.hard_stop is no longer read or written; the column stays so older and newer files open alike.
// oauth_clients.last_used_at is the unix time of the last /authorize or /token use (0 = never).
// oauth_clients.pkce_seen is set (internal, not shown) after a client's first successful PKCE exchange; from then on the client needs PKCE.
// oauth_clients.jwks_uri is the JWKS URL of a metadata-document client that uses private_key_jwt.
// sub and email hold the OIDC identity that approved an MCP authorization code and the tokens it minted.
func migrate(db *sql.DB) error {
	for _, m := range []struct{ table, col, typ string }{
		{"oauth_codes", "sub", "TEXT"}, {"oauth_codes", "email", "TEXT"}, {"oauth_tokens", "sub", "TEXT"}, {"oauth_tokens", "email", "TEXT"},
		{"upstreams", "host_override", "TEXT NOT NULL DEFAULT ''"},
		{"upstreams", "detected_kind", "TEXT NOT NULL DEFAULT ''"}, {"upstreams", "detected_note", "TEXT NOT NULL DEFAULT ''"},
		{"upstreams", "include_in_mcp", "INTEGER NOT NULL DEFAULT 0"},
		{"upstreams", "url_variant", "TEXT NOT NULL DEFAULT ''"},
		{"upstreams", "kind", "TEXT NOT NULL DEFAULT 'remote'"},
		{"upstreams", "headers", "TEXT NOT NULL DEFAULT ''"},
		{"upstreams", "command", "TEXT NOT NULL DEFAULT ''"}, {"upstreams", "args", "TEXT NOT NULL DEFAULT ''"},
		{"upstreams", "env", "TEXT NOT NULL DEFAULT ''"}, {"upstreams", "shell", "INTEGER NOT NULL DEFAULT 0"},
		{"upstreams", "workdir", "TEXT NOT NULL DEFAULT ''"}, {"upstreams", "install_cmd", "TEXT NOT NULL DEFAULT ''"},
		{"upstreams", "startup_secs", "INTEGER NOT NULL DEFAULT 0"}, {"upstreams", "idle_secs", "INTEGER NOT NULL DEFAULT 0"},
		{"upstreams", "lifecycle", "TEXT NOT NULL DEFAULT 'on-demand'"},
		{"upstreams", "git_url", "TEXT NOT NULL DEFAULT ''"}, {"upstreams", "git_ref", "TEXT NOT NULL DEFAULT ''"},
		{"upstreams", "git_token", "TEXT NOT NULL DEFAULT ''"},
		{"upstreams", "auto_update_secs", "INTEGER NOT NULL DEFAULT 0"},
		{"vkeys", "last4", "TEXT NOT NULL DEFAULT ''"},
		{"vkeys", "rate_per_min", "INTEGER NOT NULL DEFAULT 0"}, {"vkeys", "hard_stop", "INTEGER NOT NULL DEFAULT 0"}, {"vkeys", "expires_at", "INTEGER NOT NULL DEFAULT 0"},
		{"vkeys", "url_key", "INTEGER NOT NULL DEFAULT 0"},
		{"upstream_openapi", "fetched_at", "INTEGER NOT NULL DEFAULT 0"}, {"upstream_openapi", "spec_hash", "TEXT NOT NULL DEFAULT ''"},
		{"oauth_clients", "last_used_at", "INTEGER NOT NULL DEFAULT 0"},
		{"oauth_clients", "pkce_seen", "INTEGER NOT NULL DEFAULT 0"},
		{"oauth_clients", "jwks_uri", "TEXT NOT NULL DEFAULT ''"},
	} {
		has, err := hasColumn(db, m.table, m.col)
		if err != nil {
			return err
		}
		if !has {
			// The column and the one-time mapping of the old default flag go in together: stopped in between, the next
			// start would see the column, skip the mapping, and the former default upstream would drop off /mcp.
			legacy := false
			if m.table == "upstreams" && m.col == "include_in_mcp" {
				if legacy, err = hasColumn(db, "upstreams", "is_default"); err != nil {
					return err
				}
			}
			tx, err := db.Begin()
			if err != nil {
				return err
			}
			if _, err := tx.Exec("ALTER TABLE " + m.table + " ADD COLUMN " + m.col + " " + m.typ); err != nil {
				tx.Rollback()
				return err
			}
			if legacy {
				if _, err := tx.Exec(`UPDATE upstreams SET include_in_mcp=1 WHERE is_default=1`); err != nil {
					tx.Rollback()
					return err
				}
			}
			if err := tx.Commit(); err != nil {
				return err
			}
		}
	}
	// Only always-on managed upstreams may be on /mcp (on-demand ones would all start at once). Idempotent.
	if _, err := db.Exec(`UPDATE upstreams SET include_in_mcp=0 WHERE include_in_mcp=1 AND kind IN ('stdio','git') AND lifecycle<>'always'`); err != nil {
		return err
	}
	return nil
}

func hasColumn(db *sql.DB, table, col string) (bool, error) {
	rows, err := db.Query("SELECT name FROM pragma_table_info('" + table + "')")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return false, err
		}
		if n == col {
			return true, nil
		}
	}
	return false, rows.Err()
}

// GetSetting reads a key from the settings table.
func (d *DB) GetSetting(key string) (string, bool) {
	v, ok, _ := d.LookupSetting(key)
	return v, ok
}

// LookupSetting is GetSetting that also tells a failed read from a missing key: only a missing key has no error.
func (d *DB) LookupSetting(key string) (string, bool, error) {
	var v string
	err := d.QueryRow(`SELECT v FROM settings WHERE k=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// SetSetting upserts a key.
func (d *DB) SetSetting(key, value string) error {
	_, err := d.Exec(`INSERT INTO settings(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, key, value)
	return err
}

// DeleteSetting removes a key.
func (d *DB) DeleteSetting(key string) error {
	_, err := d.Exec(`DELETE FROM settings WHERE k=?`, key)
	return err
}

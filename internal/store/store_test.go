package store

import (
	"strings"

	"database/sql"
	"github.com/helv-io/skgate/internal/secrets"
	"path/filepath"
	"testing"
)

// A database created by an older release (no sub/email columns) must be migrated in place.
func TestMigrateAddsIdentityColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE oauth_codes (hash TEXT PRIMARY KEY, client_id TEXT NOT NULL, redirect_uri TEXT NOT NULL, challenge TEXT NOT NULL, resource TEXT NOT NULL DEFAULT '', scope TEXT NOT NULL DEFAULT '', expires_at INTEGER NOT NULL)`,
		`CREATE TABLE oauth_tokens (hash TEXT PRIMARY KEY, kind TEXT NOT NULL, client_id TEXT NOT NULL, resource TEXT NOT NULL DEFAULT '', scope TEXT NOT NULL DEFAULT '', expires_at INTEGER NOT NULL)`,
		`INSERT INTO oauth_tokens(hash,kind,client_id,expires_at) VALUES('h','access','c',1)`,
	} {
		if _, err := old.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()
	for i := 0; i < 2; i++ { // second open proves the migration is idempotent
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var sub sql.NullString
		if err := db.QueryRow(`SELECT sub FROM oauth_tokens WHERE hash='h'`).Scan(&sub); err != nil || sub.Valid {
			t.Fatalf("old rows must have NULL sub: %v %v", err, sub)
		}
		if _, err := db.Exec(`INSERT INTO oauth_codes(hash,client_id,redirect_uri,challenge,expires_at,sub,email) VALUES('c','c','r','ch',1,'s','e')` + ``); err != nil && i == 0 {
			t.Fatal(err)
		}
		db.Close()
	}
}

// A v0.3.0 upstreams table (no host_override) must gain the column, keep its rows, and migrate idempotently.
func TestMigrateAddsHostOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v030.db")
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE upstreams (alias TEXT PRIMARY KEY, url TEXT NOT NULL, auth_kind TEXT NOT NULL DEFAULT 'none', auth_name TEXT NOT NULL DEFAULT '', auth_value TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1, is_default INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL)`,
		`INSERT INTO upstreams(alias,url,created_at) VALUES('ops','http://ops-mcp:8000/mcp',1)`,
	} {
		if _, err := old.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()
	for i := 0; i < 3; i++ {
		db, err := Open(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		var url, ho string
		if err := db.QueryRow(`SELECT url, host_override FROM upstreams WHERE alias='ops'`).Scan(&url, &ho); err != nil || ho != "" || url == "" {
			t.Fatalf("old row: %v %q %q", err, url, ho)
		}
		db.Close()
	}
}

// A database from before v0.3.3 (vkeys without last4) gains the column, keeps its rows, and migrating
// twice is harmless.
func TestMigrateAddsLast4(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v032.db")
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE vkeys (id INTEGER PRIMARY KEY AUTOINCREMENT, prefix TEXT NOT NULL, hash TEXT NOT NULL UNIQUE, label TEXT NOT NULL, created_at INTEGER NOT NULL, last_used INTEGER NOT NULL DEFAULT 0, revoked_at INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO vkeys(prefix,hash,label,created_at) VALUES('sk-abcde','h','old',1)`,
	} {
		if _, err := old.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()
	for i := 0; i < 3; i++ {
		db, err := Open(path)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		var l4, prefix string
		if err := db.QueryRow(`SELECT prefix,last4 FROM vkeys WHERE label='old'`).Scan(&prefix, &l4); err != nil || l4 != "" || prefix != "sk-abcde" {
			t.Fatalf("old key: %q %q %v", prefix, l4, err)
		}
		db.Close()
	}
}

// A v0.3.1/v0.3.2 upstreams table (host_override, no detection columns) gains them without touching
// existing rows or their auth modes, and migrating twice is harmless.
func TestMigrateAddsDetectionColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v032u.db")
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE upstreams (alias TEXT PRIMARY KEY, url TEXT NOT NULL, auth_kind TEXT NOT NULL DEFAULT 'none', auth_name TEXT NOT NULL DEFAULT '', auth_value TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1, is_default INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, host_override TEXT NOT NULL DEFAULT '')`,
		`INSERT INTO upstreams(alias,url,auth_kind,auth_value,created_at) VALUES('ops','http://ops-mcp:8000/mcp','bearer','sekret',1)`,
	} {
		if _, err := old.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()
	for i := 0; i < 3; i++ {
		db, err := Open(path)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		var kind, dk, dn, val string
		if err := db.QueryRow(`SELECT auth_kind,auth_value,detected_kind,detected_note FROM upstreams WHERE alias='ops'`).Scan(&kind, &val, &dk, &dn); err != nil {
			t.Fatal(err)
		}
		if plain, err := db.Secrets.Open(val); err != nil || kind != "bearer" || plain != "sekret" || dk != "" || dn != "" {
			t.Fatalf("existing row changed: %q %q %q %q", kind, val, dk, dn)
		}
		db.Close()
	}
}

// The include_in_mcp column replaces is_default: former default rows become included, other rows are not,
// the mapping runs once (a later edit of the flag survives re-opening), and several rows can be included.
func TestMigrateMapsDefaultToIncludeInMCP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v033.db")
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE upstreams (alias TEXT PRIMARY KEY, url TEXT NOT NULL, auth_kind TEXT NOT NULL DEFAULT 'none', auth_name TEXT NOT NULL DEFAULT '', auth_value TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1, is_default INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, host_override TEXT NOT NULL DEFAULT '', detected_kind TEXT NOT NULL DEFAULT '', detected_note TEXT NOT NULL DEFAULT '')`,
		`INSERT INTO upstreams(alias,url,is_default,created_at) VALUES('main','http://a/mcp',1,1),('other','http://b/mcp',0,2)`,
	} {
		if _, err := old.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()
	included := func(db *DB) string {
		var out string
		rows, err := db.Query(`SELECT alias FROM upstreams WHERE include_in_mcp=1 ORDER BY alias`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var a string
			rows.Scan(&a)
			out += a + ","
		}
		return out
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := included(db); got != "main," {
		t.Fatalf("after first migration included = %q", got)
	}
	// the operator includes the second one too and un-includes the first
	if _, err := db.Exec(`UPDATE upstreams SET include_in_mcp=1-include_in_mcp`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE upstreams SET include_in_mcp=1`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := included(db); got != "main,other," {
		t.Fatalf("re-open must not redo the mapping, included = %q", got)
	}
}

// upstreams.url_variant is added to an older table without touching rows, and re-opening is harmless.
func TestMigrateAddsURLVariant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v033v.db")
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE upstreams (alias TEXT PRIMARY KEY, url TEXT NOT NULL, auth_kind TEXT NOT NULL DEFAULT 'none', auth_name TEXT NOT NULL DEFAULT '', auth_value TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1, is_default INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL)`,
		`INSERT INTO upstreams(alias,url,created_at) VALUES('a','http://a/mcp/',1)`,
	} {
		if _, err := old.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()
	for i := 0; i < 2; i++ {
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var u, v string
		if err := db.QueryRow(`SELECT url,url_variant FROM upstreams WHERE alias='a'`).Scan(&u, &v); err != nil || u != "http://a/mcp/" || v != "" {
			t.Fatalf("open #%d: %q %q %v", i, u, v, err)
		}
		db.Close()
	}
}

// A pre-0.3.5 oauth_clients table gains last_used_at (0 = never), keeps its rows, and migrating twice is harmless.
func TestMigrateAddsClientLastUsed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v034.db")
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE oauth_clients (client_id TEXT PRIMARY KEY, secret_hash TEXT NOT NULL DEFAULT '', name TEXT NOT NULL DEFAULT '', redirect_uris TEXT NOT NULL DEFAULT '[]', auth_method TEXT NOT NULL DEFAULT 'none', source TEXT NOT NULL DEFAULT 'dcr', created_at INTEGER NOT NULL)`,
		`INSERT INTO oauth_clients(client_id,name,created_at) VALUES('skc-old','old',1)`,
	} {
		if _, err := old.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()
	for i := 0; i < 3; i++ {
		db, err := Open(path)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		var used int64
		var name string
		if err := db.QueryRow(`SELECT name,last_used_at FROM oauth_clients WHERE client_id='skc-old'`).Scan(&name, &used); err != nil || used != 0 || name != "old" {
			t.Fatalf("old client: %q %d %v", name, used, err)
		}
		db.Close()
	}
}

// Upstream credentials stored as plaintext by older releases are sealed on open, once, and stay
// readable; an already sealed value is left alone.
func TestOpenSealsLegacyUpstreamSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v035.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO upstreams(alias,url,auth_kind,auth_value,created_at) VALUES('legacy','http://x.example/mcp','bearer','plain-secret',1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	var first string
	for i := 0; i < 3; i++ {
		db, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var v string
		db.QueryRow(`SELECT auth_value FROM upstreams WHERE alias='legacy'`).Scan(&v)
		if !secrets.IsSealed(v) || strings.Contains(v, "plain-secret") {
			t.Fatalf("open #%d: not sealed: %q", i, v)
		}
		if i == 0 {
			first = v
		} else if v != first {
			t.Fatal("sealing must be idempotent (no re-encryption)")
		}
		if p, err := db.Secrets.Open(v); err != nil || p != "plain-secret" {
			t.Fatalf("unreadable: %q %v", p, err)
		}
		db.Close()
	}
	// a different key cannot read the value
	db, _ = OpenWith(path, "another key entirely")
	var v string
	db.QueryRow(`SELECT auth_value FROM upstreams WHERE alias='legacy'`).Scan(&v)
	if _, err := db.Secrets.Open(v); err == nil {
		t.Fatal("wrong key must not decrypt")
	}
	db.Close()
}

// A pre-0.4.0 upstreams table gains the managed-process columns; existing rows become remote
// upstreams with their data intact, and migrating again changes nothing.
func TestMigrateAddsManagedColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v035.db")
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE upstreams (alias TEXT PRIMARY KEY, url TEXT NOT NULL, auth_kind TEXT NOT NULL DEFAULT 'none', auth_name TEXT NOT NULL DEFAULT '', auth_value TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL, host_override TEXT NOT NULL DEFAULT '', detected_kind TEXT NOT NULL DEFAULT '', detected_note TEXT NOT NULL DEFAULT '', include_in_mcp INTEGER NOT NULL DEFAULT 0, url_variant TEXT NOT NULL DEFAULT '')`,
		`INSERT INTO upstreams(alias,url,auth_kind,auth_value,created_at) VALUES('a','http://a/mcp','bearer','tok-123456789',1)`,
	} {
		if _, err := old.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()
	for i := 0; i < 3; i++ {
		db, err := Open(path)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		var kind, headers, cmd, args, env, wd, inst, life, gu, gr, gt, url string
		var shell, ss, is int
		err = db.QueryRow(`SELECT kind,headers,command,args,env,shell,workdir,install_cmd,startup_secs,idle_secs,lifecycle,git_url,git_ref,git_token,url FROM upstreams WHERE alias='a'`).
			Scan(&kind, &headers, &cmd, &args, &env, &shell, &wd, &inst, &ss, &is, &life, &gu, &gr, &gt, &url)
		if err != nil {
			t.Fatal(err)
		}
		if kind != "remote" || life != "on-demand" || url != "http://a/mcp" || headers != "" || cmd != "" || env != "" || shell != 0 || ss != 0 || is != 0 {
			t.Fatalf("open #%d: kind=%q life=%q url=%q", i, kind, life, url)
		}
		var av string
		db.QueryRow(`SELECT auth_value FROM upstreams WHERE alias='a'`).Scan(&av)
		if !secrets.IsSealed(av) {
			t.Fatalf("legacy credential should have been sealed, got %q", av)
		}
		db.Close()
	}
}

// A database from before the key limits keeps its keys, all unlimited and never expiring (the unused hard_stop column stays), and migrating twice is harmless.
func TestMigrateKeyLimitsStayUnlimited(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v075.db")
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE vkeys (id INTEGER PRIMARY KEY AUTOINCREMENT, prefix TEXT NOT NULL, hash TEXT NOT NULL UNIQUE, label TEXT NOT NULL, created_at INTEGER NOT NULL, last_used INTEGER NOT NULL DEFAULT 0, revoked_at INTEGER NOT NULL DEFAULT 0, last4 TEXT NOT NULL DEFAULT '')`,
		`INSERT INTO vkeys(prefix,hash,label,created_at) VALUES('sk-abcde','h','old',1)`,
	} {
		if _, err := old.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()
	for i := 0; i < 2; i++ {
		db, err := Open(path)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		var rate, stop, expires int64
		if err := db.QueryRow(`SELECT rate_per_min,hard_stop,expires_at FROM vkeys WHERE label='old'`).Scan(&rate, &stop, &expires); err != nil || rate != 0 || stop != 0 || expires != 0 {
			t.Fatalf("old key limits: %d %d %d %v", rate, stop, expires, err)
		}
		db.Close()
	}
}

// On-demand managed rows lose their /mcp flag at open; always-on and remote rows keep it; running again changes nothing.
func TestMigrateClearsOnDemandManagedFromMCP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inmcp.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct{ alias, kind, life string }{
		{"od-stdio", "stdio", "on-demand"}, {"od-git", "git", "on-demand"}, {"ao", "stdio", "always"}, {"rem", "remote", "on-demand"}} {
		if _, err := db.Exec(`INSERT INTO upstreams(alias,url,auth_kind,created_at,kind,lifecycle,include_in_mcp) VALUES(?,?,?,?,?,?,1)`, r.alias, "http://x", "none", 1, r.kind, r.life); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	for i := 0; i < 2; i++ {
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]int{}
		rows, _ := db.Query(`SELECT alias,include_in_mcp FROM upstreams`)
		for rows.Next() {
			var a string
			var n int
			rows.Scan(&a, &n)
			got[a] = n
		}
		rows.Close()
		db.Close()
		if got["od-stdio"] != 0 || got["od-git"] != 0 || got["ao"] != 1 || got["rem"] != 1 {
			t.Fatalf("pass %d: %v", i, got)
		}
	}
}

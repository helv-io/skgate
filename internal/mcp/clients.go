package mcp

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"time"

	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/store"
)

// Client is a registered OAuth client (DCR or admin-created).
type Client struct {
	ID           string
	SecretHash   string
	Name         string
	RedirectURIs []string
	AuthMethod   string // none | client_secret_post | client_secret_basic | private_key_jwt
	JWKSURI      string // set for private_key_jwt metadata-document clients
	Source       string // dcr | admin | cimd
	CreatedAt    time.Time
	LastUsed     time.Time // zero = never used at /authorize or /token
	// PKCESeen is set after the client's first successful PKCE exchange (internal, not shown). From then
	// on it must use PKCE, even if it is confidential.
	PKCESeen bool
}

// PKCEOptional reports whether /authorize may accept a request without code_challenge: only from a
// confidential client (it authenticates with its secret at /token) that has never used PKCE. Public
// and dynamically registered clients without a secret always need PKCE.
func (c Client) PKCEOptional() bool { return c.Confidential() && !c.PKCESeen }

// Confidential reports whether the client has a secret.
func (c Client) Confidential() bool { return c.SecretHash != "" }

// CheckSecret verifies a presented secret in constant time.
func (c Client) CheckSecret(secret string) bool {
	if c.SecretHash == "" || secret == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.SecretHash), []byte(httputil.SHA256Hex(secret))) == 1
}

// Clients is the registry of OAuth clients.
type Clients struct{ db *store.DB }

// NewClients returns the registry.
func NewClients(db *store.DB) *Clients { return &Clients{db: db} }

// Create stores a client. secret may be empty for public clients. Returns the client.
func (s *Clients) Create(c Client, secret string) (Client, error) {
	if c.ID == "" {
		return c, errors.New("client id required")
	}
	if secret != "" {
		c.SecretHash = httputil.SHA256Hex(secret)
	}
	uris, _ := json.Marshal(c.RedirectURIs)
	c.CreatedAt = time.Now()
	_, err := s.db.Exec(`INSERT INTO oauth_clients(client_id,secret_hash,name,redirect_uris,auth_method,source,created_at) VALUES(?,?,?,?,?,?,?)`,
		c.ID, c.SecretHash, c.Name, string(uris), c.AuthMethod, c.Source, c.CreatedAt.Unix())
	if err == nil && c.Source == "dcr" {
		// Bound growth from anonymous registrations: keep the newest 500.
		_, _ = s.db.Exec(`DELETE FROM oauth_clients WHERE source='dcr' AND client_id NOT IN (SELECT client_id FROM oauth_clients WHERE source='dcr' ORDER BY created_at DESC, rowid DESC LIMIT 500)`)
	}
	return c, err
}

// PutMetadataClient stores or refreshes a client described by a client ID metadata document. Its
// name and redirect URIs follow the document; the last use and PKCE state stay. The newest 500 such
// clients are kept. A stored client of another source with the same ID is left alone.
func (s *Clients) PutMetadataClient(c Client) error {
	uris, _ := json.Marshal(c.RedirectURIs)
	if c.AuthMethod == "" {
		c.AuthMethod = "none"
	}
	_, err := s.db.Exec(`INSERT INTO oauth_clients(client_id,secret_hash,name,redirect_uris,auth_method,jwks_uri,source,created_at) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(client_id) DO UPDATE SET name=excluded.name, redirect_uris=excluded.redirect_uris, auth_method=excluded.auth_method, jwks_uri=excluded.jwks_uri WHERE source=excluded.source`,
		c.ID, "", c.Name, string(uris), c.AuthMethod, c.JWKSURI, cimdSource, time.Now().Unix())
	if err == nil {
		_, _ = s.db.Exec(`DELETE FROM oauth_clients WHERE source=? AND client_id NOT IN (SELECT client_id FROM oauth_clients WHERE source=? ORDER BY created_at DESC, rowid DESC LIMIT ?)`, cimdSource, cimdSource, cimdKeep)
	}
	return err
}

func scanClient(sc interface{ Scan(...any) error }) (Client, error) {
	var c Client
	var uris string
	var ts, used, seen int64
	if err := sc.Scan(&c.ID, &c.SecretHash, &c.Name, &uris, &c.AuthMethod, &c.JWKSURI, &c.Source, &ts, &used, &seen); err != nil {
		return c, err
	}
	c.PKCESeen = seen != 0
	_ = json.Unmarshal([]byte(uris), &c.RedirectURIs)
	c.CreatedAt = time.Unix(ts, 0)
	if used > 0 {
		c.LastUsed = time.Unix(used, 0)
	}
	return c, nil
}

// Touch records that a client was just used at /authorize or /token.
func (s *Clients) Touch(id string) {
	_, _ = s.db.Exec(`UPDATE oauth_clients SET last_used_at=? WHERE client_id=?`, time.Now().Unix(), id)
}

// MarkPKCE records that the client completed a PKCE exchange, so it needs PKCE from now on.
func (s *Clients) MarkPKCE(id string) {
	_, _ = s.db.Exec(`UPDATE oauth_clients SET pkce_seen=1 WHERE client_id=?`, id)
}

// Get returns a client by id.
func (s *Clients) Get(id string) (Client, bool) {
	c, err := scanClient(s.db.QueryRow(`SELECT client_id,secret_hash,name,redirect_uris,auth_method,jwks_uri,source,created_at,last_used_at,pkce_seen FROM oauth_clients WHERE client_id=?`, id))
	return c, err == nil
}

// List returns clients, newest first.
func (s *Clients) List() ([]Client, error) {
	rows, err := s.db.Query(`SELECT client_id,secret_hash,name,redirect_uris,auth_method,jwks_uri,source,created_at,last_used_at,pkce_seen FROM oauth_clients ORDER BY created_at DESC, rowid DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Client
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Delete removes a client and its codes and tokens.
func (s *Clients) Delete(id string) error {
	for _, q := range []string{`DELETE FROM oauth_tokens WHERE client_id=?`, `DELETE FROM oauth_codes WHERE client_id=?`, `DELETE FROM oauth_clients WHERE client_id=?`} {
		if _, err := s.db.Exec(q, id); err != nil {
			return err
		}
	}
	return nil
}

// unusedWhere selects clients nobody has used since the cutoff: last used (or, never used, created) before it.
const unusedWhere = `(CASE WHEN last_used_at > 0 THEN last_used_at ELSE created_at END) < ?`

// CountUnused returns how many clients were last used (or, if never used, created) before the cutoff.
func (s *Clients) CountUnused(cutoff time.Time) int {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM oauth_clients WHERE `+unusedWhere, cutoff.Unix()).Scan(&n)
	return n
}

// DeleteUnused deletes the clients counted by CountUnused with their codes and tokens and returns how many.
func (s *Clients) DeleteUnused(cutoff time.Time) (int, error) {
	rows, err := s.db.Query(`SELECT client_id FROM oauth_clients WHERE `+unusedWhere, cutoff.Unix())
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if err := s.Delete(id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

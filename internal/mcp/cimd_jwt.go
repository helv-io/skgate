package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// private_key_jwt for Client ID Metadata Document clients (RFC 7523 / OIDC): the document names a
// jwks_uri; at /token the client sends a signed client_assertion. DCR still only offers none /
// client_secret_*.

const (
	clientAssertionType = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"
	maxAssertionLife    = 5 * time.Minute
	jwksMaxBody         = 64 << 10
)

var assertionAlgs = []jose.SignatureAlgorithm{jose.RS256, jose.ES256}

// jwksBook caches JWKS documents fetched for private_key_jwt metadata clients.
type jwksBook struct {
	mu    sync.Mutex
	fresh map[string]jwksState
}

type jwksState struct {
	until time.Time
	keys  jose.JSONWebKeySet
	err   error
}

// parseJWKSURI checks that raw is an https URL suitable as jwks_uri (no userinfo or fragment).
func parseJWKSURI(raw string) (*url.URL, string) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(raw), "https://") {
		return nil, "jwks_uri must be an https URL"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.Contains(raw, "#") || u.Opaque != "" {
		return nil, "jwks_uri is not an https URL without userinfo and fragment"
	}
	return u, ""
}

// tokenEndpoint is the absolute token URL clients must put in aud.
func (s *Server) tokenEndpoint() string { return s.Issuer() + "/token" }

// verifyClientAssertion checks client_assertion for a private_key_jwt metadata client.
func (s *Server) verifyClientAssertion(ctx context.Context, r *http.Request, c Client) error {
	if r.PostFormValue("client_assertion_type") != clientAssertionType {
		return errors.New("client_assertion_type must be " + clientAssertionType)
	}
	raw := r.PostFormValue("client_assertion")
	if raw == "" {
		return errors.New("client_assertion is required")
	}
	if c.JWKSURI == "" {
		return errors.New("client has no jwks_uri")
	}
	tok, err := jwt.ParseSigned(raw, assertionAlgs)
	if err != nil {
		return errors.New("client_assertion is not a valid JWT")
	}
	if len(tok.Headers) == 0 {
		return errors.New("client_assertion has no header")
	}
	kid := tok.Headers[0].KeyID
	key, err := s.jwksKey(ctx, c.JWKSURI, kid)
	if err != nil {
		return err
	}
	var claims jwt.Claims
	if err := tok.Claims(key, &claims); err != nil {
		return errors.New("client_assertion signature is invalid")
	}
	if claims.Expiry == nil {
		return errors.New("client_assertion is missing exp")
	}
	now := time.Now()
	if err := claims.Validate(jwt.Expected{
		Issuer:      c.ID,
		Subject:     c.ID,
		AnyAudience: jwt.Audience{s.tokenEndpoint()},
		Time:        now,
	}); err != nil {
		return fmt.Errorf("client_assertion claims: %v", err)
	}
	// Bound lifetime so a long-lived signed assertion cannot be replayed for hours.
	exp := claims.Expiry.Time()
	start := now
	if claims.IssuedAt != nil {
		start = claims.IssuedAt.Time()
	} else if claims.NotBefore != nil {
		start = claims.NotBefore.Time()
	}
	if exp.Sub(start) > maxAssertionLife+time.Minute {
		return errors.New("client_assertion lifetime is too long")
	}
	return nil
}

// jwksKey returns a verification key for kid from uri (cached). An empty kid uses the sole key when the set has one.
func (s *Server) jwksKey(ctx context.Context, uri, kid string) (any, error) {
	set, err := s.jwksGet(ctx, uri)
	if err != nil {
		return nil, err
	}
	if kid != "" {
		ks := set.Key(kid)
		if len(ks) == 0 {
			// Stale cache: refetch once.
			s.jwksInvalidate(uri)
			set, err = s.jwksGet(ctx, uri)
			if err != nil {
				return nil, err
			}
			ks = set.Key(kid)
		}
		if len(ks) == 0 {
			return nil, errors.New("no JWK matches the assertion kid")
		}
		return ks[0], nil
	}
	if len(set.Keys) == 1 {
		return set.Keys[0], nil
	}
	return nil, errors.New("client_assertion needs a kid")
}

func (s *Server) jwksInvalidate(uri string) {
	s.jwks.mu.Lock()
	defer s.jwks.mu.Unlock()
	if s.jwks.fresh != nil {
		delete(s.jwks.fresh, uri)
	}
}

func (s *Server) jwksGet(ctx context.Context, uri string) (jose.JSONWebKeySet, error) {
	s.jwks.mu.Lock()
	if s.jwks.fresh == nil {
		s.jwks.fresh = map[string]jwksState{}
	}
	if st, ok := s.jwks.fresh[uri]; ok && time.Now().Before(st.until) {
		s.jwks.mu.Unlock()
		return st.keys, st.err
	}
	s.jwks.mu.Unlock()

	keys, ttl, err := s.fetchJWKS(ctx, uri)
	s.jwks.mu.Lock()
	defer s.jwks.mu.Unlock()
	if s.jwks.fresh == nil {
		s.jwks.fresh = map[string]jwksState{}
	}
	if err != nil {
		s.jwks.fresh[uri] = jwksState{until: time.Now().Add(cimdFailTTL), err: err}
		return jose.JSONWebKeySet{}, err
	}
	s.jwks.fresh[uri] = jwksState{until: time.Now().Add(ttl), keys: keys}
	if len(s.jwks.fresh) > 4*cimdKeep {
		s.jwks.fresh = map[string]jwksState{uri: s.jwks.fresh[uri]}
	}
	return keys, nil
}

func (s *Server) fetchJWKS(ctx context.Context, uri string) (jose.JSONWebKeySet, time.Duration, error) {
	u, why := parseJWKSURI(uri)
	if why != "" {
		return jose.JSONWebKeySet{}, 0, errors.New(why)
	}
	hc := s.cimd.client
	if hc == nil {
		hc = newCIMDClient()
	}
	ctx, cancel := context.WithTimeout(ctx, cimdTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return jose.JSONWebKeySet{}, 0, errors.New("invalid jwks_uri")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "skgate (client id metadata)")
	resp, err := hc.Do(req)
	if err != nil {
		return jose.JSONWebKeySet{}, 0, fmt.Errorf("fetching JWKS failed: %s", describeFetchErr(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return jose.JSONWebKeySet{}, 0, fmt.Errorf("JWKS answered HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, jwksMaxBody+1))
	if err != nil {
		return jose.JSONWebKeySet{}, 0, fmt.Errorf("reading JWKS failed: %s", describeFetchErr(err))
	}
	if len(body) > jwksMaxBody {
		return jose.JSONWebKeySet{}, 0, fmt.Errorf("JWKS is larger than %d KB", jwksMaxBody>>10)
	}
	var set jose.JSONWebKeySet
	if err := json.Unmarshal(body, &set); err != nil || len(set.Keys) == 0 {
		return jose.JSONWebKeySet{}, 0, errors.New("JWKS is not a usable JSON Web Key Set")
	}
	for i := range set.Keys {
		if !set.Keys[i].Valid() {
			return jose.JSONWebKeySet{}, 0, errors.New("JWKS contains an invalid key")
		}
	}
	return set, cacheTTL(resp.Header.Get("Cache-Control")), nil
}

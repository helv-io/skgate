package openapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/helv-io/skgate/internal/httputil"
)

// fetchClient follows at most five redirects and never steps down from https to http.
var fetchClient = &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("the address keeps redirecting")
	}
	if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
		return errors.New("the address redirects from https to http")
	}
	return nil
}}

// Fetch downloads a description from an http(s) address. The answer is limited to MaxSpecBytes.
func Fetch(ctx context.Context, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("the address must be http or https")
	}
	if u.User != nil {
		return nil, errors.New("the address cannot hold a user name or password")
	}
	if err := httputil.CheckScheme(u); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, errors.New("invalid address")
	}
	req.Header.Set("Accept", "application/json, application/yaml, text/yaml, application/toml, text/plain, */*;q=0.5")
	req.Header.Set("User-Agent", "skgate-openapi")
	resp, err := fetchClient.Do(req)
	if err != nil {
		return nil, ErrUnreachable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the address answered %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxSpecBytes+1))
	if err != nil {
		return nil, ErrUnreachable
	}
	if len(b) > MaxSpecBytes {
		return nil, fmt.Errorf("the description is over %d MB", MaxSpecBytes>>20)
	}
	return b, nil
}

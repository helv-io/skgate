package keyed

import (
	"net/url"
	"strings"
)

// BaseCandidates lists the base URLs worth trying for an address a person typed, best guess first. People paste
// the address in whatever form they have: with or without a trailing slash, with or without the version path, or
// the full address of an endpoint. Each candidate is a clean base (no trailing slash, no query), and the list has
// no repeats. An address that is not an absolute http(s) URL gives none.
func BaseCandidates(raw string) []string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil
	}
	path := strings.TrimRight(u.Path, "/")
	// an endpoint pasted instead of the base
	for _, end := range []string{"/chat/completions", "/completions", "/responses", "/embeddings", "/models"} {
		if strings.HasSuffix(strings.ToLower(path), end) {
			path = strings.TrimRight(path[:len(path)-len(end)], "/")
			break
		}
	}
	root := path
	for _, v := range []string{"/api/v1", "/v1", "/api"} {
		if strings.HasSuffix(root, v) {
			root = strings.TrimRight(root[:len(root)-len(v)], "/")
			break
		}
	}
	paths := []string{path}
	if root != path {
		// the given address ends in a version path: try it, then the shorter forms
		paths = append(paths, root, root+"/v1", root+"/api/v1", root+"/api")
	} else {
		paths = append(paths, path+"/v1", path+"/api/v1", path+"/api")
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range paths {
		c := (&url.URL{Scheme: u.Scheme, User: u.User, Host: u.Host, Path: p}).String()
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

package managed

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// inheritKeys are the only variables a child inherits from skgate's own environment: enough for
// runtimes to find binaries, certificates, caches and proxies. Everything else (OIDC_*,
// SECRETS_KEY, PUID, ...) is dropped, so a child never sees skgate's secrets.
var inheritKeys = []string{
	"PATH", "LANG", "LC_ALL", "LC_CTYPE", "TZ",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE",
	"NPM_CONFIG_CACHE", "UV_CACHE_DIR", "UV_PYTHON_INSTALL_DIR", "UV_TOOL_DIR", "XDG_CACHE_HOME", "PIP_CACHE_DIR",
	"DOTNET_ROOT", "DOTNET_CLI_TELEMETRY_OPTOUT", "DOTNET_NOLOGO", "DOTNET_SKIP_FIRST_TIME_EXPERIENCE", "DOTNET_SYSTEM_GLOBALIZATION_INVARIANT", "DOTNET_GENERATE_ASPNET_CERTIFICATE", "NUGET_PACKAGES",
	"GOROOT", "GOTOOLCHAIN", "GOPROXY", "GOSUMDB", "CGO_ENABLED",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
}

// CacheEnv points the package managers at one upstream's own cache directory.
func CacheEnv(cacheDir string) []KV {
	return []KV{
		{"NPM_CONFIG_CACHE", filepath.Join(cacheDir, "npm")},
		{"UV_CACHE_DIR", filepath.Join(cacheDir, "uv")},
		{"PIP_CACHE_DIR", filepath.Join(cacheDir, "pip")},
		{"XDG_CACHE_HOME", filepath.Join(cacheDir, "xdg")},
		{"NUGET_PACKAGES", filepath.Join(cacheDir, "nuget")},
		{"GOCACHE", filepath.Join(cacheDir, "go-build")},
		{"GOPATH", filepath.Join(cacheDir, "gopath")},
		{"GOMODCACHE", filepath.Join(cacheDir, "gomod")},
		{"GOFLAGS", "-modcacherw"}, // writable module cache, so removing an upstream's cache works
	}
}

// BuildEnv returns the child environment: the inherited allow list from parent (KEY=VALUE
// entries), a per-process HOME and TMPDIR, a default LANG and PATH, then the user's variables
// (which may override any of these). The result is sorted and has no duplicates.
func BuildEnv(parent []string, home, tmp string, user []KV) []string {
	m := map[string]string{}
	allowed := map[string]bool{}
	for _, k := range inheritKeys {
		allowed[k] = true
	}
	for _, e := range parent {
		k, v, ok := strings.Cut(e, "=")
		if ok && allowed[k] && v != "" {
			m[k] = v
		}
	}
	m["HOME"] = home
	m["TMPDIR"] = tmp
	if m["LANG"] == "" {
		m["LANG"] = "C.UTF-8"
	}
	if m["PATH"] == "" {
		m["PATH"] = "/usr/local/bin:/usr/bin:/bin"
	}
	for _, kv := range user {
		m[kv.Name] = kv.Value
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// osEnviron is replaced in tests.
var osEnviron = os.Environ

// redactor replaces secret values in text with [redacted].
type redactor struct{ secrets []string }

// newRedactor collects values worth hiding (at least 6 characters, so ordinary words are safe).
func newRedactor(vals ...string) *redactor {
	r := &redactor{}
	for _, v := range vals {
		if len(v) >= 6 {
			r.secrets = append(r.secrets, v)
		}
	}
	// longest first, so a value containing another is replaced whole
	sort.Slice(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })
	return r
}

func (r *redactor) apply(s string) string {
	if r == nil {
		return s
	}
	for _, v := range r.secrets {
		if strings.Contains(s, v) {
			s = strings.ReplaceAll(s, v, "[redacted]")
		}
	}
	return s
}

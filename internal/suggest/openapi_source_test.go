package suggest

import "testing"

func TestParseSourceRoutesAPIAddresses(t *testing.T) {
	api := []string{
		"http://application:8080", "http://application:8080/openapi.json", "application:8080", "localhost:3000", "192.168.1.5:8080/docs",
		"http://nas.local/api", "https://api.example.com", "https://api.example.com/openapi.json", "https://example.com/swagger.yaml",
		"application.lan/openapi.json", "http://10.0.0.2/swagger/v1/swagger.json", "https://application.example.com/api-docs",
	}
	for _, in := range api {
		s, err := ParseSource(in)
		if err != nil || s.Kind != KindOpenAPI {
			t.Errorf("%q: kind %q err %v", in, s.Kind, err)
		}
	}
	other := map[string]Kind{
		"https://github.com/owner/repo": KindGit, "github.com/owner/repo": KindGit, "git@github.com:owner/repo.git": KindGit,
		"https://gitlab.com/g/p/-/tree/main/x": KindGit, "https://git.example.com/owner/repo": KindGit,
		"https://www.npmjs.com/package/foo": KindNPM, "npm:foo": KindNPM, "pypi:foo": KindPyPI, "@scope/pkg": KindNPM, "foo@1.2.3": KindNPM,
		"foo==1.0": KindPyPI, "some-package": KindPackage, "go:github.com/a/b": KindUnsupported,
		"https://github.com/o/r/blob/main/package.json": KindGit,
	}
	for in, want := range other {
		if s, err := ParseSource(in); err != nil || s.Kind != want {
			t.Errorf("%q: kind %q err %v, want %q", in, s.Kind, err, want)
		}
	}
	// a forge or a registry over plain http keeps its rule
	for _, in := range []string{"http://github.com/owner/repo", "http://www.npmjs.com/package/x"} {
		if _, err := ParseSource(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestAliasFor(t *testing.T) {
	for in, want := range map[string]string{
		"http://application:8080/openapi.json": "application", "application:8080": "application", "https://api.example.com": "example",
		"https://www.Foo-Bar.io/x": "foo-bar", "http://192.168.1.5:80": "api", "http://[::1]:80": "api", "http://docs.api.acme.dev": "acme",
	} {
		if got := AliasFor(in); got != want {
			t.Errorf("AliasFor(%q) = %q, want %q", in, got, want)
		}
	}
}

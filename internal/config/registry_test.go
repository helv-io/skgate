package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// server.json (official MCP registry) and the image labels must agree with each other and with the binary's version,
// or the registry refuses the entry. Bump server.json with Version in the same release.
func TestRegistryFilesAgreeWithTheVersion(t *testing.T) {
	root := filepath.Join("..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "server.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sj struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Version     string `json:"version"`
		Packages    []struct {
			RegistryType string `json:"registryType"`
			Identifier   string `json:"identifier"`
			Transport    struct {
				Type string `json:"type"`
				URL  string `json:"url"`
			} `json:"transport"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(raw, &sj); err != nil {
		t.Fatal(err)
	}
	if sj.Version != Version {
		t.Errorf("server.json version %q, binary %q: bump both in the release commit", sj.Version, Version)
	}
	if len(sj.Description) == 0 || len(sj.Description) > 100 {
		t.Errorf("server.json description must be 1 to 100 characters, got %d", len(sj.Description))
	}
	want := map[string]bool{"ghcr.io/helv-io/skgate:" + Version: false, "ghcr.io/helv-io/skgate:" + Version + "-slim": false}
	for _, p := range sj.Packages {
		if _, ok := want[p.Identifier]; !ok || p.RegistryType != "oci" || p.Transport.Type != "streamable-http" || !strings.HasSuffix(p.Transport.URL, "/mcp") {
			t.Errorf("unexpected package %+v", p)
		}
		want[p.Identifier] = true
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("server.json lacks the package %s", id)
		}
	}
	// every image variant carries the label the registry verifies
	df, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	label := `LABEL io.modelcontextprotocol.server.name="` + sj.Name + `"`
	for _, stage := range []string{"AS slim", "AS full"} {
		i := strings.Index(string(df), stage)
		if i < 0 {
			t.Fatalf("Dockerfile has no stage %q", stage)
		}
		rest := string(df)[i:]
		if j := strings.Index(rest[1:], "\nFROM "); j >= 0 {
			rest = rest[:j+1]
		}
		if !strings.Contains(rest, label) {
			t.Errorf("stage %q lacks %s", stage, label)
		}
	}
	// glama.json: valid, with at least one maintainer
	gb, err := os.ReadFile(filepath.Join(root, "glama.json"))
	if err != nil {
		t.Fatal(err)
	}
	var gl struct {
		Schema      string   `json:"$schema"`
		Maintainers []string `json:"maintainers"`
	}
	if err := json.Unmarshal(gb, &gl); err != nil || len(gl.Maintainers) == 0 || !regexp.MustCompile(`^https://glama\.ai/mcp/schemas/server\.json$`).MatchString(gl.Schema) {
		t.Errorf("glama.json must carry the Glama schema and a maintainer: %v %+v", err, gl)
	}
}

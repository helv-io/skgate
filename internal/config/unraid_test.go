package config

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Unraid Community Apps files (the template and icon in unraid/, the maintainer profile at the repository root, where the CA scanner looks) must be well-formed XML and describe this image; the template's
// Repository, TemplateURL and Icon point at files that exist in the repository.
func TestUnraidTemplateFilesParse(t *testing.T) {
	dir := filepath.Join("..", "..", "unraid")
	var tpl struct {
		XMLName     xml.Name `xml:"Container"`
		Name        string   `xml:"Name"`
		Repository  string   `xml:"Repository"`
		TemplateURL string   `xml:"TemplateURL"`
		Icon        string   `xml:"Icon"`
		Configs     []struct {
			Name   string `xml:"Name,attr"`
			Target string `xml:"Target,attr"`
			Type   string `xml:"Type,attr"`
		} `xml:"Config"`
	}
	b, err := os.ReadFile(filepath.Join(dir, "skgate.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal(b, &tpl); err != nil {
		t.Fatalf("skgate.xml does not parse: %v", err)
	}
	if tpl.Name != "skgate" || !strings.HasPrefix(tpl.Repository, "ghcr.io/helv-io/skgate") || len(tpl.Configs) == 0 {
		t.Errorf("unexpected template: %+v", tpl)
	}
	for _, u := range []string{tpl.TemplateURL, tpl.Icon} {
		const prefix = "https://raw.githubusercontent.com/helv-io/skgate/master/unraid/"
		if !strings.HasPrefix(u, prefix) {
			t.Errorf("%q must point into unraid/ on the default branch (master)", u)
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, strings.TrimPrefix(u, prefix))); err != nil {
			t.Errorf("%q: %v", u, err)
		}
	}
	var profile struct {
		XMLName xml.Name `xml:"CommunityApplications"`
		Profile string   `xml:"Profile"`
		Icon    string   `xml:"Icon"`
		Web     string   `xml:"WebPage"`
		Forum   string   `xml:"Forum"`
	}
	b, err = os.ReadFile(filepath.Join("..", "..", "ca_profile.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal(b, &profile); err != nil || strings.TrimSpace(profile.Profile) == "" {
		t.Fatalf("ca_profile.xml does not parse or has no Profile text: %v %+v", err, profile)
	}
	if !strings.HasPrefix(profile.Icon, "https://raw.githubusercontent.com/helv-io/skgate/master/unraid/") || !strings.HasPrefix(profile.Web, "https://github.com/helv-io/skgate") || !strings.HasPrefix(profile.Forum, "https://forums.unraid.net/") {
		t.Errorf("ca_profile.xml links: %+v", profile)
	}
}

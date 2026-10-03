package main

import (
	"testing"

	"github.com/helv-io/skgate/internal/config"
)

func TestOpenAdminNoteOnlyWithoutAllowList(t *testing.T) {
	oidc := config.Config{OIDCIssuer: "https://auth.example.com", OIDCClientID: "id", OIDCClientSecret: "s"}
	const note = "all OIDC users are admins (no allow-list set)"
	if n := startupNotes(&oidc); len(n) != 1 || n[0] != note {
		t.Fatalf("no allow-list: %v", n)
	}
	withEmails, withGroups := oidc, oidc
	withEmails.OIDCEmails = []string{"admin@example.com"}
	withGroups.OIDCGroups = []string{"admins"}
	if n := startupNotes(&withEmails); len(n) != 0 {
		t.Errorf("email allow-list: %v", n)
	}
	if n := startupNotes(&withGroups); len(n) != 0 {
		t.Errorf("group allow-list: %v", n)
	}
	if n := startupNotes(&config.Config{}); len(n) != 0 {
		t.Errorf("OIDC unconfigured: %v", n)
	}
}

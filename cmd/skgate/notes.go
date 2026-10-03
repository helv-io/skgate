package main

import "github.com/helv-io/skgate/internal/config"

// startupNotes are the configuration facts worth one log line each at start.
func startupNotes(cfg *config.Config) []string {
	var notes []string
	if cfg.AllOIDCUsersAdmin() {
		notes = append(notes, "all OIDC users are admins (no allow-list set)")
	}
	return notes
}

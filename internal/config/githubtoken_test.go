package config

import "testing"

func TestGitHubTokenIsOptionalAndTrimmed(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	if c := Load(); c.GitHubToken != "" {
		t.Fatalf("default: %q", c.GitHubToken)
	}
	t.Setenv("GITHUB_TOKEN", "  ghp_example \n")
	if c := Load(); c.GitHubToken != "ghp_example" {
		t.Fatalf("set: %q", c.GitHubToken)
	}
}

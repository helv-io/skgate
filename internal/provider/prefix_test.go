package provider

import (
	"path/filepath"
	"testing"

	"github.com/helv-io/skgate/internal/store"
)

func TestPrefixShapeAndCollisions(t *testing.T) {
	if err := CheckPrefix(""); err == nil || CheckPrefix("OpenAI") == nil || CheckPrefix("open-ai") == nil || CheckPrefix("openai_gpt") == nil {
		t.Fatal("empty, uppercase, a dash and an underscore are refused")
	}
	if err := CheckPrefix("openai2"); err != nil {
		t.Fatal(err)
	}
	if SlugPrefix("Google Gemini!") != "googlegemini" {
		t.Fatalf("slug %q", SlugPrefix("Google Gemini!"))
	}
	if got := SlugPrefix("abcdefghijklmnopqr"); got != "abcdefghijklmnop" || len(got) != 16 {
		t.Fatalf("truncated %q", got)
	}
	used := map[string]bool{"openai": true, "openai2": true}
	if got := UniquePrefix("openai", used); got != "openai3" {
		t.Fatalf("collision %q", got)
	}
	used["abcdefghijklmnop"] = true
	if got := UniquePrefix("abcdefghijklmnop", used); got != "abcdefghijklmno2" || len(got) != 16 {
		t.Fatalf("long collision %q", got)
	}
	if Expose("openai", "gpt-4o") != "openai_gpt-4o" || Expose("", "grok-4") != "grok-4" {
		t.Fatal("expose")
	}
	if raw, ok := Bare("openai", "openai_gpt-4o"); !ok || raw != "gpt-4o" {
		t.Fatalf("bare %q %v", raw, ok)
	}
	if _, ok := Bare("openai", "gpt-4o"); ok {
		t.Fatal("a bare id is not a prefixed one")
	}
	if _, ok := Bare("o", "openai_gpt-4o"); ok {
		t.Fatal("a shorter prefix must not take a longer one")
	}
}

func TestMigratePrefixesRewritesAliasesAndHelper(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := Settings{KV: db}
	if err := s.SetPrefix("anthropic", "openai"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutAlias("openai", "smart", "gpt-4o", []string{"gpt-4o"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModel("grok", "gpt-4o"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutAlias("openai", "kept", "gpt-4o", []string{"gpt-4o"}); err != nil {
		t.Fatal(err)
	}
	// an alias name used as the helper stays the name
	if err := s.SetModel("grok", "smart"); err != nil {
		t.Fatal(err)
	}
	s.MigratePrefixes([]PrefixDef{{ID: "openai", Base: "openai"}, {ID: "anthropic", Base: "anthropic"}}, "grok")
	if s.Prefix("openai") != "openai2" {
		t.Fatalf("openai prefix %q, want openai2 because anthropic already holds openai", s.Prefix("openai"))
	}
	if s.Prefix("anthropic") != "openai" {
		t.Fatalf("anthropic prefix %q", s.Prefix("anthropic"))
	}
	if s.Model("grok") != "smart" {
		t.Fatalf("alias helper rewritten: %q", s.Model("grok"))
	}
	var saw bool
	for _, a := range s.Aliases("openai") {
		if a.Name == "kept" && a.Target == "openai2_gpt-4o" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("alias target not rewritten: %+v", s.Aliases("openai"))
	}
	if err := s.SetModel("grok", "gpt-4o"); err != nil {
		t.Fatal(err)
	}
	s.MigratePrefixes([]PrefixDef{{ID: "openai", Base: "openai"}}, "grok")
	if s.Model("grok") != "openai2_gpt-4o" {
		t.Fatalf("helper %q", s.Model("grok"))
	}
	// a second run does not stack prefixes
	s.MigratePrefixes([]PrefixDef{{ID: "openai", Base: "openai"}}, "grok")
	if s.Model("grok") != "openai2_gpt-4o" || s.Aliases("openai")[0].Target == "openai2_openai2_gpt-4o" {
		t.Fatalf("stacked: helper %q aliases %+v", s.Model("grok"), s.Aliases("openai"))
	}
}

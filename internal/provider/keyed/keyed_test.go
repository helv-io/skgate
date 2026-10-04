package keyed

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/helv-io/skgate/internal/store"
)

func open(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func get(t *testing.T, db *store.DB, id string) *Provider {
	t.Helper()
	for _, p := range New(db) {
		if k, ok := Keyed(p); ok && k.ID() == id {
			return k
		}
	}
	t.Fatalf("no provider %s", id)
	return nil
}

func TestPresetsAreCompleteData(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Presets {
		if p.ID == "" || p.Name == "" || p.Hint == "" || seen[p.ID] || p.ID == "grok" {
			t.Errorf("bad preset %+v", p)
		}
		seen[p.ID] = true
		if !p.Custom && !strings.HasPrefix(p.Base, "http") {
			t.Errorf("%s: base %q", p.ID, p.Base)
		}
		if p.NeedsKey && p.Optional {
			t.Errorf("%s: a key cannot be both required and optional", p.ID)
		}
	}
	for _, id := range []string{"openai", "anthropic", "gemini", "mistral", "deepseek", "groq", "openrouter", "ollama", "lmstudio", "custom"} {
		if !seen[id] {
			t.Errorf("missing preset %s", id)
		}
	}
}

func TestKeyIsSealedAtRestAndReadyNeedsIt(t *testing.T) {
	db := open(t)
	p := get(t, db, "openai")
	if p.Ready() || p.Status().SignedIn {
		t.Fatal("not added yet")
	}
	p.SetEnabled(true)
	if p.Ready() {
		t.Fatal("a provider that needs a key is not ready without one")
	}
	if _, err := p.Token(context.Background()); err == nil {
		t.Fatal("expected an error without a key")
	}
	if err := p.SaveKey("  sk-secret-1234 "); err != nil {
		t.Fatal(err)
	}
	if !p.Ready() {
		t.Fatal("should be ready")
	}
	raw, _ := db.GetSetting("provider.openai.key")
	if raw == "" || strings.Contains(raw, "sk-secret") {
		t.Fatalf("key stored in the clear: %q", raw)
	}
	if tok, err := p.Token(context.Background()); err != nil || tok != "sk-secret-1234" {
		t.Fatalf("%q %v", tok, err)
	}
	if m := p.Status().AccessMasked; !strings.HasSuffix(m, "1234") || strings.Contains(m, "secret") {
		t.Fatalf("mask %q", m)
	}
	p.Remove()
	if p.Ready() || p.Enabled() {
		t.Fatal("remove forgets everything")
	}
	if _, ok := db.GetSetting("provider.openai.key"); ok {
		t.Fatal("key must be deleted")
	}
}

func TestLocalServerNeedsNoKey(t *testing.T) {
	db := open(t)
	p := get(t, db, "ollama")
	p.SetEnabled(true)
	if !p.Ready() {
		t.Fatal("ollama needs no key")
	}
	if tok, err := p.Token(context.Background()); err != nil || tok != "" {
		t.Fatalf("%q %v", tok, err)
	}
}

func TestGeminiModelIDsLoseTheirPrefix(t *testing.T) {
	p := get(t, open(t), "gemini")
	out := string(p.FilterModels([]byte(`{"object":"list","data":[{"id":"models/gemini-2.5-pro","object":"model"}]}`)))
	if !strings.Contains(out, `"gemini-2.5-pro"`) || strings.Contains(out, "models/") {
		t.Fatal(out)
	}
	o := get(t, open(t), "openai")
	in := `{"data":[{"id":"models/x"}]}`
	if string(o.FilterModels([]byte(in))) != in {
		t.Fatal("only Gemini is filtered")
	}
}

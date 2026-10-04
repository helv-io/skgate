package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type memKV struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *memKV) GetSetting(key string) (string, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[key]
	return v, ok
}
func (k *memKV) SetSetting(key, v string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[key] = v
	return nil
}

func clearRemoved(t *testing.T) {
	for _, k := range removedEnv {
		t.Setenv(k, "") // restores the original on cleanup
		os.Unsetenv(k)
	}
}

// Removed variables are ignored by Load; the ones that changed behavior move into the settings store once.
func TestMigrateLegacyEnv(t *testing.T) {
	clearRemoved(t)
	t.Setenv("MCP_ALLOW_QUERY_KEY", "true")
	t.Setenv("UPSTREAM_BASE", "https://up.example/v1/")
	t.Setenv("UPSTREAM_FALLBACK", "")
	t.Setenv("XAI_CLIENT_ID", "x")
	kv := &memKV{m: map[string]string{}}
	c := Load()
	c.Bind(kv)
	if _, ok := kv.m["allow_query_key"]; ok {
		t.Fatal("the variable itself must not enable ?key=")
	}
	MigrateLegacyEnv(kv)
	if kv.m["allow_query_key"] != "1" || kv.m["provider.grok.base"] != "https://up.example/v1" {
		t.Fatalf("not migrated: %v", kv.m)
	}
	if v, ok := kv.m["provider.grok.fallback"]; !ok || v != "" {
		t.Fatalf("an explicitly empty fallback must stay off: %q %v", v, ok)
	}
	// once only: a later change in the UI is not overwritten by a still-set variable
	kv.m["allow_query_key"] = "0"
	MigrateLegacyEnv(kv)
	if kv.m["allow_query_key"] != "0" {
		t.Fatal("the migration must not run twice")
	}
}

func TestMigrateLegacyEnvKeepsUIValuesAndUnsetIsNoop(t *testing.T) {
	clearRemoved(t)
	t.Setenv("MCP_ALLOW_QUERY_KEY", "1")
	kv := &memKV{m: map[string]string{"allow_query_key": "0"}}
	MigrateLegacyEnv(kv)
	if kv.m["allow_query_key"] != "0" {
		t.Fatal("a value already chosen in the UI wins")
	}
	clearRemoved(t)
	kv = &memKV{m: map[string]string{}}
	MigrateLegacyEnv(kv)
	if _, ok := kv.m["allow_query_key"]; ok {
		t.Fatal("unset variables store nothing")
	}
	if _, ok := kv.m["provider.grok.fallback"]; ok {
		t.Fatal("an unset fallback keeps the built-in default")
	}
}

// The README and docs describe the current configuration only: no removed variable is mentioned.
func TestDocsMentionNoRemovedVariables(t *testing.T) {
	files := []string{filepath.Join("..", "..", "README.md")}
	more, err := filepath.Glob(filepath.Join("..", "..", "docs", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, more...)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range removedEnv {
			if strings.Contains(string(b), k) {
				t.Errorf("%s mentions removed variable %s", f, k)
			}
		}
	}
}

type failKV struct {
	*memKV
	fail bool
}

func (k *failKV) SetSetting(key, v string) error {
	if k.fail && key != legacyEnvDone {
		return os.ErrPermission
	}
	return k.memKV.SetSetting(key, v)
}

// A write that failed is not recorded as done: the next start tries again.
func TestMigrateLegacyEnvRetriesAfterAFailedWrite(t *testing.T) {
	clearRemoved(t)
	t.Setenv("UPSTREAM_BASE", "https://up.example/v1")
	kv := &failKV{memKV: &memKV{m: map[string]string{}}, fail: true}
	MigrateLegacyEnv(kv)
	if _, done := kv.m[legacyEnvDone]; done {
		t.Fatal("marked done although a write failed")
	}
	kv.fail = false
	MigrateLegacyEnv(kv)
	if kv.m["provider.grok.base"] != "https://up.example/v1" || kv.m[legacyEnvDone] != "1" {
		t.Fatalf("not migrated on retry: %v", kv.m)
	}
}

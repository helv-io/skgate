package provider

import "testing"

func TestModelAliasesFromAList(t *testing.T) {
	raw := []byte(`{"data":[{"id":"a","aliases":["a-latest","b"," "]},{"id":"b","aliases":["a-latest-x","dup"]},{"id":"c","aliases":["dup"]},{"id":"d"}]}`)
	got := ModelAliases(raw)
	want := map[string]string{"a-latest": "a", "a-latest-x": "b"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s -> %q, want %q", k, got[k], v)
		}
	}
	if ModelAliases([]byte(`{"data":[{"id":"a"}]}`)) != nil || ModelAliases([]byte(`nope`)) != nil {
		t.Error("no aliases must give nil")
	}
}

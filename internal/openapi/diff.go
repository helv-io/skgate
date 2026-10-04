package openapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// Hash is the content hash of a description as fetched (hex SHA-256). It tells an update whether the address
// answers with the same text as the last time.
func Hash(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

// OpDiff is what differs between two versions of a description, by operation key ("GET /pets/{id}").
type OpDiff struct {
	Added, Removed, Changed []string
}

// Empty reports whether the operations are the same.
func (d OpDiff) Empty() bool { return len(d.Added)+len(d.Removed)+len(d.Changed) == 0 }

// DiffOps compares two lists of operations. An operation is changed when anything a tool is made of differs: its
// names, texts, parameters, body or whether it can be a tool at all.
func DiffOps(before, after []Op) OpDiff {
	fp := func(o Op) string { b, _ := json.Marshal(o); return string(b) }
	old := map[string]string{}
	for _, o := range before {
		old[o.Key] = fp(o)
	}
	var d OpDiff
	seen := map[string]bool{}
	for _, o := range after {
		seen[o.Key] = true
		prev, ok := old[o.Key]
		switch {
		case !ok:
			d.Added = append(d.Added, o.Key)
		case prev != fp(o):
			d.Changed = append(d.Changed, o.Key)
		}
	}
	for _, o := range before {
		if !seen[o.Key] {
			d.Removed = append(d.Removed, o.Key)
		}
	}
	sort.Strings(d.Added)
	sort.Strings(d.Removed)
	sort.Strings(d.Changed)
	return d
}

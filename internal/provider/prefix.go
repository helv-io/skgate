package provider

import (
	"errors"
	"strconv"
	"strings"
)

// A prefix is how a key-based provider's models are named in front of skgate: openai_gpt-4o.
// Grok has none, so its model ids stay as the provider lists them. A prefix is lowercase
// letters and digits, at most 16 characters, and unique across providers.

const PrefixMax = 16

// ValidPrefix reports whether s can be stored as a provider prefix.
func ValidPrefix(s string) bool {
	if s == "" || len(s) > PrefixMax {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// CheckPrefix is the refusal for an empty or illegal prefix.
func CheckPrefix(s string) error {
	if s == "" {
		return errors.New("enter a prefix")
	}
	if !ValidPrefix(s) {
		return errors.New("use lowercase letters and numbers, up to 16")
	}
	return nil
}

// SlugPrefix keeps lowercase letters and digits and cuts the result at 16 characters.
func SlugPrefix(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > PrefixMax {
		out = out[:PrefixMax]
	}
	return out
}

// UniquePrefix returns base, or base with a digit appended when base is taken (openai, openai2).
// The result is at most 16 characters. used is updated by the caller.
func UniquePrefix(base string, used map[string]bool) string {
	base = SlugPrefix(base)
	if base == "" {
		base = "p"
	}
	if !used[base] {
		return base
	}
	for n := 2; n < 10000; n++ {
		suf := strconv.Itoa(n)
		stem := base
		if len(stem)+len(suf) > PrefixMax {
			stem = stem[:PrefixMax-len(suf)]
		}
		cand := stem + suf
		if !used[cand] {
			return cand
		}
	}
	return base
}

// Expose is the model id clients see. An empty prefix leaves the id unchanged.
func Expose(prefix, id string) string {
	if prefix == "" || id == "" {
		return id
	}
	if _, ok := Bare(prefix, id); ok {
		return id
	}
	return prefix + "_" + id
}

// Bare removes prefix_ from name. The bool is false when name is not in that form.
func Bare(prefix, name string) (string, bool) {
	if prefix == "" || name == "" {
		return name, false
	}
	p := prefix + "_"
	if strings.HasPrefix(name, p) && len(name) > len(p) {
		return name[len(p):], true
	}
	return name, false
}

// Prefix is the provider's stored prefix ("" for Grok and for a provider that has none yet).
func (s Settings) Prefix(id string) string {
	v, _ := s.Get(id, "prefix")
	return v
}

// SetPrefix stores a prefix that CheckPrefix accepts.
func (s Settings) SetPrefix(id, prefix string) error {
	if err := CheckPrefix(prefix); err != nil {
		return err
	}
	return s.Set(id, "prefix", prefix)
}

// PrefixDef is one provider that should have a prefix, with the preset it starts from.
type PrefixDef struct {
	ID, Base string
}

// MigratePrefixes fills a missing prefix for each provider (a collision appends a digit) and
// rewrites that provider's alias targets to prefix_model. A helper model that names one of
// those targets is rewritten the same way. An alias name used as the helper is left as it is.
func (s Settings) MigratePrefixes(defs []PrefixDef, helperOwner string) {
	used := map[string]bool{}
	for _, d := range defs {
		if ValidPrefix(s.Prefix(d.ID)) {
			used[s.Prefix(d.ID)] = true
		}
	}
	for _, d := range defs {
		if ValidPrefix(s.Prefix(d.ID)) {
			continue
		}
		base := SlugPrefix(d.Base)
		if base == "" {
			base = SlugPrefix(d.ID)
		}
		pre := UniquePrefix(base, used)
		used[pre] = true
		_ = s.SetPrefix(d.ID, pre)
	}
	ids := make([]string, 0, len(defs)+1)
	ids = append(ids, helperOwner)
	for _, d := range defs {
		ids = append(ids, d.ID)
	}
	oldHelper := s.Model(helperOwner)
	newHelper := oldHelper
	if oldHelper != "" && !s.aliasName(ids, oldHelper) {
		for _, d := range defs {
			pre := s.Prefix(d.ID)
			for _, a := range s.Aliases(d.ID) {
				raw := a.Target
				if b, ok := Bare(pre, raw); ok {
					raw = b
				}
				if oldHelper == a.Target || oldHelper == raw {
					newHelper = Expose(pre, raw)
				}
			}
		}
	}
	for _, d := range defs {
		pre := s.Prefix(d.ID)
		if pre == "" {
			continue
		}
		list := s.Aliases(d.ID)
		changed := false
		for i := range list {
			raw := list[i].Target
			if b, ok := Bare(pre, raw); ok {
				raw = b
			}
			exposed := Expose(pre, raw)
			if list[i].Target != exposed {
				list[i].Target = exposed
				changed = true
			}
		}
		if changed {
			_ = s.saveAliases(d.ID, list)
		}
	}
	if newHelper != oldHelper {
		_ = s.SetModel(helperOwner, newHelper)
	}
}

func (s Settings) aliasName(ids []string, name string) bool {
	for _, id := range ids {
		if _, ok := findAlias(s.Aliases(id), name); ok {
			return true
		}
	}
	return false
}

package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Alias maps a synthetic model name to a real model of the provider.
type Alias struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

var aliasNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)

func findAlias(list []Alias, name string) (Alias, bool) {
	for _, a := range list {
		if a.Name == name {
			return a, true
		}
	}
	return Alias{}, false
}

func contains(ids []string, s string) bool {
	for _, id := range ids {
		if id == s {
			return true
		}
	}
	return false
}

// Aliases returns the provider's aliases in the order they are listed.
func (s Settings) Aliases(id string) []Alias {
	v, ok := s.Get(id, "aliases")
	if !ok || v == "" {
		return nil
	}
	var out []Alias
	if json.Unmarshal([]byte(v), &out) != nil {
		return nil
	}
	return out
}

func (s Settings) saveAliases(id string, list []Alias) error {
	if len(list) == 0 {
		return s.Delete(id, "aliases")
	}
	b, _ := json.Marshal(list)
	return s.Set(id, "aliases", string(b))
}

// PutAlias adds an alias or changes the target of an existing one. real is the provider's current
// model list: the target must be in it and the name must not equal any real id (case-insensitive).
func (s Settings) PutAlias(id, name, target string, real []string) error {
	name, target = strings.TrimSpace(name), strings.TrimSpace(target)
	if !aliasNameRE.MatchString(name) {
		return errors.New("alias: letters, digits and . _ : -, up to 64 characters, starting with a letter or digit")
	}
	if len(real) == 0 {
		return errors.New("model list unavailable, sign in and reload the models first")
	}
	for _, r := range real {
		if strings.EqualFold(r, name) {
			return fmt.Errorf("alias %q collides with the model id %q", name, r)
		}
	}
	if !contains(real, target) {
		return fmt.Errorf("target %q is not one of the provider's models", target)
	}
	list := s.Aliases(id)
	for i := range list {
		if list[i].Name == name {
			list[i].Target = target
			return s.saveAliases(id, list)
		}
	}
	return s.saveAliases(id, append(list, Alias{Name: name, Target: target}))
}

// DeleteAlias removes an alias.
func (s Settings) DeleteAlias(id, name string) error {
	var keep []Alias
	for _, a := range s.Aliases(id) {
		if a.Name != name {
			keep = append(keep, a)
		}
	}
	return s.saveAliases(id, keep)
}

// AliasIssue explains why an alias is not usable, or "" when it is. known is false when the model
// list has not been loaded; nothing can be judged then.
func AliasIssue(a Alias, real []string, known bool) string {
	if !known {
		return ""
	}
	for _, r := range real {
		if strings.EqualFold(r, a.Name) {
			return "the provider now lists a model named " + r + "; the alias wins"
		}
	}
	if !contains(real, a.Target) {
		return "target " + a.Target + " is no longer in the provider's model list"
	}
	return ""
}

// Model is the MCP helper model: the model skgate uses for its own calls (configuration suggestions).
func (s Settings) Model(id string) string { v, _ := s.Get(id, "model"); return v }

// SetModel stores the MCP helper model; empty clears it.
func (s Settings) SetModel(id, model string) error {
	if model == "" {
		return s.Delete(id, "model")
	}
	return s.Set(id, "model", model)
}

// ModelCache remembers the last model list per provider (from the proxy's list responses and from
// explicit reloads). It never makes network calls itself.
type ModelCache struct {
	mu sync.Mutex
	m  map[string]cached
}

type cached struct {
	ids []string
	at  time.Time
}

// NewModelCache returns an empty cache.
func NewModelCache() *ModelCache { return &ModelCache{m: map[string]cached{}} }

// Set stores a list.
func (c *ModelCache) Set(id string, ids []string) {
	if len(ids) == 0 {
		return
	}
	c.mu.Lock()
	c.m[id] = cached{ids: append([]string(nil), ids...), at: time.Now()}
	c.mu.Unlock()
}

// Get returns the cached list and when it was loaded (ok false when there is none).
func (c *ModelCache) Get(id string) (ids []string, at time.Time, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[id]
	return append([]string(nil), e.ids...), e.at, ok
}

// Delete forgets the list of a provider (when it is removed).
func (c *ModelCache) Delete(id string) {
	c.mu.Lock()
	delete(c.m, id)
	c.mu.Unlock()
}

package openapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Patch is one change to a description: set a value or remove one, at a JSON pointer.
type Patch struct {
	Op     string `json:"op"` // set or remove
	Path   string `json:"path"`
	Value  any    `json:"value,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Change is a patch as shown for approval: what stood there and what stands there afterwards.
type Change struct {
	Path   string `json:"path"`
	Op     string `json:"op"`
	Before string `json:"before"`
	After  string `json:"after"`
	Reason string `json:"reason"`
}

// MaxPatches bounds one repair.
const MaxPatches = 200

// Apply returns a copy of the description with the patches applied, and the changes made. A patch whose parent
// does not exist is refused: a repair edits what is there, it does not invent structure. The first bad patch stops
// the whole repair, so an approval never covers half a fix.
func (d *Doc) Apply(patches []Patch) (*Doc, []Change, error) {
	if len(patches) > MaxPatches {
		return nil, nil, fmt.Errorf("a repair of more than %d changes is refused", MaxPatches)
	}
	var cp map[string]any
	b, _ := json.Marshal(d.Raw)
	_ = json.Unmarshal(b, &cp)
	out := &Doc{Raw: cp, Source: d.Source}
	var changes []Change
	for _, p := range patches {
		if !strings.HasPrefix(p.Path, "/") || p.Path == "/" {
			return nil, nil, fmt.Errorf("bad path %q", p.Path)
		}
		before := pointer(out.Raw, p.Path)
		switch p.Op {
		case "set":
			if err := setPointer(out.Raw, p.Path, p.Value); err != nil {
				return nil, nil, fmt.Errorf("%s: %w", p.Path, err)
			}
			changes = append(changes, Change{Path: p.Path, Op: "set", Before: show(before), After: show(p.Value), Reason: p.Reason})
		case "remove":
			if before == nil {
				return nil, nil, fmt.Errorf("%s: nothing to remove", p.Path)
			}
			if err := removePointer(out.Raw, p.Path); err != nil {
				return nil, nil, fmt.Errorf("%s: %w", p.Path, err)
			}
			changes = append(changes, Change{Path: p.Path, Op: "remove", Before: show(before), After: "", Reason: p.Reason})
		default:
			return nil, nil, fmt.Errorf("%s: unknown operation %q", p.Path, p.Op)
		}
	}
	return out, changes, nil
}

func show(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return clip(s, 300)
	}
	b, _ := json.Marshal(v)
	return clip(string(b), 300)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

func splitPtr(ptr string) (parent []string, last string) {
	segs := strings.Split(strings.TrimPrefix(ptr, "/"), "/")
	for i := range segs {
		segs[i] = unescPtr(segs[i])
	}
	return segs[:len(segs)-1], segs[len(segs)-1]
}

func descend(root any, segs []string) (any, error) {
	cur := root
	for _, s := range segs {
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[s]
			if !ok {
				return nil, errors.New("the parent does not exist")
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(s)
			if err != nil || i < 0 || i >= len(c) {
				return nil, errors.New("the parent does not exist")
			}
			cur = c[i]
		default:
			return nil, errors.New("the parent is not an object or a list")
		}
	}
	return cur, nil
}

func setPointer(root map[string]any, ptr string, v any) error {
	parent, last := splitPtr(ptr)
	p, err := descend(root, parent)
	if err != nil {
		return err
	}
	switch c := p.(type) {
	case map[string]any:
		c[last] = v
	case []any:
		i, err := strconv.Atoi(last)
		if err != nil || i < 0 || i >= len(c) {
			return errors.New("no such list element")
		}
		c[i] = v
	default:
		return errors.New("the parent is not an object or a list")
	}
	return nil
}

func removePointer(root map[string]any, ptr string) error {
	parent, last := splitPtr(ptr)
	// A list element is removed through its parent's slot, which needs the grandparent.
	p, err := descend(root, parent)
	if err != nil {
		return err
	}
	switch c := p.(type) {
	case map[string]any:
		delete(c, last)
	case []any:
		i, err := strconv.Atoi(last)
		if err != nil || i < 0 || i >= len(c) {
			return errors.New("no such list element")
		}
		if len(parent) == 0 {
			return errors.New("cannot remove from the root")
		}
		nl := append(append([]any{}, c[:i]...), c[i+1:]...)
		return setPointer(root, "/"+joinPtr(parent), nl)
	default:
		return errors.New("the parent is not an object or a list")
	}
	return nil
}

func joinPtr(segs []string) string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = escPtr(s)
	}
	return strings.Join(out, "/")
}

// IssueContext returns the JSON of the object around an issue, clipped, for a repair prompt.
func (d *Doc) IssueContext(is Issue, max int) string {
	parent, _ := splitPtr(is.Path)
	node := pointer(d.Raw, "/"+joinPtr(parent))
	if node == nil {
		return ""
	}
	b, _ := json.Marshal(node)
	return clip(string(b), max)
}

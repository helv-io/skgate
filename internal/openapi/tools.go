package openapi

import (
	"fmt"
	"strings"
)

// Override is a name and description the admin (or an assistant) chose for an operation's tool.
type Override struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// Selection is which operations are exposed as tools, by Op.Key, and the names and descriptions chosen for them.
type Selection struct {
	Enabled   map[string]bool     `json:"enabled"`
	Overrides map[string]Override `json:"overrides"`
}

// Tool is an MCP tool made of an operation.
type Tool struct {
	Name        string
	Title       string
	Description string
	InputSchema map[string]any
	ReadOnly    bool
	Destructive bool
	Op          Op
}

// MaxDescription is the longest tool description.
const MaxDescription = 1000

// DefaultDescription is what a tool says about itself without an override: the summary, else the first
// paragraph of the description, else the method and path.
func DefaultDescription(o Op) string {
	d := o.Summary
	if d == "" {
		d = strings.TrimSpace(strings.SplitN(o.Description, "\n\n", 2)[0])
	}
	if d == "" {
		return o.Method + " " + o.Path
	}
	return clip(d, MaxDescription)
}

// Tools builds the tools of the enabled operations. Names are unique: a clash gets a numeric suffix. An operation
// that cannot be a tool (Skip) is left out even if enabled.
func Tools(ops []Op, sel Selection) []Tool {
	var out []Tool
	used := map[string]bool{}
	for _, o := range ops {
		if !sel.Enabled[o.Key] || o.Skip != "" {
			continue
		}
		ov := sel.Overrides[o.Key]
		name := CleanName(ov.Name)
		if name == "" {
			name = o.ID
		}
		base := name
		for n := 2; used[name]; n++ {
			suffix := fmt.Sprintf("_%d", n)
			name = clipName(base, MaxNameLen-len(suffix)) + suffix
		}
		used[name] = true
		desc := strings.TrimSpace(ov.Description)
		if desc == "" {
			desc = DefaultDescription(o)
		}
		out = append(out, Tool{Name: name, Title: o.Method + " " + o.Path, Description: clip(desc, MaxDescription), InputSchema: inputSchema(o),
			ReadOnly: o.Method == "GET" || o.Method == "HEAD", Destructive: o.Method == "DELETE", Op: o})
	}
	return out
}

func clipName(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// DefaultName is the name an operation's tool gets without an override (before clash suffixes).
func DefaultName(o Op) string { return o.ID }

func inputSchema(o Op) map[string]any {
	props := map[string]any{}
	var required []any
	for _, p := range o.Params {
		s := map[string]any{}
		for k, v := range p.Schema {
			s[k] = v
		}
		desc := p.Desc
		if desc != "" {
			s["description"] = clip(desc, 400)
		}
		if p.In == "header" {
			if s["description"] == nil {
				s["description"] = "sent as the HTTP header " + p.Name
			}
		}
		props[p.Arg] = s
		if p.Required {
			required = append(required, p.Arg)
		}
	}
	if o.Body != nil {
		s := map[string]any{}
		for k, v := range o.Body.Schema {
			s[k] = v
		}
		if s["description"] == nil && o.Body.Desc != "" {
			s["description"] = clip(o.Body.Desc, 400)
		}
		props["body"] = s
		if o.Body.Required {
			required = append(required, "body")
		}
	}
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

// WithoutCredential returns the tool without the parameters the upstream's own credential fills in. Many
// descriptions declare their API key as a header or query parameter; a model must neither be asked for it nor
// be able to make the call fail by not providing it.
func (t Tool) WithoutCredential(a Auth) Tool {
	if a.Name == "" || (a.Kind != AuthHeader && a.Kind != AuthQuery) {
		return t
	}
	in := "header"
	if a.Kind == AuthQuery {
		in = "query"
	}
	var keep []Param
	for _, p := range t.Op.Params {
		if p.In == in && strings.EqualFold(p.Name, a.Name) && (in == "header" || p.Name == a.Name) {
			continue
		}
		keep = append(keep, p)
	}
	if len(keep) == len(t.Op.Params) {
		return t
	}
	op := t.Op
	op.Params = keep
	t.Op = op
	t.InputSchema = inputSchema(op)
	return t
}

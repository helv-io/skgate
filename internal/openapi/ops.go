package openapi

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Param is one argument taken from the URL, query string or headers.
type Param struct {
	Arg      string // the tool argument name
	Name     string // the name on the wire
	In       string // path, query or header
	Required bool
	Style    string
	Explode  *bool
	Schema   map[string]any
	Desc     string
}

// Body is the request body of an operation.
type Body struct {
	Type     string // content type sent: application/json, application/x-www-form-urlencoded or text/plain
	Required bool
	Schema   map[string]any
	Desc     string
}

// Op is one operation of the description.
type Op struct {
	Key         string // "GET /pets/{id}": stable across edits of the description, the key of selections
	Method      string // upper case
	Path        string
	ID          string // the operationId, or a name made from method and path
	Summary     string
	Description string
	Tags        []string
	Deprecated  bool
	Params      []Param
	Body        *Body
	// Skip says why no tool can be made of the operation ("" when one can): a file upload, or an unreadable body.
	Skip string
}

var nameRE = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// MaxNameLen is the longest tool name.
const MaxNameLen = 64

// CleanName makes a tool name of free text: letters, digits, underscore and dash, at most 64 characters.
func CleanName(s string) string {
	s = strings.Trim(nameRE.ReplaceAllString(strings.TrimSpace(s), "_"), "_")
	if len(s) > MaxNameLen {
		s = strings.TrimRight(s[:MaxNameLen], "_-")
	}
	return s
}

// ValidName reports whether s is acceptable as a tool name as it is.
func ValidName(s string) bool { return s != "" && CleanName(s) == s }

// generatedID builds a name from method and path: get_pets_petId.
func generatedID(method, path string) string {
	var parts []string
	for _, seg := range strings.Split(path, "/") {
		seg = strings.Trim(seg, "{}")
		if seg != "" {
			parts = append(parts, seg)
		}
	}
	return CleanName(strings.ToLower(method) + "_" + strings.Join(parts, "_"))
}

// Operations lists every operation in document order (paths sorted, methods in the usual order).
func (d *Doc) Operations() []Op {
	var out []Op
	paths := obj(d.Raw["paths"])
	for _, p := range sortedKeys(paths) {
		item := obj(paths[p])
		for _, m := range methods {
			opm := obj(item[m])
			if opm == nil {
				continue
			}
			op := Op{Key: strings.ToUpper(m) + " " + p, Method: strings.ToUpper(m), Path: p,
				Summary: strings.TrimSpace(str(opm["summary"])), Description: strings.TrimSpace(str(opm["description"]))}
			op.Deprecated, _ = opm["deprecated"].(bool)
			for _, t := range list(opm["tags"]) {
				op.Tags = append(op.Tags, str(t))
			}
			op.ID = CleanName(str(opm["operationId"]))
			if op.ID == "" {
				op.ID = generatedID(m, p)
			}
			op.Params = d.params(item, opm, p)
			op.Body, op.Skip = d.body(opm)
			out = append(out, op)
		}
	}
	return out
}

func (d *Doc) deref(v any) map[string]any {
	m := obj(v)
	for i := 0; i < 5 && m != nil && str(m["$ref"]) != ""; i++ {
		m = obj(lookup(d.Raw, str(m["$ref"])))
	}
	return m
}

func (d *Doc) params(item, op map[string]any, path string) []Param {
	var merged []map[string]any
	idx := map[string]int{}
	add := func(l []any) {
		for _, x := range l {
			pm := d.deref(x)
			if pm == nil || str(pm["name"]) == "" {
				continue
			}
			k := str(pm["in"]) + ":" + str(pm["name"])
			if i, ok := idx[k]; ok {
				merged[i] = pm
				continue
			}
			idx[k] = len(merged)
			merged = append(merged, pm)
		}
	}
	add(list(item["parameters"]))
	add(list(op["parameters"]))
	var out []Param
	used := map[string]bool{"body": true}
	for _, pm := range merged {
		in := str(pm["in"])
		if in != "path" && in != "query" && in != "header" {
			continue // cookies are not offered
		}
		name := str(pm["name"])
		if in == "header" && isAuthHeader(name) {
			continue // credentials are the upstream's, never a model's
		}
		p := Param{Name: name, In: in, Desc: strings.TrimSpace(str(pm["description"])), Style: str(pm["style"])}
		p.Required, _ = pm["required"].(bool)
		if in == "path" {
			p.Required = true
		}
		if e, ok := pm["explode"].(bool); ok {
			p.Explode = &e
		}
		p.Schema = d.Schema(pm["schema"])
		if p.Schema == nil {
			p.Schema = map[string]any{"type": "string"}
		}
		arg := CleanName(strings.NewReplacer("[", "_", "]", "").Replace(name))
		if arg == "" {
			arg = "param"
		}
		for base, n := arg, 2; used[arg]; n++ {
			arg = fmt.Sprintf("%s_%d", base, n)
		}
		if in != "path" && used[arg] {
			arg = in + "_" + arg
		}
		used[arg] = true
		p.Arg = arg
		out = append(out, p)
	}
	return out
}

func isAuthHeader(n string) bool {
	switch strings.ToLower(n) {
	case "authorization", "proxy-authorization", "cookie", "host", "content-length", "content-type", "accept":
		return true
	}
	return false
}

// body picks the request body. JSON wins; form-urlencoded and plain text follow. A body that can only be sent as
// multipart or a binary file is a file upload: the operation is skipped.
func (d *Doc) body(op map[string]any) (*Body, string) {
	rb := d.deref(op["requestBody"])
	if rb == nil {
		return nil, ""
	}
	content := obj(rb["content"])
	required, _ := rb["required"].(bool)
	desc := strings.TrimSpace(str(rb["description"]))
	pick := func(match func(string) bool) (string, map[string]any) {
		for _, ct := range sortedKeys(content) {
			if match(strings.ToLower(ct)) {
				return ct, obj(content[ct])
			}
		}
		return "", nil
	}
	if ct, m := pick(func(s string) bool { return strings.Contains(s, "json") }); ct != "" {
		return &Body{Type: "application/json", Required: required, Schema: d.Schema(m["schema"]), Desc: desc}, ""
	}
	if _, m := pick(func(s string) bool { return s == "application/x-www-form-urlencoded" }); m != nil {
		return &Body{Type: "application/x-www-form-urlencoded", Required: required, Schema: d.Schema(m["schema"]), Desc: desc}, ""
	}
	if _, m := pick(func(s string) bool { return strings.HasPrefix(s, "text/") }); m != nil {
		return &Body{Type: "text/plain", Required: required, Schema: map[string]any{"type": "string"}, Desc: desc}, ""
	}
	if len(content) == 0 {
		return nil, ""
	}
	return nil, "file upload or binary body"
}

// Tags lists the tags used, sorted.
func Tags(ops []Op) []string {
	seen := map[string]bool{}
	for _, o := range ops {
		for _, t := range o.Tags {
			seen[t] = true
		}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

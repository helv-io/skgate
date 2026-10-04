package openapi

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Issue is one thing wrong with a description. Fixable issues are the ones an assistant may propose a repair for;
// none of them stops an import: skgate reads what it can.
type Issue struct {
	Code    string `json:"code"` // no-paths, no-operations, missing-operation-id, duplicate-operation-id, broken-ref, invalid-type, invalid-parameter
	Path    string `json:"path"` // JSON pointer into the normalized description
	Message string `json:"message"`
	Fatal   bool   `json:"fatal"` // nothing can be offered from this description
}

var validTypes = map[string]bool{"string": true, "number": true, "integer": true, "boolean": true, "array": true, "object": true, "null": true}

var methods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// Validate checks the structure the tools are built from. Issues come in document order.
func (d *Doc) Validate() []Issue {
	var out []Issue
	paths := obj(d.Raw["paths"])
	if len(paths) == 0 {
		return []Issue{{Code: "no-paths", Path: "/paths", Message: "the description has no paths", Fatal: true}}
	}
	seenID := map[string]string{}
	ops := 0
	for _, p := range sortedKeys(paths) {
		item := obj(paths[p])
		for _, m := range methods {
			op := obj(item[m])
			if op == nil {
				continue
			}
			ops++
			base := "/paths/" + escPtr(p) + "/" + m
			id := str(op["operationId"])
			switch {
			case id == "":
				out = append(out, Issue{Code: "missing-operation-id", Path: base + "/operationId", Message: strings.ToUpper(m) + " " + p + " has no operationId"})
			case seenID[id] != "":
				out = append(out, Issue{Code: "duplicate-operation-id", Path: base + "/operationId", Message: fmt.Sprintf("operationId %q is also used by %s", id, seenID[id])})
			default:
				seenID[id] = strings.ToUpper(m) + " " + p
			}
			for i, x := range list(op["parameters"]) {
				out = append(out, d.checkParam(base+"/parameters/"+fmt.Sprint(i), x, p)...)
			}
		}
		for i, x := range list(item["parameters"]) {
			out = append(out, d.checkParam("/paths/"+escPtr(p)+"/parameters/"+fmt.Sprint(i), x, p)...)
		}
	}
	if ops == 0 {
		return append(out, Issue{Code: "no-operations", Path: "/paths", Message: "the paths have no operations (get, post, ...)", Fatal: true})
	}
	walk(d.Raw, "", func(ptr string, node map[string]any) {
		if r, ok := node["$ref"].(string); ok {
			if strings.HasPrefix(r, "#") && lookup(d.Raw, r) == nil {
				out = append(out, Issue{Code: "broken-ref", Path: ptr + "/$ref", Message: fmt.Sprintf("the reference %s points at nothing", r)})
			}
		}
		if t, ok := node["type"]; ok && looksLikeSchema(node) {
			switch tv := t.(type) {
			case string:
				if !validTypes[tv] {
					out = append(out, Issue{Code: "invalid-type", Path: ptr + "/type", Message: fmt.Sprintf("%q is not a JSON Schema type", tv)})
				}
			case []any:
				for _, e := range tv {
					if s, ok := e.(string); !ok || !validTypes[s] {
						out = append(out, Issue{Code: "invalid-type", Path: ptr + "/type", Message: fmt.Sprintf("%v is not a JSON Schema type", e)})
						break
					}
				}
			default:
				out = append(out, Issue{Code: "invalid-type", Path: ptr + "/type", Message: "type must be a string"})
			}
		}
	})
	return out
}

// looksLikeSchema tells a schema's "type" from a property that happens to be named type (inside properties the
// value is itself a schema, so the walk reaches it at its own level).
func looksLikeSchema(n map[string]any) bool {
	switch n["type"].(type) {
	case string, []any:
		return true
	}
	// a non-string type on a node with schema companions is an error worth reporting
	for _, k := range []string{"properties", "items", "format", "enum", "required"} {
		if _, ok := n[k]; ok {
			return true
		}
	}
	return false
}

var pathVarRE = regexp.MustCompile(`\{([^}]+)\}`)

func (d *Doc) checkParam(ptr string, x any, path string) []Issue {
	pm := obj(x)
	if pm == nil {
		return []Issue{{Code: "invalid-parameter", Path: ptr, Message: "a parameter must be an object"}}
	}
	if r := str(pm["$ref"]); r != "" {
		if lookup(d.Raw, r) == nil {
			return nil // reported as a broken reference
		}
		pm = obj(lookup(d.Raw, r))
	}
	name, in := str(pm["name"]), str(pm["in"])
	switch {
	case name == "" || in == "":
		return []Issue{{Code: "invalid-parameter", Path: ptr, Message: "a parameter needs a name and an \"in\" (path, query, header or cookie)"}}
	case in != "path" && in != "query" && in != "header" && in != "cookie":
		return []Issue{{Code: "invalid-parameter", Path: ptr + "/in", Message: fmt.Sprintf("a parameter cannot be \"in\" %q", in)}}
	case in == "path" && !strings.Contains(path, "{"+name+"}"):
		return []Issue{{Code: "invalid-parameter", Path: ptr + "/name", Message: fmt.Sprintf("path parameter %q is not in the path %s", name, path)}}
	}
	return nil
}

// walk calls fn for every JSON object in the tree with its pointer. Examples and extensions (x-) are skipped: their
// content is data. So are the security schemes, which have a "type" of their own.
func walk(v any, ptr string, fn func(string, map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		fn(ptr, x)
		for _, k := range sortedKeys(x) {
			if k == "example" || k == "examples" || k == "default" || k == "enum" || k == "const" || strings.HasPrefix(k, "x-") {
				continue
			}
			if k == "securitySchemes" && ptr == "/components" { // their "type" is apiKey, http, oauth2: not a schema's
				continue
			}
			walk(x[k], ptr+"/"+escPtr(k), fn)
		}
	case []any:
		for i, e := range x {
			walk(e, fmt.Sprintf("%s/%d", ptr, i), fn)
		}
	}
}

func escPtr(s string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(s)
}

func unescPtr(s string) string {
	return strings.NewReplacer("~1", "/", "~0", "~").Replace(s)
}

// lookup follows an internal reference ("#/components/schemas/Pet"), nil if it points at nothing.
func lookup(root map[string]any, ref string) any {
	if ref == "#" {
		return root
	}
	p, ok := strings.CutPrefix(ref, "#/")
	if !ok {
		return nil
	}
	if u, err := url.PathUnescape(p); err == nil { // a reference is a URI fragment: percent-encoded
		p = u
	}
	return pointer(root, "/"+p)
}

// pointer reads a JSON pointer, nil when absent.
func pointer(root any, ptr string) any {
	cur := root
	if ptr == "" {
		return cur
	}
	for _, seg := range strings.Split(strings.TrimPrefix(ptr, "/"), "/") {
		seg = unescPtr(seg)
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[seg]
			if !ok {
				return nil
			}
			cur = v
		case []any:
			i := 0
			if _, err := fmt.Sscanf(seg, "%d", &i); err != nil || i < 0 || i >= len(c) {
				return nil
			}
			cur = c[i]
		default:
			return nil
		}
	}
	return cur
}

func resolveRef(base, ref string) string {
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}

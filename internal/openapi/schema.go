package openapi

import "strings"

const maxSchemaDepth = 5

// Schema inlines the references of a schema and removes what a model does not need (examples, XML hints), so the
// result stands alone as a tool's input schema. Cycles and very deep schemas end in a plain object.
func (d *Doc) Schema(v any) map[string]any {
	m := obj(v)
	if m == nil {
		return nil
	}
	return d.schema(m, 0, nil)
}

var dropKeys = map[string]bool{"xml": true, "externalDocs": true, "example": true, "examples": true, "discriminator": true, "deprecated": true, "readOnly": true, "writeOnly": true}

func (d *Doc) schema(m map[string]any, depth int, stack []string) map[string]any {
	if r := str(m["$ref"]); r != "" {
		for _, s := range stack {
			if s == r {
				return map[string]any{"type": "object"}
			}
		}
		target := obj(lookup(d.Raw, r))
		if target == nil || depth >= maxSchemaDepth {
			return map[string]any{"type": "object"}
		}
		return d.schema(target, depth+1, append(stack, r))
	}
	out := map[string]any{}
	for k, v := range m {
		if dropKeys[k] || strings.HasPrefix(k, "x-") {
			continue
		}
		switch k {
		case "properties":
			props := map[string]any{}
			for name, pv := range obj(v) {
				if pm := obj(pv); pm != nil {
					props[name] = d.schema(pm, depth+1, stack)
				}
			}
			out[k] = props
		case "items", "additionalProperties", "not":
			if sm := obj(v); sm != nil {
				out[k] = d.schema(sm, depth+1, stack)
			} else {
				out[k] = v
			}
		case "allOf", "anyOf", "oneOf":
			var l []any
			for _, e := range list(v) {
				if em := obj(e); em != nil {
					l = append(l, d.schema(em, depth+1, stack))
				}
			}
			out[k] = l
		default:
			out[k] = v
		}
	}
	if all, ok := out["allOf"].([]any); ok { // merge: models read one object better than a list of parts
		delete(out, "allOf")
		props, _ := out["properties"].(map[string]any)
		if props == nil {
			props = map[string]any{}
		}
		var req []any
		req = append(req, list(out["required"])...)
		for _, e := range all {
			em := obj(e)
			for k, pv := range obj(em["properties"]) {
				props[k] = pv
			}
			req = append(req, list(em["required"])...)
			for k, v := range em {
				if k != "properties" && k != "required" {
					if _, has := out[k]; !has {
						out[k] = v
					}
				}
			}
		}
		if len(props) > 0 {
			out["properties"] = props
		}
		if len(req) > 0 {
			out["required"] = req
		}
	}
	// A type the model cannot use is dropped (the issue is reported separately); nullable becomes a type pair.
	switch t := out["type"].(type) {
	case string:
		if !validTypes[t] {
			delete(out, "type")
		} else if nb, _ := out["nullable"].(bool); nb && t != "null" {
			out["type"] = []any{t, "null"}
		}
	case []any:
	default:
		delete(out, "type")
	}
	delete(out, "nullable")
	if req, ok := out["required"].([]any); ok { // required is a list of names; anything else is dropped
		if props, ok2 := out["properties"].(map[string]any); ok2 {
			var keep []any
			for _, r := range req {
				if s, isStr := r.(string); isStr {
					if _, has := props[s]; has {
						keep = append(keep, s)
					}
				}
			}
			if len(keep) > 0 {
				out["required"] = keep
			} else {
				delete(out, "required")
			}
		} else if _, isBool := out["required"].(bool); isBool {
			delete(out, "required")
		}
	} else if _, isBool := out["required"].(bool); isBool {
		delete(out, "required")
	}
	return out
}

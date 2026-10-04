package openapi

import "strings"

// convertSwagger turns a Swagger 2 document into OpenAPI 3: servers from host/basePath/schemes, definitions and
// parameters into components, body and form parameters into request bodies. Security definitions become security
// schemes and the security requirements are kept, so the way to authenticate can be read from either version.
func convertSwagger(in map[string]any) map[string]any {
	out := map[string]any{"openapi": "3.0.3"}
	for _, k := range []string{"info", "tags", "externalDocs"} {
		if v, ok := in[k]; ok {
			out[k] = v
		}
	}
	if host := str(in["host"]); host != "" {
		schemes := []string{"https"}
		if l := list(in["schemes"]); len(l) > 0 {
			schemes = nil
			for _, s := range l {
				schemes = append(schemes, str(s))
			}
		}
		var servers []any
		for _, sc := range schemes {
			servers = append(servers, map[string]any{"url": sc + "://" + host + str(in["basePath"])})
		}
		out["servers"] = servers
	} else if bp := str(in["basePath"]); bp != "" {
		out["servers"] = []any{map[string]any{"url": bp}}
	}
	comps := map[string]any{}
	if d := obj(in["definitions"]); d != nil {
		comps["schemas"] = d
	}
	if p := obj(in["parameters"]); p != nil {
		cp := map[string]any{}
		for k, v := range p {
			if pm := obj(v); pm != nil && str(pm["in"]) != "body" && str(pm["in"]) != "formData" {
				cp[k] = convertParam(pm)
			}
		}
		comps["parameters"] = cp
	}
	if r := obj(in["responses"]); r != nil {
		comps["responses"] = r
	}
	if sd := obj(in["securityDefinitions"]); sd != nil {
		ss := map[string]any{}
		for k, v := range sd {
			if m := obj(v); m != nil {
				ss[k] = convertScheme(m)
			}
		}
		comps["securitySchemes"] = ss
	}
	if sec := list(in["security"]); sec != nil {
		out["security"] = sec
	}
	if len(comps) > 0 {
		out["components"] = comps
	}
	globalConsumes := strs(in["consumes"])
	paths := map[string]any{}
	for p, item := range obj(in["paths"]) {
		im := obj(item)
		if im == nil {
			continue
		}
		np := map[string]any{}
		for k, v := range im {
			switch {
			case k == "parameters":
				var ps []any
				for _, x := range list(v) {
					if pm := obj(x); pm != nil && str(pm["in"]) != "body" && str(pm["in"]) != "formData" {
						ps = append(ps, convertParam(pm))
					} else if pm != nil && str(pm["$ref"]) != "" {
						ps = append(ps, pm)
					}
				}
				np["parameters"] = ps
			case isMethod(k):
				np[k] = convertOperation(obj(v), globalConsumes)
			default:
				np[k] = v
			}
		}
		paths[p] = np
	}
	out["paths"] = paths
	return rewriteRefs(out).(map[string]any)
}

// convertScheme turns a Swagger 2 security definition into an OpenAPI 3 security scheme: basic becomes http basic,
// apiKey and oauth2 keep their type.
func convertScheme(m map[string]any) map[string]any {
	if str(m["type"]) == "basic" {
		return map[string]any{"type": "http", "scheme": "basic"}
	}
	return m
}

func isMethod(k string) bool {
	switch strings.ToLower(k) {
	case "get", "put", "post", "delete", "options", "head", "patch", "trace":
		return true
	}
	return false
}

func strs(v any) []string {
	var out []string
	for _, x := range list(v) {
		out = append(out, str(x))
	}
	return out
}

// convertParam moves the type fields of a Swagger parameter into a schema.
func convertParam(p map[string]any) map[string]any {
	if str(p["$ref"]) != "" {
		return p
	}
	out := map[string]any{}
	schema := map[string]any{}
	for k, v := range p {
		switch k {
		case "type", "format", "items", "enum", "default", "minimum", "maximum", "minLength", "maxLength", "pattern", "minItems", "maxItems":
			schema[k] = v
		case "collectionFormat":
			// csv is the OpenAPI 3 default (style form, explode false); multi means explode
			if str(v) == "multi" {
				out["explode"] = true
			} else {
				out["explode"] = false
			}
		default:
			out[k] = v
		}
	}
	if len(schema) > 0 {
		out["schema"] = schema
	}
	return out
}

func convertOperation(op map[string]any, globalConsumes []string) map[string]any {
	out := map[string]any{}
	for k, v := range op {
		switch k {
		case "parameters", "consumes", "produces", "schemes":
		default:
			out[k] = v
		}
	}
	consumes := strs(op["consumes"])
	if len(consumes) == 0 {
		consumes = globalConsumes
	}
	var params []any
	var body map[string]any
	form := map[string]any{}
	var formRequired []any
	fileUpload := false
	for _, x := range list(op["parameters"]) {
		pm := obj(x)
		if pm == nil {
			continue
		}
		switch str(pm["in"]) {
		case "body":
			body = pm
		case "formData":
			name := str(pm["name"])
			prop := obj(convertParam(withoutKeys(pm, "name", "in", "required"))["schema"])
			if prop == nil {
				prop = map[string]any{}
			}
			if str(pm["type"]) == "file" {
				prop = map[string]any{"type": "string", "format": "binary"}
				fileUpload = true
			}
			if d := str(pm["description"]); d != "" {
				prop["description"] = d
			}
			form[name] = prop
			if b, _ := pm["required"].(bool); b {
				formRequired = append(formRequired, name)
			}
		default:
			params = append(params, convertParam(pm))
		}
	}
	if len(params) > 0 {
		out["parameters"] = params
	}
	switch {
	case body != nil:
		cts := consumes
		if len(cts) == 0 {
			cts = []string{"application/json"}
		}
		content := map[string]any{}
		for _, ct := range cts {
			content[ct] = map[string]any{"schema": body["schema"]}
		}
		rb := map[string]any{"content": content}
		if r, _ := body["required"].(bool); r {
			rb["required"] = true
		}
		out["requestBody"] = rb
	case len(form) > 0:
		ct := "application/x-www-form-urlencoded"
		if fileUpload {
			ct = "multipart/form-data"
		} else {
			for _, c := range consumes {
				if strings.HasPrefix(c, "multipart/") {
					ct = c
				}
			}
		}
		schema := map[string]any{"type": "object", "properties": form}
		if len(formRequired) > 0 {
			schema["required"] = formRequired
		}
		out["requestBody"] = map[string]any{"content": map[string]any{ct: map[string]any{"schema": schema}}}
	}
	return out
}

func withoutKeys(m map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		skip := false
		for _, x := range keys {
			skip = skip || k == x
		}
		if !skip {
			out[k] = v
		}
	}
	return out
}

// rewriteRefs points Swagger 2 references at their OpenAPI 3 places.
func rewriteRefs(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if k == "$ref" {
				if s, ok := e.(string); ok {
					s = strings.Replace(s, "#/definitions/", "#/components/schemas/", 1)
					s = strings.Replace(s, "#/parameters/", "#/components/parameters/", 1)
					s = strings.Replace(s, "#/responses/", "#/components/responses/", 1)
					x[k] = s
				}
				continue
			}
			x[k] = rewriteRefs(e)
		}
	case []any:
		for i, e := range x {
			x[i] = rewriteRefs(e)
		}
	}
	return v
}

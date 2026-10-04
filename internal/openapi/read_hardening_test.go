package openapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// A description whose references fan out (1,000 properties, each a reference to the next schema, three deep) is
// about 100 KB of text but would be a billion schema nodes. It must come out small and quickly.
func TestSchemaFanOutIsBounded(t *testing.T) {
	schemas := map[string]any{}
	for i, name := range []string{"A", "B", "C", "D"} {
		props := map[string]any{}
		for n := 0; n < 1000; n++ {
			if i < 3 {
				props[fmt.Sprintf("p%d", n)] = map[string]any{"$ref": "#/components/schemas/" + []string{"A", "B", "C", "D"}[i+1]}
			} else {
				props[fmt.Sprintf("p%d", n)] = map[string]any{"type": "string"}
			}
		}
		schemas[name] = map[string]any{"type": "object", "properties": props}
	}
	spec, _ := json.Marshal(map[string]any{"openapi": "3.0.0", "info": map[string]any{"title": "x"},
		"paths": map[string]any{"/a": map[string]any{"post": map[string]any{"requestBody": map[string]any{"content": map[string]any{
			"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/A"}}}}}}},
		"components": map[string]any{"schemas": schemas}})
	d := mustParse(t, string(spec))
	start := time.Now()
	ops := d.Operations()
	b, _ := json.Marshal(Tools(ops, Selection{Enabled: map[string]bool{"POST /a": true}}))
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
	if len(b) > 2<<20 {
		t.Errorf("the tool is %d bytes", len(b))
	}
}

func TestSchemaInlineNestingIsBounded(t *testing.T) {
	var v any = map[string]any{"type": "string"}
	for i := 0; i < 200; i++ {
		v = map[string]any{"type": "object", "properties": map[string]any{"n": v}}
	}
	d := &Doc{Raw: map[string]any{}}
	b, _ := json.Marshal(d.Schema(v))
	if strings.Count(string(b), `"properties"`) > maxInlineDepth+1 {
		t.Errorf("nesting not cut: %d levels", strings.Count(string(b), `"properties"`))
	}
}

func TestSecuritySchemesAreNotSchemaProblems(t *testing.T) {
	d := mustParse(t, `{"openapi":"3.0.0","info":{"title":"s"},"paths":{"/a":{"get":{"operationId":"a"}}},
	 "components":{"securitySchemes":{"k":{"type":"apiKey","in":"header","name":"X"},"b":{"type":"http","scheme":"bearer"},"o":{"type":"oauth2","flows":{}}},
	 "schemas":{"S":{"type":"object","x-ext":{"type":"whatever"}}}}}`)
	for _, is := range d.Validate() {
		t.Errorf("unexpected issue %s %s", is.Code, is.Path)
	}
	bad := mustParse(t, `{"openapi":"3.0.0","info":{"title":"s"},"paths":{"/a":{"get":{"operationId":"a","responses":{"200":{"description":"x","content":{"application/json":{"schema":{"type":"strng"}}}}}}}}}`)
	if len(bad.Validate()) == 0 {
		t.Error("a real invalid type must still be reported")
	}
}

func TestNormalizedSpecIsCompactAndStoredParseHasNoSizeLimit(t *testing.T) {
	d := mustParse(t, petJSON)
	if strings.Contains(string(d.JSON()), "\n") {
		t.Error("the stored description is indented")
	}
	big := `{"openapi":"3.0.0","info":{"title":"` + strings.Repeat("x", MaxSpecBytes) + `"},"paths":{"/a":{"get":{}}}}`
	if _, err := Parse([]byte(big)); err == nil {
		t.Error("Parse accepted a description over the limit")
	}
	if _, err := ParseStored([]byte(big)); err != nil {
		t.Errorf("ParseStored: %v", err)
	}
}

func TestRepairDropsAnyPatchThatTouchesAuthOrServers(t *testing.T) {
	bad := []Patch{
		{Op: "set", Path: "/servers", Value: []any{}},
		{Op: "set", Path: "/paths/~1a/servers", Value: []any{}},
		{Op: "set", Path: "/paths/~1a/get/security", Value: []any{}},
		{Op: "set", Path: "/components", Value: map[string]any{"securitySchemes": map[string]any{}}},
		{Op: "remove", Path: "/components/securitySchemes/k"},
		{Op: "set", Path: "/info", Value: map[string]any{"x": []any{map[string]any{"security": 1}}}},
	}
	for _, p := range bad {
		if !touchesAuthOrServers(p) {
			t.Errorf("kept %+v", p)
		}
	}
	for _, p := range []Patch{
		{Op: "set", Path: "/paths/~1a/get/operationId", Value: "a"},
		{Op: "set", Path: "/components/schemas/S/properties/security/type", Value: "string"},
	} {
		if touchesAuthOrServers(p) {
			t.Errorf("dropped %+v", p)
		}
	}
}

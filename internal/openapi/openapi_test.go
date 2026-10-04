package openapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const petJSON = `{
 "openapi":"3.0.3","info":{"title":"Pets","version":"1"},
 "servers":[{"url":"https://api.example.com/v1"}],
 "paths":{
  "/pets":{
   "get":{"operationId":"listPets","summary":"List pets","parameters":[
     {"name":"limit","in":"query","schema":{"type":"integer"}},
     {"name":"tag","in":"query","explode":true,"schema":{"type":"array","items":{"type":"string"}}}]},
   "post":{"operationId":"createPet","requestBody":{"required":true,"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Pet"}}}}}
  },
  "/pets/{petId}":{
   "parameters":[{"name":"petId","in":"path","required":true,"schema":{"type":"string"}}],
   "get":{"operationId":"getPet","description":"Fetch one.\n\nMore text."},
   "delete":{}
  },
  "/pets/{petId}/photo":{"post":{"operationId":"upload","requestBody":{"content":{"multipart/form-data":{"schema":{"type":"object"}}}}}}
 },
 "components":{"schemas":{"Pet":{"type":"object","required":["name"],"properties":{"name":{"type":"string","example":"x"},"friend":{"$ref":"#/components/schemas/Pet"}}}}}
}`

func mustParse(t *testing.T, s string) *Doc {
	t.Helper()
	d, err := Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestParseFormats(t *testing.T) {
	y := "openapi: 3.0.0\ninfo:\n  title: Y\n  version: '1'\npaths:\n  /a:\n    get:\n      operationId: a\n"
	toml := "openapi = \"3.1.0\"\n[info]\ntitle = \"T\"\nversion = \"1\"\n[paths.\"/a\".get]\noperationId = \"a\"\n"
	for name, src := range map[string]string{"json": petJSON, "yaml": y, "toml": toml} {
		d, err := Parse([]byte(src))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(d.Operations()) == 0 {
			t.Errorf("%s: no operations", name)
		}
	}
	if _, err := Parse([]byte("hello: [")); err == nil {
		t.Error("garbage parsed")
	}
	if _, err := Parse([]byte(`{"openapi":"2.9.0","paths":{}}`)); err == nil {
		t.Error("unsupported version parsed")
	}
}

func TestSwaggerConverted(t *testing.T) {
	d := mustParse(t, `{"swagger":"2.0","info":{"title":"S","version":"1"},"host":"h.example.com","basePath":"/api","schemes":["https"],
	 "paths":{"/x/{id}":{"put":{"operationId":"putX","consumes":["application/json"],"parameters":[
	  {"name":"id","in":"path","required":true,"type":"string"},
	  {"name":"b","in":"body","required":true,"schema":{"$ref":"#/definitions/B"}}]}}},
	 "definitions":{"B":{"type":"object","properties":{"n":{"type":"integer"}}}}}`)
	if s := d.Servers(""); len(s) != 1 || s[0].URL != "https://h.example.com/api" {
		t.Fatalf("servers %+v", s)
	}
	ops := d.Operations()
	if len(ops) != 1 || ops[0].Body == nil || ops[0].Body.Schema["properties"] == nil || len(ops[0].Params) != 1 {
		t.Fatalf("%+v", ops)
	}
}

func TestValidateReportsAndNeverBlocks(t *testing.T) {
	d := mustParse(t, `{"openapi":"3.0.0","info":{"title":"x","version":"1"},"paths":{"/a":{"get":{"responses":{}},"post":{"operationId":"d"}},"/b":{"get":{"operationId":"d","parameters":[{"in":"query"}],"requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Nope"}}}}}}}}`)
	codes := map[string]bool{}
	for _, is := range d.Validate() {
		codes[is.Code] = true
	}
	for _, want := range []string{"missing-operation-id", "duplicate-operation-id", "invalid-parameter", "broken-ref"} {
		if !codes[want] {
			t.Errorf("missing issue %s: %v", want, codes)
		}
	}
	if len(d.Operations()) != 3 {
		t.Error("operations lost")
	}
}

func TestPatchApply(t *testing.T) {
	d := mustParse(t, petJSON)
	nd, ch, err := d.Apply([]Patch{{Op: "set", Path: "/paths/~1pets~1{petId}/delete/operationId", Value: "deletePet", Reason: "name"}})
	if err != nil || len(ch) != 1 {
		t.Fatal(err, ch)
	}
	if got := str(obj(obj(obj(nd.Raw["paths"])["/pets/{petId}"])["delete"])["operationId"]); got != "deletePet" {
		t.Errorf("got %q", got)
	}
	if str(obj(obj(obj(d.Raw["paths"])["/pets/{petId}"])["delete"])["operationId"]) != "" {
		t.Error("the original changed")
	}
	if _, _, err := d.Apply([]Patch{{Op: "set", Path: "/nope/deeper/x", Value: 1}}); err == nil {
		t.Error("a patch under a missing parent applied")
	}
	if _, _, err := d.Apply(make([]Patch, MaxPatches+1)); err == nil {
		t.Error("too many patches applied")
	}
}

func TestOperationsAndTools(t *testing.T) {
	d := mustParse(t, petJSON)
	ops := d.Operations()
	byKey := map[string]Op{}
	for _, o := range ops {
		byKey[o.Key] = o
	}
	if byKey["POST /pets/{petId}/photo"].Skip == "" {
		t.Error("upload not skipped")
	}
	if byKey["DELETE /pets/{petId}"].ID != "delete_pets_petId" {
		t.Errorf("generated id %q", byKey["DELETE /pets/{petId}"].ID)
	}
	if len(byKey["GET /pets/{petId}"].Params) != 1 {
		t.Error("path-level parameter not merged")
	}
	sel := Selection{Enabled: map[string]bool{"GET /pets": true, "POST /pets": true, "GET /pets/{petId}": true, "POST /pets/{petId}/photo": true},
		Overrides: map[string]Override{"GET /pets/{petId}": {Name: "list pets", Description: "Mine."}}}
	tools := Tools(ops, sel)
	if len(tools) != 3 {
		t.Fatalf("%d tools", len(tools))
	}
	var names []string
	for _, tl := range tools {
		names = append(names, tl.Name)
	}
	if strings.Join(names, ",") != "listPets,createPet,list_pets" && strings.Join(names, ",") != "createPet,listPets,list_pets" {
		// order follows path order: /pets get, /pets post, /pets/{petId} get
		t.Errorf("names %v", names)
	}
	for _, tl := range tools {
		if tl.Name == "createPet" {
			b, _ := json.Marshal(tl.InputSchema)
			s := string(b)
			if !strings.Contains(s, `"body"`) || strings.Contains(s, "example") || !strings.Contains(s, `"required":["body"]`) {
				t.Errorf("schema %s", s)
			}
		}
		if tl.Name == "list_pets" && tl.Description != "Mine." {
			t.Errorf("override lost: %q", tl.Description)
		}
	}
	// a clash gets a suffix
	sel.Overrides["GET /pets/{petId}"] = Override{Name: "listPets"}
	tools = Tools(ops, sel)
	if tools[2].Name != "listPets_2" {
		t.Errorf("clash: %q", tools[2].Name)
	}
}

func TestSchemaCycleAndDepth(t *testing.T) {
	d := mustParse(t, petJSON)
	b, _ := json.Marshal(d.Schema(map[string]any{"$ref": "#/components/schemas/Pet"}))
	if len(b) > 2000 || strings.Contains(string(b), "$ref") {
		t.Errorf("schema %s", b)
	}
}

func caller(srv *httptest.Server, a Auth) *Caller {
	return &Caller{Base: srv.URL + "/v1", Auth: a, Client: srv.Client()}
}

func toolFor(t *testing.T, d *Doc, key string) Tool {
	t.Helper()
	for _, tl := range Tools(d.Operations(), Selection{Enabled: map[string]bool{key: true}}) {
		return tl
	}
	t.Fatalf("no tool for %s", key)
	return Tool{}
}

func TestCallBuildsRequests(t *testing.T) {
	var got *http.Request
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	d := mustParse(t, petJSON)

	res := caller(srv, Auth{Kind: AuthBearer, Value: "tok"}).Call(context.Background(), toolFor(t, d, "GET /pets/{petId}"), map[string]any{"petId": "a/b c"})
	if res.IsError || !strings.Contains(res.Text, `{"ok":true}`) {
		t.Fatalf("%+v", res)
	}
	if got.URL.EscapedPath() != "/v1/pets/a%2Fb%20c" || got.Header.Get("Authorization") != "Bearer tok" {
		t.Errorf("path %q auth %q", got.URL.EscapedPath(), got.Header.Get("Authorization"))
	}

	c := caller(srv, Auth{Kind: AuthQuery, Name: "key", Value: "k"})
	c.Call(context.Background(), toolFor(t, d, "GET /pets"), map[string]any{"limit": 5, "tag": []any{"a", "b"}, "key": "evil"})
	q := got.URL.Query()
	if q.Get("limit") != "5" || strings.Join(q["tag"], ",") != "a,b" || q.Get("key") != "k" {
		t.Errorf("query %v", q)
	}

	c = caller(srv, Auth{Kind: AuthBasic, Name: "u", Value: "p"})
	c.Call(context.Background(), toolFor(t, d, "POST /pets"), map[string]any{"body": map[string]any{"name": "rex"}})
	if u, p, ok := got.BasicAuth(); !ok || u != "u" || p != "p" || body != `{"name":"rex"}` || got.Header.Get("Content-Type") != "application/json" {
		t.Errorf("basic %v %v body %q", u, p, body)
	}

	c = caller(srv, Auth{Kind: AuthHeader, Name: "X-Key", Value: "s"})
	c.Call(context.Background(), toolFor(t, d, "GET /pets"), nil)
	if got.Header.Get("X-Key") != "s" {
		t.Error("header auth missing")
	}
}

func TestCallRejectsBadInput(t *testing.T) {
	d := mustParse(t, petJSON)
	c := &Caller{Base: "https://api.example.com"}
	tl := toolFor(t, d, "GET /pets/{petId}")
	for _, args := range []map[string]any{nil, {"petId": ".."}, {"petId": ""}, {"petId": map[string]any{"a": 1}}} {
		if r := c.Call(context.Background(), tl, args); !r.IsError {
			t.Errorf("accepted %v: %s", args, r.Text)
		}
	}
}

func TestCallErrorsAndTruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/pets":
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte(strings.Repeat("é", MaxResultBytes)))
		default:
			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(404)
			w.Write([]byte("PNG"))
		}
	}))
	defer srv.Close()
	d := mustParse(t, petJSON)
	r := caller(srv, Auth{}).Call(context.Background(), toolFor(t, d, "GET /pets"), nil)
	if r.IsError || !strings.Contains(r.Text, "[truncated") || len(r.Text) > MaxResultBytes+600 {
		t.Errorf("len %d: %.80s", len(r.Text), r.Text)
	}
	r = caller(srv, Auth{}).Call(context.Background(), toolFor(t, d, "GET /pets/{petId}"), map[string]any{"petId": "1"})
	if !r.IsError || !strings.Contains(r.Text, "binary response") {
		t.Errorf("%+v", r)
	}
	srv.Close()
	r = (&Caller{Base: srv.URL, Auth: Auth{Kind: AuthQuery, Name: "key", Value: "SECRET"}}).Call(context.Background(), toolFor(t, d, "GET /pets"), nil)
	if !r.IsError || strings.Contains(r.Text, "SECRET") {
		t.Errorf("error leaks: %s", r.Text)
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/spec" {
			w.Write([]byte(petJSON))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	b, err := Fetch(context.Background(), srv.URL+"/spec")
	if err != nil || mustParse(t, string(b)).Title() != "Pets" {
		t.Fatal(err)
	}
	if _, err := Fetch(context.Background(), srv.URL+"/none"); err == nil {
		t.Error("404 accepted")
	}
	if _, err := Fetch(context.Background(), "file:///etc/passwd"); err == nil {
		t.Error("file URL accepted")
	}
}

func TestServersResolve(t *testing.T) {
	d := mustParse(t, `{"openapi":"3.0.0","info":{"title":"x","version":"1"},"servers":[{"url":"/api"},{"url":"https://{env}.x.com","variables":{"env":{"default":"prod"}}}],"paths":{}}`)
	s := d.Servers("https://spec.example.com/docs/openapi.json")
	if len(s) != 2 || s[0].URL != "https://spec.example.com/api" || s[1].URL != "https://prod.x.com" {
		t.Errorf("%+v", s)
	}
}

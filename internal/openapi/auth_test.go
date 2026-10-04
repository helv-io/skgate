package openapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func schemeOfSpec(t *testing.T, spec string) Scheme {
	t.Helper()
	d, err := Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	return d.Scheme()
}

func TestSchemeFromTheDescription(t *testing.T) {
	for name, tc := range map[string]struct {
		spec string
		want Scheme
	}{
		"bearer":                                 {`{"openapi":"3.0.0","components":{"securitySchemes":{"b":{"type":"http","scheme":"bearer"}}},"security":[{"b":[]}],"paths":{}}`, Scheme{AuthBearer, ""}},
		"basic":                                  {`{"openapi":"3.0.0","components":{"securitySchemes":{"b":{"type":"http","scheme":"Basic"}}},"security":[{"b":[]}],"paths":{}}`, Scheme{AuthBasic, ""}},
		"header":                                 {`{"openapi":"3.0.0","components":{"securitySchemes":{"k":{"type":"apiKey","in":"header","name":"X-Token"}}},"security":[{"k":[]}],"paths":{}}`, Scheme{AuthHeader, "X-Token"}},
		"query":                                  {`{"openapi":"3.0.0","components":{"securitySchemes":{"k":{"type":"apiKey","in":"query","name":"key"}}},"security":[{"k":[]}],"paths":{}}`, Scheme{AuthQuery, "key"}},
		"oauth2":                                 {`{"openapi":"3.0.0","components":{"securitySchemes":{"o":{"type":"oauth2","flows":{}}}},"security":[{"o":["a"]}],"paths":{}}`, Scheme{AuthBearer, ""}},
		"cookie is not usable":                   {`{"openapi":"3.0.0","components":{"securitySchemes":{"c":{"type":"apiKey","in":"cookie","name":"s"}}},"security":[{"c":[]}],"paths":{}}`, Scheme{}},
		"alternatives: bearer first, query last": {`{"openapi":"3.0.0","components":{"securitySchemes":{"q":{"type":"apiKey","in":"query","name":"k"},"h":{"type":"apiKey","in":"header","name":"X-K"},"b":{"type":"http","scheme":"bearer"}}},"security":[{"q":[]},{"h":[]},{"b":[]}],"paths":{}}`, Scheme{AuthBearer, ""}},
		"operation level":                        {`{"openapi":"3.0.0","components":{"securitySchemes":{"h":{"type":"apiKey","in":"header","name":"X-K"}}},"paths":{"/a":{"get":{"security":[{"h":[]}]}}}}`, Scheme{AuthHeader, "X-K"}},
		"defined, never required":                {`{"openapi":"3.0.0","components":{"securitySchemes":{"h":{"type":"apiKey","in":"header","name":"X-K"}}},"paths":{}}`, Scheme{AuthHeader, "X-K"}},
		"silent":                                 {`{"openapi":"3.0.0","paths":{}}`, Scheme{}},
		"swagger2 basic":                         {`{"swagger":"2.0","securityDefinitions":{"b":{"type":"basic"}},"security":[{"b":[]}],"paths":{}}`, Scheme{AuthBasic, ""}},
		"swagger2 apiKey":                        {`{"swagger":"2.0","securityDefinitions":{"k":{"type":"apiKey","in":"header","name":"api_key"}},"paths":{"/x":{"get":{"security":[{"k":[]}]}}}}`, Scheme{AuthHeader, "api_key"}},
		"swagger2 oauth2":                        {`{"swagger":"2.0","securityDefinitions":{"o":{"type":"oauth2","flow":"implicit"}},"security":[{"o":[]}],"paths":{}}`, Scheme{AuthBearer, ""}},
	} {
		if got := schemeOfSpec(t, tc.spec); got != tc.want {
			t.Errorf("%s: %+v, want %+v", name, got, tc.want)
		}
	}
}

func TestWaysAndAuth(t *testing.T) {
	if got := Ways(Scheme{}, "abc"); len(got) != 4 || got[0] != "bearer" || got[1] != "header:X-API-Key" || got[2] != "header:Api-Key" || got[3] != "token" {
		t.Errorf("silent spec: %v", got)
	}
	if got := Ways(Scheme{}, "user:pass"); got[len(got)-1] != "basic" {
		t.Errorf("user:pass should add basic: %v", got)
	}
	if got := Ways(Scheme{AuthQuery, "key"}, "v"); len(got) != 1 || got[0] != "query:key" {
		t.Errorf("a stated scheme is the only way: %v", got)
	}
	for w, want := range map[Way]Auth{
		"bearer": {AuthBearer, "", "v"}, "token": {AuthHeader, "Authorization", "Token v"}, "header:X-K": {AuthHeader, "X-K", "v"},
		"query:k": {AuthQuery, "k", "v"}, "basic": {AuthBasic, "v", ""},
	} {
		if got := w.Auth("v"); got != want {
			t.Errorf("%s: %+v want %+v", w, got, want)
		}
	}
	if got := Way("basic").Auth("al:pw:x"); got != (Auth{AuthBasic, "al", "pw:x"}) {
		t.Errorf("basic: %+v", got)
	}
}

func TestProbeOpPrefersAProtectedSafeGet(t *testing.T) {
	d, _ := Parse([]byte(`{"openapi":"3.0.0","security":[{"b":[]}],"components":{"securitySchemes":{"b":{"type":"http","scheme":"bearer"}}},"paths":{
	 "/health":{"get":{"operationId":"health","security":[]}},
	 "/items/{id}":{"get":{"operationId":"item","parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}]}},
	 "/q":{"get":{"operationId":"q","parameters":[{"name":"x","in":"query","required":true,"schema":{"type":"string"}}]}},
	 "/make":{"post":{"operationId":"make"}},
	 "/things":{"get":{"operationId":"things"}}}}`))
	o, ok := d.ProbeOp(d.Operations())
	if !ok || o.Path != "/things" {
		t.Errorf("probe op: %+v %v", o, ok)
	}
	d2, _ := Parse([]byte(`{"openapi":"3.0.0","paths":{"/a":{"post":{}},"/b/{x}":{"get":{"parameters":[{"name":"x","in":"path","required":true,"schema":{"type":"string"}}]}}}}`))
	if _, ok := d2.ProbeOp(d2.Operations()); ok {
		t.Error("no safe GET without arguments: nothing to probe")
	}
	d3, _ := Parse([]byte(`{"openapi":"3.0.0","paths":{"/open":{"get":{}}}}`))
	if o, ok := d3.ProbeOp(d3.Operations()); !ok || o.Path != "/open" {
		t.Errorf("first safe GET: %+v", o)
	}
}

func TestTryKeyFindsTheWayThatIsNotRefused(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		switch {
		case r.Header.Get("Api-Key") == "sekret":
			w.Write([]byte("[]"))
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()
	op := Op{Method: "GET", Path: "/things"}
	got := TryKey(context.Background(), srv.URL, op, "sekret", Ways(Scheme{}, "sekret"), "t")
	if !got.OK || got.Way != "header:Api-Key" || got.Status != 200 {
		t.Fatalf("%+v", got)
	}
	for _, s := range seen {
		if s != "GET /things" {
			t.Errorf("only a safe GET of the operation is sent, saw %s", s)
		}
	}
	// refused every way
	bad := TryKey(context.Background(), srv.URL, op, "wrong", Ways(Scheme{}, "wrong"), "t")
	if bad.OK || bad.Status != 401 {
		t.Errorf("refused: %+v", bad)
	}
	// a stated scheme is not second-guessed
	seen = nil
	TryKey(context.Background(), srv.URL, op, "sekret", Ways(Scheme{AuthBearer, ""}, "sekret"), "t")
	if len(seen) != 1 {
		t.Errorf("one request for a stated scheme, saw %v", seen)
	}
	srv.Close()
	if gone := TryKey(context.Background(), srv.URL, op, "x", []Way{"bearer"}, "t"); gone.Status != 0 || gone.OK {
		t.Errorf("unreachable: %+v", gone)
	}
}

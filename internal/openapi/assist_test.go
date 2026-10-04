package openapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type fakeLLM struct {
	replies []string // content per call; "400" answers HTTP 400
	bodies  []string
}

func (f *fakeLLM) Post(_ context.Context, _ string, body []byte) (int, []byte, error) {
	f.bodies = append(f.bodies, string(body))
	r := f.replies[0]
	if len(f.replies) > 1 {
		f.replies = f.replies[1:]
	}
	if r == "400" {
		return 400, []byte(`{"error":"unsupported"}`), nil
	}
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": r}}}})
	return 200, b, nil
}

func TestRepairProposesPatchesAndDropsServerChanges(t *testing.T) {
	d := mustParse(t, `{"openapi":"3.0.0","info":{"title":"x","version":"1"},"paths":{"/a":{"get":{}}}}`)
	llm := &fakeLLM{replies: []string{"```json\n" + `{"patches":[{"op":"set","path":"/paths/~1a/get/operationId","value":"getA","reason":"missing"},{"op":"set","path":"/servers","value":[{"url":"https://evil.example"}]}]}` + "\n```"}}
	as := Assist{LLM: llm, Model: "m"}
	patches, err := as.Repair(context.Background(), d, d.Validate())
	if err != nil || len(patches) != 1 || patches[0].Path != "/paths/~1a/get/operationId" {
		t.Fatalf("%v %+v", err, patches)
	}
	nd, _, err := d.Apply(patches)
	if err != nil {
		t.Fatal(err)
	}
	for _, is := range nd.Validate() {
		if is.Code == "missing-operation-id" {
			t.Error("not repaired")
		}
	}
	if !strings.Contains(llm.bodies[0], "missing-operation-id") || strings.Contains(llm.bodies[0], "MCP server from") {
		t.Errorf("prompt: %s", llm.bodies[0])
	}
}

func TestDescribeValidatesAnswers(t *testing.T) {
	d := mustParse(t, petJSON)
	ops := d.Operations()[:3]
	llm := &fakeLLM{replies: []string{`{"tools":[
	 {"key":"GET /pets","name":"list pets!","description":"Lists  the pets\nnewest first."},
	 {"key":"POST /pets","name":"list_pets","description":"Adds one."},
	 {"key":"GET /nope","name":"x","description":"y"}]}`}}
	res, err := (Assist{LLM: llm, Model: "m", Effort: "low"}).Describe(context.Background(), ops)
	if err != nil {
		t.Fatal(err)
	}
	if res["GET /pets"].Name != "list_pets" || res["GET /pets"].Description != "Lists the pets newest first." {
		t.Errorf("%+v", res["GET /pets"])
	}
	if res["POST /pets"].Name != "" || res["POST /pets"].Description != "Adds one." { // a repeated name is dropped
		t.Errorf("%+v", res["POST /pets"])
	}
	if _, ok := res["GET /nope"]; ok {
		t.Error("an answer for a key that was not asked was kept")
	}
}

func TestAskRetriesWithoutEffortAndJSONMode(t *testing.T) {
	d := mustParse(t, petJSON)
	llm := &fakeLLM{replies: []string{"400", "400", `{"tools":[{"key":"GET /pets","name":"list_pets","description":"d"}]}`}}
	if _, err := (Assist{LLM: llm, Model: "m", Effort: "high"}).Describe(context.Background(), d.Operations()[:1]); err != nil {
		t.Fatal(err)
	}
	if len(llm.bodies) != 3 || strings.Contains(llm.bodies[1], "reasoning_effort") || !strings.Contains(llm.bodies[1], "response_format") ||
		strings.Contains(llm.bodies[2], "response_format") {
		t.Errorf("degrade steps wrong: %d calls", len(llm.bodies))
	}
	if _, err := (Assist{LLM: &fakeLLM{replies: []string{"not json"}}, Model: "m"}).Describe(context.Background(), d.Operations()[:1]); err == nil {
		t.Error("garbage accepted")
	}
}

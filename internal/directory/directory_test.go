package directory

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/config"
)

// rpc sends requests through Run, over pipes like a real client, and returns the answers by id.
func rpc(t *testing.T, requests ...string) []map[string]any {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- Run(inR, outW); outW.Close() }()
	go func() {
		for _, r := range requests {
			_, _ = io.WriteString(inW, r+"\n")
		}
		inW.Close()
	}()
	var got []map[string]any
	sc := bufio.NewScanner(outR)
	sc.Buffer(nil, 1<<22)
	deadline := time.AfterFunc(10*time.Second, func() { outR.Close() })
	defer deadline.Stop()
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("answer is not JSON: %q", sc.Text())
		}
		got = append(got, m)
	}
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	return got
}

func TestRoundTripInitializeListCall(t *testing.T) {
	got := rpc(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"about","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"ping"}`,
	)
	if len(got) != 4 { // the notification gets no answer
		t.Fatalf("got %d answers, want 4: %v", len(got), got)
	}
	init := got[0]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v", init["protocolVersion"])
	}
	if si := init["serverInfo"].(map[string]any); si["name"] != "skgate" || si["version"] != config.Version {
		t.Errorf("serverInfo = %v", si)
	}
	tools := got[1]["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, x := range tools {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "about,list_features,config_help" {
		t.Errorf("tools = %v", names)
	}
	res := got[2]["result"].(map[string]any)
	sc := res["structuredContent"].(map[string]any)
	if sc["name"] != "skgate" || sc["version"] != config.Version {
		t.Errorf("about = %v", sc)
	}
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, config.Version) {
		t.Errorf("text content lacks the version: %s", text)
	}
	if _, ok := got[3]["result"].(map[string]any); !ok {
		t.Errorf("ping = %v", got[3])
	}
}

func TestUnsupportedProtocolGetsTheNewest(t *testing.T) {
	got := rpc(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	if v := got[0]["result"].(map[string]any)["protocolVersion"]; v != protocols[0] {
		t.Errorf("protocolVersion = %v", v)
	}
}

func TestErrors(t *testing.T) {
	got := rpc(t,
		`not json`,
		`{"jsonrpc":"2.0","id":1,"method":"resources/subscribe"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nope"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{}}`,
	)
	want := []float64{errParse, errMethod, errParams, errParams}
	if len(got) != len(want) {
		t.Fatalf("answers = %v", got)
	}
	for i, w := range want {
		if c := got[i]["error"].(map[string]any)["code"]; c != w {
			t.Errorf("answer %d code = %v, want %v", i, c, w)
		}
	}
}

func TestToolErrorsAreResultsTheModelCanRead(t *testing.T) {
	got := rpc(t,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_features","arguments":{"area":"cooking"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"config_help","arguments":{"variable":"NOPE"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"config_help","arguments":"x"}}`,
	)
	for i, g := range got {
		res := g["result"].(map[string]any)
		if res["isError"] != true {
			t.Errorf("call %d: want isError, got %v", i, res)
		}
	}
	text := got[0]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	for _, a := range areas() {
		if !strings.Contains(text, a) {
			t.Errorf("the error does not name the area %q: %s", a, text)
		}
	}
}

func TestListFeaturesAndConfigHelp(t *testing.T) {
	got := rpc(t,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_features","arguments":{"area":"openapi"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_features"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"config_help","arguments":{"variable":"public_url"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"config_help"}}`,
	)
	sc := func(i int) map[string]any {
		return got[i]["result"].(map[string]any)["structuredContent"].(map[string]any)
	}
	if f := sc(0)["features"].([]any); len(f) != 1 || f[0].(map[string]any)["area"] != "openapi" {
		t.Errorf("openapi features = %v", f)
	}
	if sc(1)["area"] != "all" || len(sc(1)["features"].([]any)) != len(features) {
		t.Errorf("all features = %v", sc(1))
	}
	if s := sc(2)["settings"].([]any); len(s) != 1 || s[0].(map[string]any)["name"] != "PUBLIC_URL" {
		t.Errorf("PUBLIC_URL = %v", s)
	}
	if len(sc(3)["settings"].([]any)) != len(settings) {
		t.Errorf("settings = %v", sc(3))
	}
}

// Every tool says what it is for, takes an object, declares what it returns and promises read-only.
func TestToolDefinitions(t *testing.T) {
	emDash := "\u2014"
	for _, tool := range toolList() {
		name := tool["name"].(string)
		desc := tool["description"].(string)
		if len(desc) < 120 {
			t.Errorf("%s: description is too short to say what it takes and returns", name)
		}
		if strings.Contains(desc, emDash) || strings.Contains(tool["title"].(string), emDash) {
			t.Errorf("%s: no em dashes", name)
		}
		for _, k := range []string{"inputSchema", "outputSchema"} {
			if tool[k].(map[string]any)["type"] != "object" {
				t.Errorf("%s: %s is not an object schema", name, k)
			}
		}
		for p, v := range tool["inputSchema"].(map[string]any)["properties"].(map[string]any) {
			if v.(map[string]any)["description"] == "" {
				t.Errorf("%s.%s: no description", name, p)
			}
		}
		a := tool["annotations"].(map[string]any)
		if a["readOnlyHint"] != true || a["destructiveHint"] != false || a["openWorldHint"] != false {
			t.Errorf("%s: annotations = %v", name, a)
		}
		// The structured answer carries every property the output schema requires.
		res, ok := call(name, nil)
		if !ok {
			t.Fatalf("%s is listed but cannot be called", name)
		}
		sc := res["structuredContent"].(map[string]any)
		b, _ := json.Marshal(sc)
		var back map[string]any
		_ = json.Unmarshal(b, &back)
		for _, r := range tool["outputSchema"].(map[string]any)["required"].([]string) {
			if _, ok := back[r]; !ok {
				t.Errorf("%s: output lacks required %q", name, r)
			}
		}
	}
}

// config_help lists exactly the variables of the README table, so the two cannot drift apart.
func TestSettingsMatchTheREADME(t *testing.T) {
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(b)
	i := strings.Index(readme, "\n## Configuration")
	if i < 0 {
		t.Fatal("README has no Configuration section")
	}
	section := readme[i+1:]
	if j := strings.Index(section[3:], "\n## "); j >= 0 {
		section = section[:j+3]
	}
	want := map[string]bool{}
	re := regexp.MustCompile("`([A-Z][A-Z0-9_]+)`")
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cell := strings.SplitN(line, "|", 4)[1]
		for _, m := range re.FindAllStringSubmatch(cell, -1) {
			want[m[1]] = true
		}
	}
	if len(want) < 10 {
		t.Fatalf("found only %d variables in the README table", len(want))
	}
	have := map[string]bool{}
	for _, s := range settings {
		have[s.Name] = true
		if !want[s.Name] {
			t.Errorf("config_help lists %s, which the README table does not", s.Name)
		}
	}
	for n := range want {
		if !have[n] {
			t.Errorf("the README table lists %s, which config_help does not", n)
		}
	}
}

func TestTextHasNoEmDashes(t *testing.T) {
	for _, f := range features {
		if strings.Contains(f.Summary, "\u2014") {
			t.Errorf("%s: em dash", f.ID)
		}
	}
	for _, s := range settings {
		if strings.Contains(s.Description, "\u2014") {
			t.Errorf("%s: em dash", s.Name)
		}
	}
	if strings.Contains(summary, "\u2014") {
		t.Error("summary has an em dash")
	}
}

func TestOversizedLineIsAParseError(t *testing.T) {
	got := rpc(t, `{"jsonrpc":"2.0","id":1,"method":"`+strings.Repeat("a", maxLine+10)+`"}`, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if len(got) != 2 || got[0]["error"].(map[string]any)["code"] != float64(errParse) || got[1]["result"] == nil {
		t.Errorf("answers = %v", got)
	}
}

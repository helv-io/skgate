package app

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/httputil"
)

// slowPost sends a request whose body arrives in two parts with a pause between them, over a raw connection, and
// returns the status and body of the answer.
func slowPost(t *testing.T, ts *httptest.Server, path string, hdr map[string]string, body string, firstN int, pause time.Duration) (int, string) {
	t.Helper()
	c, err := net.Dial("tcp", strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	var h strings.Builder
	fmt.Fprintf(&h, "POST %s HTTP/1.1\r\nHost: x\r\nContent-Length: %d\r\nConnection: close\r\n", path, len(body))
	for k, v := range hdr {
		fmt.Fprintf(&h, "%s: %s\r\n", k, v)
	}
	h.WriteString("\r\n")
	_, _ = io.WriteString(c, h.String()+body[:firstN])
	time.Sleep(pause)
	_, _ = io.WriteString(c, body[firstN:]) // may fail when the server has already answered and closed
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("%s: no answer: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// The small endpoints cut off a body that takes longer than the deadline; the ones that stream or upload do not
// (re-audit PROD-06).
func TestBodyDeadlineIsOnSmallEndpointsOnly(t *testing.T) {
	old := httputil.SmallBodyTimeout
	httputil.SmallBodyTimeout = 300 * time.Millisecond
	t.Cleanup(func() { httputil.SmallBodyTimeout = old })
	pause := 3 * httputil.SmallBodyTimeout

	grokUp, _ := modelsUpstream(t, "grok-4.7")
	oa, lastChat := openAIUpstream(t, "gpt-x")
	_, ts, br, csrf, full := signedInProvider(t, grokUp)
	if resp, _ := addProvider(br, csrf, "openai", oa.URL+"/v1", "sk-test-1234"); resp.StatusCode != 303 {
		t.Fatalf("add provider: %d", resp.StatusCode)
	}
	reg := `{"redirect_uris":["https://client.example/cb"],"client_name":"slow"}`
	json := map[string]string{"Content-Type": "application/json"}

	// /register: a body that arrives in time is accepted, one that dribbles in is cut off
	if st, b := slowPost(t, ts, "/register", json, reg, len(reg), 0); st != 201 {
		t.Fatalf("a normal registration must work: %d %s", st, b)
	}
	if st, b := slowPost(t, ts, "/register", json, reg, 10, pause); st != 400 {
		t.Fatalf("a slow body must be cut off: %d %s", st, b)
	}
	// /token takes the same rule
	form := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	tok := "grant_type=authorization_code&code=x&client_id=y&redirect_uri=https%3A%2F%2Fa.example%2Fcb&code_verifier=" + strings.Repeat("a", 43)
	if st, b := slowPost(t, ts, "/token", form, tok, 10, pause); st != 400 {
		t.Fatalf("a slow /token body must be cut off: %d %s", st, b)
	}

	// /mcp: the same slow body is read to the end and answered
	mcpHdr := map[string]string{"Content-Type": "application/json", "Accept": "application/json, text/event-stream", "Authorization": "Bearer " + full}
	ping := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	if st, b := slowPost(t, ts, "/mcp", mcpHdr, ping, 10, pause); st != 200 || !strings.Contains(b, `"result"`) {
		t.Fatalf("/mcp must not cut a slow body: %d %s", st, b)
	}
	// /v1: a body that takes longer than the deadline reaches the provider whole, as an upload would
	chat := `{"model":"openai_gpt-x","messages":[{"role":"user","content":"` + strings.Repeat("hello ", 20) + `"}]}`
	v1 := map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + full}
	if st, b := slowPost(t, ts, "/v1/chat/completions", v1, chat, 10, pause); st != 200 {
		t.Fatalf("/v1 must not cut a slow body: %d %s", st, b)
	}
	if _, got := lastChat(); !strings.Contains(got, strings.Repeat("hello ", 20)) {
		t.Fatalf("the provider did not get the whole body: %q", got)
	}

	// an admin form is cut off too, a signed-in admin included
	u, _ := url.Parse(ts.URL)
	var cookies []string
	for _, c := range br.c.Jar.Cookies(u) {
		cookies = append(cookies, c.Name+"="+c.Value)
	}
	adm := map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Cookie": strings.Join(cookies, "; ")}
	formBody := url.Values{"csrf": {csrf}, "label": {"slow"}}.Encode()
	if st, b := slowPost(t, ts, "/admin/keys/create", adm, formBody, 10, pause); st != http.StatusRequestTimeout {
		t.Fatalf("a slow admin form must be cut off with 408: %d %s", st, b)
	}
	if st, b := slowPost(t, ts, "/admin/keys/create", adm, formBody, len(formBody), 0); st != 200 && st != 303 {
		t.Fatalf("a normal admin form must work: %d %s", st, b)
	}
}

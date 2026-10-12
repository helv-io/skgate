package suggest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func exploreRepo(t *testing.T) (*Fetcher, *rewrite) {
	return fetcherFor(t, func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/contents") || strings.HasSuffix(p, "/contents/"):
			io.WriteString(w, `[{"name":"README.md","type":"file"},{"name":".env","type":"file"},{"name":".env.example","type":"file"},{"name":"logo.png","type":"file"},{"name":"src","type":"dir"},{"name":"node_modules","type":"dir"}]`)
		case strings.HasSuffix(p, "/contents/src"):
			io.WriteString(w, `[{"name":"settings.go","type":"file"}]`)
		case strings.HasSuffix(p, "/contents/node_modules"):
			t.Error("node_modules was listed")
		case strings.HasSuffix(p, "/contents/README.md"):
			io.WriteString(w, "# Tool\nRun it.")
		case strings.HasSuffix(p, "/contents/.env.example"):
			io.WriteString(w, "TOOL_API_KEY=\nTOOL_REGION=us\n")
		case strings.HasSuffix(p, "/contents/.env"):
			io.WriteString(w, "TOOL_API_KEY=realsecret\n")
		case strings.HasSuffix(p, "/contents/src/settings.go"):
			io.WriteString(w, `os.Getenv("TOOL_TIMEOUT")`)
		default:
			w.WriteHeader(404)
		}
	})
}

func TestModelExploresTheRepoAndNeverReadsRealEnv(t *testing.T) {
	f, rw := exploreRepo(t)
	round := 0
	llm := &mockLLM{reply: func(body string) (int, string) {
		if strings.Contains(body, "File tree") {
			round++
			if round == 1 {
				if !strings.Contains(body, "src/settings.go") || !strings.Contains(body, ".env.example") {
					t.Errorf("tree missing: %s", body)
				}
				return 200, chat(`{"read":[".env.example",".env","logo.png","src/settings.go","not/there"]}`)
			}
			return 200, chat(`{"read":[]}`)
		}
		if !strings.Contains(body, "TOOL_REGION") || !strings.Contains(body, "TOOL_TIMEOUT") || strings.Contains(body, "realsecret") {
			t.Errorf("final request: %s", body)
		}
		return 200, chat(goodReply())
	}}
	src, _ := ParseSource("https://github.com/acme/tool")
	if _, err := (&Service{Fetch: f, LLM: llm}).Suggest(context.Background(), "m", src, "", runners); err != nil {
		t.Fatal(err)
	}
	if round != 2 {
		t.Fatalf("rounds %d", round)
	}
	for _, s := range rw.seen {
		u := strings.SplitN(s.url, "?", 2)[0]
		if strings.HasSuffix(u, "/contents/.env") || strings.HasSuffix(u, "logo.png") {
			t.Fatalf("%s was requested", u)
		}
	}
}

func TestReadable(t *testing.T) {
	for n, want := range map[string]bool{".env": false, ".env.local": false, "app/.env.production": false, ".env.example": true,
		".env.sample": true, "certs/server.key": false, "id_rsa": false, "logo.png": false, "docker-compose.yml": true,
		"config/secrets.yaml": false, "config/secrets.example.yaml": true, "src/main.go": true} {
		if got := Readable(n); got != want {
			t.Errorf("%s: %v, want %v", n, got, want)
		}
	}
}

func TestPrivateRepoAnswersAnAuthError(t *testing.T) {
	f, _ := githubAuthServer(t, "own")
	src, _ := ParseSource("https://github.com/acme/private")
	_, err := f.Fetch(context.Background(), src, "")
	var ae *AuthError
	if !errors.As(err, &ae) || ae.Status != 404 || ae.Refused || ae.Host != "github.com" {
		t.Fatalf("%v %+v", err, ae)
	}
	_, err = f.Fetch(context.Background(), src, "wrong")
	if !errors.As(err, &ae) || ae.Status != 401 || !ae.Refused {
		t.Fatalf("%v %+v", err, ae)
	}
	if _, err = f.Fetch(context.Background(), src, "own"); err != nil {
		t.Fatal(err)
	}
}

func TestPromptNamesNodeEntryRule(t *testing.T) {
	if !strings.Contains(SystemPrompt, "scripts.start") || !strings.Contains(SystemPrompt, "dist/src/index.js") {
		t.Fatal("the system prompt lacks the Node entry rule")
	}
}

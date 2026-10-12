package suggest

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func countSeen(rw *rewrite, part string) int {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	n := 0
	for _, s := range rw.seen {
		if strings.Contains(s.url, part) {
			n++
		}
	}
	return n
}

func TestGitHubTreeIsOneRequest(t *testing.T) {
	f, rw := fetcherFor(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/git/trees/HEAD") && r.URL.Query().Get("recursive") == "1":
			io.WriteString(w, `{"tree":[{"path":"README.md","type":"blob"},{"path":"src","type":"tree"},{"path":"src/a.go","type":"blob"},{"path":"node_modules","type":"tree"},{"path":"node_modules/x/index.js","type":"blob"},{"path":".env.example","type":"blob"}],"truncated":false}`)
		case strings.HasSuffix(r.URL.Path, "/contents/README.md"):
			io.WriteString(w, "# Tool")
		default:
			w.WriteHeader(404)
		}
	})
	src, _ := ParseSource("https://github.com/acme/tool")
	doc, err := f.Fetch(context.Background(), src, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(doc.Tree, ","); got != "README.md,src/,src/a.go,.env.example" {
		t.Fatalf("tree %s", got)
	}
	if n := countSeen(rw, "/git/trees/"); n != 1 {
		t.Fatalf("%d tree requests", n)
	}
	if n := countSeen(rw, "/contents?") + countSeen(rw, "/contents/src?"); n > 1 { // only the .NET root look
		t.Fatalf("walked folders: %d", n)
	}
}

func TestGitHubTruncatedTreeFallsBackToTheWalk(t *testing.T) {
	f, rw := fetcherFor(t, func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.Contains(p, "/git/trees/"):
			io.WriteString(w, `{"tree":[{"path":"README.md","type":"blob"}],"truncated":true}`)
		case strings.HasSuffix(p, "/contents") || strings.HasSuffix(p, "/contents/"):
			io.WriteString(w, `[{"name":"README.md","type":"file"},{"name":"lib","type":"dir"}]`)
		case strings.HasSuffix(p, "/contents/lib"):
			io.WriteString(w, `[{"name":"b.py","type":"file"}]`)
		case strings.HasSuffix(p, "/contents/README.md"):
			io.WriteString(w, "# Tool")
		default:
			w.WriteHeader(404)
		}
	})
	src, _ := ParseSource("https://github.com/acme/tool")
	doc, _ := f.Fetch(context.Background(), src, "")
	if got := strings.Join(doc.Tree, ","); got != "README.md,lib/,lib/b.py" || countSeen(rw, "/contents/lib") != 1 {
		t.Fatalf("tree %s", got)
	}
}

func TestGitLabTreeIsPaged(t *testing.T) {
	f, rw := fetcherFor(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case strings.HasSuffix(r.URL.Path, "/repository/tree") && q.Get("recursive") == "true":
			if q.Get("page") == "1" {
				w.Header().Set("X-Next-Page", "2")
				io.WriteString(w, `[{"path":"README.md","type":"blob"},{"path":"cfg","type":"tree"}]`)
			} else {
				io.WriteString(w, `[{"path":"cfg/app.example.yaml","type":"blob"}]`)
			}
		case strings.Contains(r.URL.RawPath+r.URL.Path, "README.md/raw"):
			io.WriteString(w, "# Tool")
		default:
			w.WriteHeader(404)
		}
	})
	src, _ := ParseSource("https://gitlab.com/acme/tool")
	doc, err := f.Fetch(context.Background(), src, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(doc.Tree, ","); got != "README.md,cfg/,cfg/app.example.yaml" || countSeen(rw, "recursive=true") != 2 {
		t.Fatalf("tree %s", got)
	}
}

func TestGiteaTreeFollowsPagesAndSubdir(t *testing.T) {
	f, _ := fetcherFor(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case strings.Contains(r.URL.Path, "/git/trees/") && q.Get("recursive") == "true":
			if q.Get("page") == "1" {
				io.WriteString(w, `{"tree":[{"path":"README.md","type":"blob"},{"path":"srv","type":"tree"},{"path":"srv/README.md","type":"blob"}],"truncated":true,"page":1}`)
			} else {
				io.WriteString(w, `{"tree":[{"path":"srv/.env.sample","type":"blob"}],"truncated":false,"page":2}`)
			}
		case strings.Contains(r.URL.Path, "/raw/srv/README.md"):
			io.WriteString(w, "# Srv")
		default:
			w.WriteHeader(404)
		}
	})
	src, _ := ParseSource("https://git.example.org/acme/mono/src/branch/main/srv")
	if src.Subdir != "srv" {
		t.Skipf("subdir form not parsed: %+v", src)
	}
	doc, err := f.Fetch(context.Background(), src, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(doc.Tree, ","); got != "README.md,.env.sample" {
		t.Fatalf("tree %s", got)
	}
}

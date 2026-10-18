package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// `skgate stdio` is what a directory runs to look at skgate. It must answer in seconds with an empty
// environment (no OIDC settings, no HOME), write nothing to its working folder, and keep stdout for
// protocol messages only.
func TestStdioModeAnswersWithAnEmptyEnvironment(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "skgate")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	work := t.TempDir()
	cmd := exec.Command(bin, "stdio")
	cmd.Dir = work
	cmd.Env = []string{} // nothing: no PUBLIC_URL, no OIDC_*, no HOME
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	rd := bufio.NewReader(out)
	ask := func(req string) map[string]any {
		t.Helper()
		if _, err := io.WriteString(in, req+"\n"); err != nil {
			t.Fatal(err)
		}
		type res struct {
			line string
			err  error
		}
		ch := make(chan res, 1)
		go func() { l, err := rd.ReadString('\n'); ch <- res{l, err} }()
		select {
		case r := <-ch:
			var m map[string]any
			if err := json.Unmarshal([]byte(r.line), &m); err != nil {
				t.Fatalf("stdout carries something that is not a protocol message: %q (stderr %q)", r.line, stderr.String())
			}
			return m
		case <-time.After(5 * time.Second):
			t.Fatalf("no answer to %s within 5 s (stderr %q)", req, stderr.String())
			return nil
		}
	}
	if m := ask(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`); m["result"] == nil {
		t.Fatalf("initialize: %v", m)
	}
	m := ask(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if n := len(m["result"].(map[string]any)["tools"].([]any)); n != 3 {
		t.Fatalf("tools/list returned %d tools", n)
	}
	if m := ask(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"about"}}`); m["result"].(map[string]any)["isError"] == true {
		t.Fatalf("about: %v", m)
	}
	_ = in.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("exit after stdin closed: %v (stderr %q)", err, stderr.String())
	}
	if left, _ := os.ReadDir(work); len(left) != 0 {
		t.Errorf("stdio mode wrote to its working folder: %v", left)
	}
}

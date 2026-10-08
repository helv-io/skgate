package managed

import "testing"

func TestPackageOf(t *testing.T) {
	for _, tc := range []struct {
		cmd       string
		args      []string
		shell     bool
		ok        bool
		name, ver string
		pinned    bool
	}{
		{cmd: "npx", args: []string{"-y", "@modelcontextprotocol/server-everything"}, ok: true, name: "@modelcontextprotocol/server-everything"},
		{cmd: "npx", args: []string{"-y", "@scope/pkg@1.2.3"}, ok: true, name: "@scope/pkg", ver: "1.2.3", pinned: true},
		{cmd: "npx", args: []string{"--yes", "pkg@latest", "--flag"}, ok: true, name: "pkg", ver: "latest"},
		{cmd: "/usr/bin/npx", args: []string{"pkg@2.0.0-beta.1"}, ok: true, name: "pkg", ver: "2.0.0-beta.1", pinned: true},
		{cmd: "npx", args: []string{"pkg@^1.2.0"}, ok: true, name: "pkg", ver: "^1.2.0"},
		{cmd: "npx", args: []string{"pkg@1"}, ok: true, name: "pkg", ver: "1", pinned: true},
		{cmd: "npx", args: []string{"-p", "real-pkg@3.1.0", "-c", "bin --x"}, ok: true, name: "real-pkg", ver: "3.1.0", pinned: true},
		{cmd: "npx", args: []string{"--package=other@1.0.0", "bin"}, ok: true, name: "other", ver: "1.0.0", pinned: true},
		{cmd: "npx", args: []string{"--registry", "https://r.example.com", "pkg"}, ok: true, name: "pkg"},
		{cmd: "uvx", args: []string{"mcp-server-time"}, ok: true, name: "mcp-server-time"},
		{cmd: "uvx", args: []string{"mcp-server-time==0.6.2"}, ok: true, name: "mcp-server-time", ver: "0.6.2", pinned: true},
		{cmd: "uvx", args: []string{"mcp-server-time@1.0.0"}, ok: true, name: "mcp-server-time", ver: "1.0.0", pinned: true},
		{cmd: "uvx", args: []string{"pkg@latest"}, ok: true, name: "pkg", ver: "latest"},
		{cmd: "uvx", args: []string{"pkg>=1.0"}, ok: true, name: "pkg", ver: ">=1.0"},
		{cmd: "uvx", args: []string{"pkg==1.*"}, ok: true, name: "pkg", ver: "1.*"},
		{cmd: "uvx", args: []string{"--python", "3.12", "--from", "git-ish==2.0", "tool"}, ok: true, name: "git-ish", ver: "2.0", pinned: true},
		{cmd: "uvx", args: []string{"--from=pkg", "tool"}, ok: true, name: "pkg"},
		{cmd: "uvx", args: []string{"--with", "extra==1.0", "main-pkg"}, ok: true, name: "main-pkg"},
		{cmd: "pnpm", args: []string{"dlx", "pkg@1.0.0"}, ok: true, name: "pkg", ver: "1.0.0", pinned: true},
		{cmd: "bunx", args: []string{"pkg"}, ok: true, name: "pkg"},
		{cmd: "bun", args: []string{"x", "@scope/pkg@2.0.0"}, ok: true, name: "@scope/pkg", ver: "2.0.0", pinned: true},
		{cmd: "bun", args: []string{"run", "index.ts"}},
		{cmd: "deno", args: []string{"run", "-A", "npm:@scope/pkg@1.2.3", "--stdio"}, ok: true, name: "@scope/pkg", ver: "1.2.3", pinned: true},
		{cmd: "deno", args: []string{"run", "--allow-net=example.com", "--config", "deno.json", "jsr:@scope/pkg"}, ok: true, name: "@scope/pkg"},
		{cmd: "deno", args: []string{"run", "-A", "main.ts"}},
		{cmd: "deno", args: []string{"task", "start"}},
		{cmd: "uv", args: []string{"tool", "run", "pkg==1.0"}, ok: true, name: "pkg", ver: "1.0", pinned: true},
		{cmd: "pipx", args: []string{"run", "pkg"}, ok: true, name: "pkg"},
		{cmd: "pnpm", args: []string{"install"}},
		{cmd: "node", args: []string{"server.js"}},
		{cmd: "python3", args: []string{"-m", "srv"}},
		{cmd: "npx", args: []string{"-y"}},
		{cmd: "npx", args: []string{"pkg@1.0.0"}, shell: true},
	} {
		got, ok := PackageOf(Spec{Command: tc.cmd, Args: tc.args, Shell: tc.shell})
		if ok != tc.ok {
			t.Errorf("%s %v: ok=%v", tc.cmd, tc.args, ok)
			continue
		}
		if ok && (got.Name != tc.name || got.Version != tc.ver || got.Pinned != tc.pinned) {
			t.Errorf("%s %v: got %+v", tc.cmd, tc.args, got)
		}
	}
}

package managed

import (
	"os"
	"path/filepath"
	"strings"
)

// Command is a program that exists on the child's PATH.
type Command struct {
	Name string
	Hint string
}

// commonCommands are the programs offered in the admin form, in display order.
var commonCommands = []Command{
	{"npx", "run an npm package"},
	{"bunx", "run an npm package with bun"},
	{"pnpm", "pnpm (pnpm dlx runs a package)"},
	{"npm", "npm"},
	{"node", "run a JavaScript file"},
	{"deno", "run a JavaScript or TypeScript file"},
	{"uvx", "run a Python package"},
	{"uv", "uv (uv run, uv tool run)"},
	{"pipx", "run a Python package"},
	{"python3", "run a Python file"},
	{"python", "run a Python file"},
	{"git", "git"},
	{"docker", "run a container"},
	{"sh", "POSIX shell"},
	{"bash", "bash"},
}

// AvailableCommands lists the common commands that exist and are executable on the PATH the
// children get (the allow-listed parent PATH, or the default).
func (m *Manager) AvailableCommands() []Command {
	env := BuildEnv(m.o.Environ(), "", "", nil)
	path := ""
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "PATH="); ok {
			path = v
		}
	}
	var out []Command
	for _, c := range commonCommands {
		for _, d := range filepath.SplitList(path) {
			if d == "" {
				continue
			}
			if st, err := os.Stat(filepath.Join(d, c.Name)); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

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
	{"bunx", "run an npm package with Bun"},
	{"pnpm", "run an npm package with pnpm dlx"},
	{"npm", "npm"},
	{"node", "run a JavaScript file"},
	{"bun", "run a JavaScript or TypeScript file"},
	{"deno", "run a script or an npm package"},
	{"uvx", "run a Python package"},
	{"uv", "run a Python project or tool"},
	{"pipx", "run a Python package"},
	{"python3", "run a Python file"},
	{"python", "run a Python file"},
	{"dotnet", "build and run a .NET project"},
	{"go", "build and run a Go module"},
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

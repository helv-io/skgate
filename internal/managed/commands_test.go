package managed

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAvailableCommandsListsOnlyExecutablesOnPath(t *testing.T) {
	dir := t.TempDir()
	for name, mode := range map[string]os.FileMode{"npx": 0o755, "uvx": 0o755, "node": 0o644, "sh": 0o755, "other-tool": 0o755} {
		os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), mode)
	}
	os.Mkdir(filepath.Join(dir, "git"), 0o755) // a directory is not a command
	m, _ := testMgr(t, func(o *Options) { o.Environ = func() []string { return []string{"PATH=" + dir} } })
	var got []string
	for _, c := range m.AvailableCommands() {
		got = append(got, c.Name)
		if c.Hint == "" {
			t.Errorf("%s has no hint", c.Name)
		}
	}
	if len(got) != 3 || got[0] != "npx" || got[1] != "uvx" || got[2] != "sh" {
		t.Fatalf("%v", got)
	}
}

package managed

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func own() *RunAs { return &RunAs{UID: os.Geteuid(), GID: os.Getegid()} }

func TestConfineSetsRunnerCredential(t *testing.T) {
	m := &Manager{o: Options{RunAs: &RunAs{UID: 65534, GID: 65534}}}
	cmd := exec.Command("true")
	m.confine(cmd)
	c := cmd.SysProcAttr.Credential
	if !cmd.SysProcAttr.Setpgid || c == nil || c.Uid != 65534 || c.Gid != 65534 || len(c.Groups) != 0 {
		t.Fatalf("%+v %+v", cmd.SysProcAttr, c)
	}
	cmd = exec.Command("true")
	(&Manager{}).confine(cmd)
	if cmd.SysProcAttr.Credential != nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("without a runner identity children keep skgate's own")
	}
}

func TestHandOverCreatesAndOpensTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "cache")
	m := &Manager{}
	if err := m.handOver(dir); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("without a runner the directory stays private: %v", fi.Mode().Perm())
	}
	m = &Manager{o: Options{RunAs: own()}}
	other := filepath.Join(t.TempDir(), "x")
	if err := m.handOver(other); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(other); fi.Mode().Perm() != 0o755 {
		t.Errorf("runner directory mode %v", fi.Mode().Perm())
	}
}

func TestTraversableOpensOnlyDirectoriesWeOwn(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "data", "managed", "alias")
	os.MkdirAll(deep, 0o750)
	secret := filepath.Join(root, "data", "skgate.db")
	os.WriteFile(secret, []byte("x"), 0o600)
	(&Manager{}).traversable(deep) // no runner: untouched
	if fi, _ := os.Stat(deep); fi.Mode().Perm() != 0o750 {
		t.Fatalf("%v", fi.Mode().Perm())
	}
	(&Manager{o: Options{RunAs: own()}}).traversable(deep)
	for _, d := range []string{deep, filepath.Dir(deep), filepath.Join(root, "data")} {
		if fi, _ := os.Stat(d); fi.Mode().Perm() != 0o751 {
			t.Errorf("%s: %v", d, fi.Mode().Perm())
		}
	}
	if fi, _ := os.Stat(secret); fi.Mode().Perm() != 0o600 {
		t.Errorf("files are never opened: %v", fi.Mode().Perm())
	}
}

func TestRunnerCapabilities(t *testing.T) {
	all := uint64(1<<capChown | 1<<capKill | 1<<capSetGid | 1<<capSetUID)
	if !canRunAs(all) || !canRunAs(all|1<<1) {
		t.Fatal("chown, kill, setgid and setuid are enough")
	}
	for _, miss := range []uint64{1 << capChown, 1 << capKill, 1 << capSetGid, 1 << capSetUID} {
		if canRunAs(all &^ miss) {
			t.Errorf("missing %x must not be enough", miss)
		}
	}
	if canRunAs(0) {
		t.Fatal("no capabilities")
	}
}

// With root, children really run as nobody, can use their directory, and cannot read what is private.
func TestRunnerIsNobodyAsRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	root := t.TempDir()
	os.Chmod(root, 0o751)
	secret := filepath.Join(root, "skgate.db")
	os.WriteFile(secret, []byte("secret"), 0o600)
	m := &Manager{o: Options{RunAs: &RunAs{UID: NobodyID, GID: NobodyID}}}
	work := filepath.Join(root, "managed", "a", "work")
	m.traversable(root)
	if err := m.handOver(work); err != nil {
		t.Fatal(err)
	}
	m.traversable(filepath.Dir(work))
	run := func(script string) (string, error) {
		cmd := exec.Command("/bin/sh", "-c", script)
		cmd.Dir = work
		m.confine(cmd)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if out, err := run("id -u; id -g; echo ok > f && cat f"); err != nil || out != "65534\n65534\nok" {
		t.Fatalf("%q %v", out, err)
	}
	if out, err := run("cat " + secret); err == nil {
		t.Fatalf("the runner read the database: %q", out)
	}
	if err := m.removeAll(filepath.Join(root, "managed", "a")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "managed", "a")); err == nil {
		t.Fatal("not removed")
	}
}

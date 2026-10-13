package managed

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func own() *RunAs { return &RunAs{UID: os.Geteuid(), GID: os.Getegid()} }

func TestConfineSetsThePerUpstreamCredential(t *testing.T) {
	m := &Manager{o: Options{RunAs: &RunAs{UID: NobodyID, GID: NobodyID}, Dir: t.TempDir()}}
	cmd := exec.Command("true")
	m.confine(cmd, "a")
	c := cmd.SysProcAttr.Credential
	if !cmd.SysProcAttr.Setpgid || c == nil || c.Uid < RunnerIDBase || c.Uid >= RunnerIDBase+RunnerIDCount || c.Gid != c.Uid || len(c.Groups) != 0 {
		t.Fatalf("%+v %+v", cmd.SysProcAttr, c)
	}
	cmd2 := exec.Command("true")
	m.confine(cmd2, "b")
	if cmd2.SysProcAttr.Credential.Uid == c.Uid {
		t.Fatal("two upstreams must not share a uid")
	}
	cmd = exec.Command("true")
	(&Manager{}).confine(cmd, "a")
	if cmd.SysProcAttr.Credential != nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("without a runner identity children keep skgate's own")
	}
}

func TestRunnerIDsAreUniqueAndKeptAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{o: Options{RunAs: own(), Dir: dir}}
	seen := map[int]string{}
	for i := 0; i < 300; i++ {
		a := "up" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + itoa(i)
		id := m.runner(a).UID
		if other, dup := seen[id]; dup {
			t.Fatalf("%s and %s share %d", a, other, id)
		}
		seen[id] = a
	}
	first := m.runner("alpha").UID
	if fi, err := os.Stat(filepath.Join(dir, runnerIDsFile)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("ids file: %v %v", fi, err)
	}
	again := &Manager{o: Options{RunAs: own(), Dir: dir}}
	if again.runner("alpha").UID != first {
		t.Fatal("an upstream must keep its uid after a restart")
	}
	if (&Manager{}).runner("alpha") != nil || (&Manager{}).RunnerOf("alpha") != "" {
		t.Fatal("no runner without the ability to switch users")
	}
}

func TestHandOverWithoutRunnerKeepsTheDirectoryPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "cache")
	m := &Manager{}
	if err := m.handOver(dir, "a"); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("without a runner the directory stays private: %v", fi.Mode().Perm())
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

// With root, each upstream's children run as their own uid, can use their directory, and can read
// neither skgate's private files nor another upstream's directory.
func TestRunnersAreSeparatedAsRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	root := t.TempDir()
	os.Chmod(root, 0o751)
	secret := filepath.Join(root, "skgate.db")
	os.WriteFile(secret, []byte("secret"), 0o600)
	m := &Manager{o: Options{RunAs: &RunAs{UID: NobodyID, GID: NobodyID}, Dir: filepath.Join(root, "managed")}}
	work := filepath.Join(root, "managed", "a", "work")
	otherWork := filepath.Join(root, "managed", "b", "work")
	m.traversable(root)
	for alias, d := range map[string]string{"a": work, "b": otherWork} {
		if err := m.handOver(d, alias); err != nil {
			t.Fatal(err)
		}
		m.traversable(filepath.Dir(d))
	}
	if fi, _ := os.Stat(work); fi.Mode().Perm() != 0o750 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	os.WriteFile(filepath.Join(otherWork, "token"), []byte("b-secret"), 0o644)
	m.handOver(otherWork, "b")
	run := func(alias, script string) (string, error) {
		cmd := exec.Command("/bin/sh", "-c", script)
		cmd.Dir = work
		m.confine(cmd, alias)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	want := m.runner("a").UID
	if out, err := run("a", "id -u; echo ok > f && cat f"); err != nil || out != itoa(want)+"\nok" {
		t.Fatalf("%q %v", out, err)
	}
	if out, err := run("a", "cat "+secret); err == nil {
		t.Fatalf("the runner read the database: %q", out)
	}
	if out, err := run("a", "cat "+filepath.Join(otherWork, "token")); err == nil {
		t.Fatalf("one runner read another's files: %q", out)
	}
	if _, err := os.ReadFile(filepath.Join(work, "f")); err != nil {
		t.Fatalf("skgate must still read a runner's directory: %v", err)
	}
	if err := m.removeAll(filepath.Join(root, "managed", "a"), "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "managed", "a")); err == nil {
		t.Fatal("not removed")
	}
}

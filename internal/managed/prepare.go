package managed

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// prepare runs the steps before the command starts: the git checkout (git upstreams) and the
// install step. The install step runs once per change of its inputs (command, environment,
// checkout revision) and again on demand (Sync), not on every start.
func (p *Proc) prepare(ctx context.Context, spec Spec, work string, env []string, force bool) error {
	if spec.Git != nil {
		if err := p.syncGit(ctx, spec, work, force); err != nil {
			return err
		}
	}
	return p.install(ctx, spec, work, env, force)
}

// install runs the install step when its inputs changed (or force is set) and records the stamp.
func (p *Proc) install(ctx context.Context, spec Spec, work string, env []string, force bool) error {
	if strings.TrimSpace(spec.Install) == "" {
		return nil
	}
	base, _ := p.m.AliasDir(spec.Alias)
	stamp := filepath.Join(base, "installed")
	want := spec.installStamp(p.gitRev(work, spec))
	if have, err := os.ReadFile(stamp); err == nil && string(have) == want && !force {
		return nil
	}
	p.setState(StateStarting, "installing")
	p.note("install: running")
	if err := p.runStep(ctx, "install", work, env, "/bin/sh", "-c", spec.Install); err != nil {
		_ = os.Remove(stamp)
		return fmt.Errorf("install step failed: %w", err)
	}
	return os.WriteFile(stamp, []byte(want), 0o600)
}

// runStep runs one helper command in its own process group with output captured into the log ring.
func (p *Proc) runStep(ctx context.Context, src, dir string, env []string, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, p.m.o.InstallMax)
	defer cancel()
	bin, err := lookPath(name, env, dir)
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir, cmd.Env = dir, env
	setGroup(cmd)
	w := &lineWriter{fn: func(s string) {
		s = p.redactLine(s)
		p.ring.Add(src, s)
		p.m.o.Logf("managed[%s] %s: %s", p.alias, src, clipLine(s))
	}}
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot run: %s", cleanExecErr(err))
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		w.Flush()
		if err != nil {
			return fmt.Errorf("%s%s", exitText(err), lastLine(p.ring, src))
		}
		return nil
	case <-ctx.Done():
		signalGroup(cmd.Process.Pid, true)
		<-done
		w.Flush()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("timed out after %s", p.m.o.InstallMax.Round(time.Second))
		}
		return ctx.Err()
	}
}

func lastLine(r *Ring, src string) string {
	if l := lastOf(r.Last(20), src); len(l) > 0 {
		return "; " + clipText(l[0].Text, 200)
	}
	return ""
}

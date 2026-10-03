package managed

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitAuthValue is the Authorization header sent to the repository host. Basic auth with the token
// as the password is accepted by the common git hosts for personal access tokens.
func gitAuthValue(token string) string {
	return "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token))
}

// gitEnv returns the environment for git steps: the child's environment plus settings that keep git
// non-interactive and independent of any user or system configuration. The token travels only as a
// config value in the environment (http.extraHeader), never in argv, and is redacted from logs.
func gitEnv(base []string, g *GitSpec) []string {
	env := append([]string(nil), base...)
	env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ASKPASS=/bin/true", "GCM_INTERACTIVE=never")
	n := 0
	add := func(k, v string) {
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", n, k), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", n, v))
		n++
	}
	add("protocol.ext.allow", "never")
	if g != nil && g.Token != "" {
		add("http.extraHeader", gitAuthValue(g.Token))
	}
	return append(env, fmt.Sprintf("GIT_CONFIG_COUNT=%d", n))
}

func (p *Proc) sourceStamp(spec Spec) string { return spec.Git.URL + "\x00" + spec.Git.Ref }

// syncGit makes work a checkout of the configured ref. The network is used when there is no
// checkout yet, when the repository or ref changed, or when force is set (the admin's Update).
func (p *Proc) syncGit(ctx context.Context, spec Spec, work string, force bool) error {
	base, _ := p.m.AliasDir(spec.Alias)
	stampFile := filepath.Join(base, "source")
	have, _ := os.ReadFile(stampFile)
	_, gitErr := os.Stat(filepath.Join(work, ".git"))
	if gitErr == nil && string(have) == p.sourceStamp(spec) && !force {
		return nil
	}
	p.setState(StateStarting, "updating")
	env := gitEnv(p.childEnv(spec, filepath.Join(base, "home"), filepath.Join(base, "tmp")), spec.Git)
	git := func(args ...string) error { return p.runStep(ctx, "git", work, env, "git", args...) }
	if gitErr != nil {
		p.note("git: cloning")
		if err := git("init", "-q"); err != nil {
			return fmt.Errorf("git init: %w", err)
		}
		if err := git("remote", "add", "origin", spec.Git.URL); err != nil {
			return fmt.Errorf("git remote add: %w", err)
		}
	} else {
		p.note("git: updating")
		if err := git("remote", "set-url", "origin", spec.Git.URL); err != nil {
			return fmt.Errorf("git remote set-url: %w", err)
		}
	}
	ref := spec.Git.Ref
	if ref == "" {
		ref = "HEAD"
	}
	if err := git("fetch", "--depth", "1", "--no-tags", "--quiet", "origin", ref); err != nil {
		return fmt.Errorf("git fetch: %w", err)
	}
	if err := git("checkout", "--quiet", "--force", "--detach", "FETCH_HEAD"); err != nil {
		return fmt.Errorf("git checkout: %w", err)
	}
	// Untracked files go, ignored files stay (installed dependencies live there).
	if err := git("clean", "-fdq"); err != nil {
		return fmt.Errorf("git clean: %w", err)
	}
	if err := os.WriteFile(stampFile, []byte(p.sourceStamp(spec)), 0o600); err != nil {
		return err
	}
	p.setRev(work)
	p.note("git: at %s", p.Rev())
	return nil
}

// gitRevFull returns the checked out commit (40 hex), or "" when there is none.
func (p *Proc) gitRevFull(work string) string {
	env := gitEnv(BuildEnv(p.m.o.Environ(), work, work, nil), nil)
	bin, err := lookPath("git", env, work)
	if err != nil {
		return ""
	}
	cmd := exec.Command(bin, "rev-parse", "HEAD")
	cmd.Dir, cmd.Env = work, env
	p.m.confine(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitRev returns the checked out commit (short), or "" when there is none.
func (p *Proc) gitRev(work string, spec Spec) string {
	if spec.Git == nil {
		return ""
	}
	return shortRev(p.gitRevFull(work))
}

func shortRev(full string) string {
	if len(full) > 12 {
		return full[:12]
	}
	return full
}

// setRev records the installed commit after a checkout.
func (p *Proc) setRev(work string) {
	full := p.gitRevFull(work)
	p.mu.Lock()
	p.revFull, p.rev = full, shortRev(full)
	p.mu.Unlock()
}

// Rev is the installed commit of a git upstream (short), for display. It is read from the checkout
// once and then kept up to date by every sync.
func (p *Proc) Rev() string {
	p.mu.Lock()
	r := p.rev
	p.mu.Unlock()
	if r != "" {
		return r
	}
	spec := p.Spec()
	if spec.Git == nil {
		return ""
	}
	base, err := p.m.AliasDir(spec.Alias)
	if err != nil {
		return ""
	}
	work := filepath.Join(base, "repo")
	if _, err := os.Stat(filepath.Join(work, ".git")); err != nil {
		return ""
	}
	p.setRev(work)
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rev
}

var errNoGit = errors.New("not a git upstream")

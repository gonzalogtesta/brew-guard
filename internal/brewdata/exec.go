package brewdata

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	runTimeout = 90 * time.Second
	maxOutput  = 32 << 20
	safePath   = "/usr/bin:/bin:/usr/sbin:/sbin"
)

// Candidates are the places Homebrew installs its brew command.
var Candidates = []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew", "/home/linuxbrew/.linuxbrew/bin/brew"}

// FindBrew returns the resolved absolute path of the first usable brew.
// It must be a regular executable file, owned by root or the current user,
// and not writable by group or others.
func FindBrew(candidates []string) (string, error) {
	var lastErr error = errors.New("no brew found in " + strings.Join(candidates, ", "))
	for _, c := range candidates {
		p, err := filepath.EvalSymlinks(c)
		if err != nil {
			continue
		}
		if err := checkExecutable(p); err != nil {
			lastErr = fmt.Errorf("%s: %w", p, err)
			continue
		}
		return p, nil
	}
	return "", lastErr
}

func checkExecutable(p string) error {
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0o111 == 0 {
		return errors.New("not a regular executable file")
	}
	if st.Mode().Perm()&0o022 != 0 {
		return errors.New("writable by group or others")
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		if sys.Uid != 0 && int(sys.Uid) != os.Geteuid() {
			return errors.New("owned by another user")
		}
	}
	return nil
}

// Env builds the environment brew runs with. Only a short allowlist of the
// caller's variables is kept; every other HOMEBREW_* and all DYLD_*/LD_*
// variables are dropped so an injected redirect cannot reach brew.
func Env(parent []string) []string {
	keep := map[string]bool{"HOME": true, "USER": true, "LOGNAME": true, "TMPDIR": true, "LANG": true, "LC_ALL": true, "TERM": true}
	out := []string{"PATH=" + safePath}
	for _, kv := range parent {
		k, _, _ := strings.Cut(kv, "=")
		if keep[k] {
			out = append(out, kv)
		}
	}
	return append(out,
		"HOMEBREW_NO_AUTO_UPDATE=1",
		"HOMEBREW_NO_ANALYTICS=1",
		"HOMEBREW_NO_ENV_HINTS=1",
		"HOMEBREW_NO_INSECURE_REDIRECT=1",
	)
}

// ExecRunner runs a pinned brew binary.
type ExecRunner struct {
	Brew   string
	Parent []string // caller's environment, usually os.Environ()
}

func (e ExecRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.Brew, args...)
	cmd.Env = Env(e.Parent)
	var out, errb limited
	out.max, errb.max = maxOutput, 1<<20
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("brew %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	if out.over {
		return nil, errors.New("brew output too large")
	}
	return out.Bytes(), nil
}

// limited is a bytes.Buffer that stops growing at max and notes the overflow.
type limited struct {
	bytes.Buffer
	max  int
	over bool
}

func (l *limited) Write(p []byte) (int, error) {
	if l.Len()+len(p) > l.max {
		l.over = true
		return len(p), nil
	}
	return l.Buffer.Write(p)
}

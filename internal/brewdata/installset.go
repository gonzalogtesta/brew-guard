package brewdata

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var flagRe = regexp.MustCompile(`^--?[A-Za-z][A-Za-z-]*$`)

// SetItem is one package brew says it would install.
type SetItem struct {
	Name    string
	Version string // as brew prints it, a revision may be attached (2.3.3_1)
	Via     string // empty for a package that was asked for, else the package it is a dependency of
	From    string // for an upgrade, the version installed now; empty for a new install
}

// InstallSet asks brew what installing the named packages would install:
// the packages themselves and every dependency that is not installed yet.
// It runs `brew install --dry-run`, which installs nothing. An empty result
// means brew had nothing to install (for example everything is installed).
func InstallSet(ctx context.Context, r Runner, flags []string, names ...string) ([]SetItem, error) {
	if len(names) == 0 {
		return nil, errors.New("no package names given")
	}
	args := []string{"install", "--dry-run"}
	for _, f := range flags {
		if !flagRe.MatchString(f) {
			return nil, fmt.Errorf("invalid option %q", f)
		}
		args = append(args, f)
	}
	for _, n := range names {
		if !ValidName(n) {
			return nil, fmt.Errorf("invalid package name %q", n)
		}
		args = append(args, n)
	}
	b, err := r.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	return ParseDryRun(b)
}

// ParseDryRun reads the output of `brew install --dry-run`: the
// "==> Would install N ...:" sections (lines "name version") and the
// "==> Would upgrade N ...:" sections (lines "name old -> new"). brew upgrades
// outdated dependencies that are already installed, so they are part of what
// an install changes. A section whose line count does not match its header, or
// a line that is not in that shape, is an error: a wrong guess here would hide
// a package from the policy.
func ParseDryRun(b []byte) ([]SetItem, error) {
	const (
		installPrefix = "==> Would install "
		upgradePrefix = "==> Would upgrade "
	)
	var (
		out      []SetItem
		via      string
		inside   bool
		upgrade  bool
		want, n  int
		sawTitle string
	)
	finish := func() error {
		if inside && n != want {
			return fmt.Errorf("brew dry-run: %q announced %d packages but listed %d", sawTitle, want, n)
		}
		inside = false
		return nil
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, " \r")
		switch {
		case strings.HasPrefix(line, installPrefix) || strings.HasPrefix(line, upgradePrefix):
			if err := finish(); err != nil {
				return nil, err
			}
			upgrade = strings.HasPrefix(line, upgradePrefix)
			title := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(line, installPrefix), upgradePrefix), ":")
			count, rest, _ := strings.Cut(title, " ")
			c, err := strconv.Atoi(count)
			if err != nil || c < 0 {
				return nil, fmt.Errorf("brew dry-run: cannot read the count in %q", line)
			}
			via = ""
			if _, target, ok := strings.Cut(rest, " for "); ok {
				via = strings.TrimSpace(target)
				if !ValidName(via) {
					return nil, fmt.Errorf("brew dry-run: odd package name in %q", line)
				}
			}
			inside, want, n, sawTitle = true, c, 0, title
		case strings.HasPrefix(line, "==>") || line == "":
			if err := finish(); err != nil {
				return nil, err
			}
		case inside:
			f := strings.Fields(line)
			if len(f) == 0 || len(f) > 4 || !ValidName(f[0]) {
				return nil, fmt.Errorf("brew dry-run: cannot read the line %q", line)
			}
			it := SetItem{Name: f[0], Via: via}
			switch {
			case upgrade && len(f) == 4 && f[2] == "->":
				it.From, it.Version = f[1], f[3]
			case !upgrade && len(f) <= 2:
				if len(f) == 2 {
					it.Version = f[1]
				}
			default:
				return nil, fmt.Errorf("brew dry-run: cannot read the line %q", line)
			}
			out = append(out, it)
			n++
		}
	}
	if err := finish(); err != nil {
		return nil, err
	}
	return out, nil
}

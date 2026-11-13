package brewdata

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// SetItem is one package brew says it would install.
type SetItem struct {
	Name    string
	Version string // as brew prints it, a revision may be attached (2.3.3_1)
	Via     string // empty for a package that was asked for, else the package it is a dependency of
}

// InstallSet asks brew what installing the named packages would install:
// the packages themselves and every dependency that is not installed yet.
// It runs `brew install --dry-run`, which installs nothing. An empty result
// means brew had nothing to install (for example everything is installed).
func InstallSet(ctx context.Context, r Runner, names ...string) ([]SetItem, error) {
	if len(names) == 0 {
		return nil, errors.New("no package names given")
	}
	args := []string{"install", "--dry-run"}
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

// ParseDryRun reads the output of `brew install --dry-run`. It looks only at
// the "==> Would install N ...:" sections. A section whose line count does
// not match its header, or a line that is not "name [version]", is an error:
// a wrong guess here would hide a package from the policy.
func ParseDryRun(b []byte) ([]SetItem, error) {
	const prefix = "==> Would install "
	var (
		out      []SetItem
		via      string
		inside   bool
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
		case strings.HasPrefix(line, prefix):
			if err := finish(); err != nil {
				return nil, err
			}
			title := strings.TrimSuffix(strings.TrimPrefix(line, prefix), ":")
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
			if len(f) == 0 || len(f) > 2 || !ValidName(f[0]) {
				return nil, fmt.Errorf("brew dry-run: cannot read the line %q", line)
			}
			it := SetItem{Name: f[0], Via: via}
			if len(f) == 2 {
				it.Version = f[1]
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

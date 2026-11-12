// Package brewdata runs the real brew in a controlled way and parses the
// JSON it prints. brew is always started by absolute path with a cleaned
// environment, never through PATH.
package brewdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/gonzalogtesta/brew-guard/internal/dates"
)

// Runner runs brew with args and returns its standard output.
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@+._-]{0,127}$`)

// ValidName reports whether s is a safe formula or cask name to pass to brew.
// Taps and paths are rejected on purpose: only plain names are checked.
func ValidName(s string) bool { return nameRe.MatchString(s) }

// Package is what the policy needs to know about one formula or cask.
type Package struct {
	Name       string
	Kind       dates.Kind
	Tap        string
	Version    string
	Revision   int
	SourcePath string // path of the .rb file inside the tap repository
	URL        string
	Checksum   string
	Deprecated bool
	Disabled   bool
}

type infoJSON struct {
	Formulae []struct {
		Name       string `json:"name"`
		Tap        string `json:"tap"`
		Revision   int    `json:"revision"`
		SourcePath string `json:"ruby_source_path"`
		Deprecated bool   `json:"deprecated"`
		Disabled   bool   `json:"disabled"`
		Versions   struct {
			Stable string `json:"stable"`
		} `json:"versions"`
		URLs struct {
			Stable struct {
				URL      string `json:"url"`
				Checksum string `json:"checksum"`
			} `json:"stable"`
		} `json:"urls"`
	} `json:"formulae"`
	Casks []struct {
		Token      string `json:"token"`
		Tap        string `json:"tap"`
		Version    string `json:"version"`
		SHA256     string `json:"sha256"`
		URL        string `json:"url"`
		SourcePath string `json:"ruby_source_path"`
		Deprecated bool   `json:"deprecated"`
		Disabled   bool   `json:"disabled"`
	} `json:"casks"`
}

// ParseInfo parses the output of `brew info --json=v2`.
func ParseInfo(b []byte) ([]Package, error) {
	var in infoJSON
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, fmt.Errorf("brew info output: %w", err)
	}
	var out []Package
	for _, f := range in.Formulae {
		out = append(out, Package{Name: f.Name, Kind: dates.Formula, Tap: f.Tap, Version: f.Versions.Stable,
			Revision: f.Revision, SourcePath: f.SourcePath, URL: f.URLs.Stable.URL, Checksum: f.URLs.Stable.Checksum,
			Deprecated: f.Deprecated, Disabled: f.Disabled})
	}
	for _, c := range in.Casks {
		out = append(out, Package{Name: c.Token, Kind: dates.Cask, Tap: c.Tap, Version: c.Version,
			SourcePath: c.SourcePath, URL: c.URL, Checksum: c.SHA256, Deprecated: c.Deprecated, Disabled: c.Disabled})
	}
	return out, nil
}

// Info asks brew about the named formulae or casks.
func Info(ctx context.Context, r Runner, names ...string) ([]Package, error) {
	if len(names) == 0 {
		return nil, errors.New("no package names given")
	}
	args := []string{"info", "--json=v2"}
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
	pkgs, err := ParseInfo(b)
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("brew returned no data for %s", strings.Join(names, ", "))
	}
	return pkgs, nil
}

// Outdated is one installed package that has a newer version available.
type Outdated struct {
	Name      string
	Kind      dates.Kind
	Installed []string
	Current   string
}

type outdatedItem struct {
	Name      string          `json:"name"`
	Installed json.RawMessage `json:"installed_versions"`
	Current   string          `json:"current_version"`
}

// ParseOutdated parses `brew outdated --json=v2`. Cask installed_versions may
// be a string or a list, so both are accepted.
func ParseOutdated(b []byte) ([]Outdated, error) {
	var in struct {
		Formulae []outdatedItem `json:"formulae"`
		Casks    []outdatedItem `json:"casks"`
	}
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, fmt.Errorf("brew outdated output: %w", err)
	}
	var out []Outdated
	add := func(items []outdatedItem, k dates.Kind) error {
		for _, it := range items {
			inst, err := versions(it.Installed)
			if err != nil {
				return fmt.Errorf("%s: %w", it.Name, err)
			}
			out = append(out, Outdated{Name: it.Name, Kind: k, Installed: inst, Current: it.Current})
		}
		return nil
	}
	if err := add(in.Formulae, dates.Formula); err != nil {
		return nil, err
	}
	if err := add(in.Casks, dates.Cask); err != nil {
		return nil, err
	}
	return out, nil
}

func versions(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err != nil {
		return nil, errors.New("installed_versions is neither a string nor a list")
	}
	return []string{one}, nil
}

// ListOutdated asks brew which installed packages are outdated.
func ListOutdated(ctx context.Context, r Runner) ([]Outdated, error) {
	b, err := r.Run(ctx, "outdated", "--json=v2")
	if err != nil {
		return nil, err
	}
	return ParseOutdated(b)
}

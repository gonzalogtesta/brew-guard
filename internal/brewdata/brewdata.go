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
	"sort"
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

	// Signals that something runs or is not verified at install time.
	PostInstall   bool     // formula defines a post-install step
	HasService    bool     // formula defines a launchd/systemd service
	HasCaveats    bool     // brew prints caveats after install
	ArtifactKinds []string // cask artifact types, sorted (app, pkg, binary, ...)
	NoCheck       bool     // cask checksum is "no_check": the download is not verified

	Dependencies []string // formulae it needs at run time (direct ones, as brew lists them)
	Installed    bool     // some version is installed on this machine
}

type infoJSON struct {
	Formulae []struct {
		Name        string            `json:"name"`
		Tap         string            `json:"tap"`
		Revision    int               `json:"revision"`
		SourcePath  string            `json:"ruby_source_path"`
		Deprecated  bool              `json:"deprecated"`
		Disabled    bool              `json:"disabled"`
		PostInstall bool              `json:"post_install_defined"`
		Service     json.RawMessage   `json:"service"`
		Caveats     *string           `json:"caveats"`
		Deps        []string          `json:"dependencies"`
		Installed   []json.RawMessage `json:"installed"`
		Versions    struct {
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
		Token      string                       `json:"token"`
		Tap        string                       `json:"tap"`
		Version    string                       `json:"version"`
		SHA256     string                       `json:"sha256"`
		URL        string                       `json:"url"`
		SourcePath string                       `json:"ruby_source_path"`
		Deprecated bool                         `json:"deprecated"`
		Disabled   bool                         `json:"disabled"`
		Artifacts  []map[string]json.RawMessage `json:"artifacts"`
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
			Deprecated: f.Deprecated, Disabled: f.Disabled, PostInstall: f.PostInstall,
			HasService: hasValue(f.Service), HasCaveats: f.Caveats != nil && strings.TrimSpace(*f.Caveats) != "",
			Dependencies: f.Deps, Installed: len(f.Installed) > 0})
	}
	for _, c := range in.Casks {
		out = append(out, Package{Name: c.Token, Kind: dates.Cask, Tap: c.Tap, Version: c.Version,
			SourcePath: c.SourcePath, URL: c.URL, Checksum: c.SHA256, Deprecated: c.Deprecated, Disabled: c.Disabled,
			ArtifactKinds: artifactKinds(c.Artifacts), NoCheck: c.SHA256 == "no_check"})
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

func hasValue(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null" && s != "{}" && s != "[]"
}

// artifactKinds returns the sorted, unique artifact type names of a cask.
func artifactKinds(arts []map[string]json.RawMessage) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range arts {
		for k := range a {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Signals describes, in words, anything about the package that runs code or
// skips verification at install time. They are notes for the user; they do not
// change an allow or block decision.
func (p Package) Signals() []string {
	var out []string
	if p.PostInstall {
		out = append(out, "runs a post-install step")
	}
	if p.HasService {
		out = append(out, "installs a background service")
	}
	if p.NoCheck {
		out = append(out, "download checksum is not verified (no_check)")
	}
	for _, k := range p.ArtifactKinds {
		switch k {
		case "pkg", "installer":
			out = append(out, "cask runs an installer ("+k+"), which needs admin rights")
		case "preflight", "postflight", "preflight_steps", "postflight_steps":
			out = append(out, "cask runs install scripts ("+k+")")
		}
	}
	return out
}

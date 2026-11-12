// Package policy loads and validates the brew-guard policy file.
// TOML, JSON and a line-based text format are supported; the format is
// chosen by file extension and every format fails closed on unknown keys.
package policy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	MaxFileSize = 64 << 10
	ModeWarn    = "warn"
	ModeBlock   = "block"
	dateLayout  = "2006-01-02"
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@+._/-]{0,127}$`)

type Exception struct {
	Package string
	Reason  string
	Expires time.Time
}

type Policy struct {
	MinAgeDays     int
	Mode           string
	AllowedTaps    []string
	BlockTelemetry bool
	Exceptions     []Exception
}

// Default is the policy used when the user supplies none of a setting.
func Default() Policy {
	return Policy{
		MinAgeDays:     12,
		Mode:           ModeBlock,
		AllowedTaps:    []string{"homebrew/core", "homebrew/cask"},
		BlockTelemetry: true,
	}
}

// raw is the on-disk shape shared by the TOML and JSON formats.
type raw struct {
	MinAgeDays     *int           `toml:"min_age_days" json:"min_age_days"`
	Mode           *string        `toml:"mode" json:"mode"`
	AllowedTaps    []string       `toml:"allowed_taps" json:"allowed_taps"`
	BlockTelemetry *bool          `toml:"block_telemetry" json:"block_telemetry"`
	Exceptions     []rawException `toml:"exception" json:"exceptions"`
}

type rawException struct {
	Package string `toml:"package" json:"package"`
	Reason  string `toml:"reason" json:"reason"`
	Expires string `toml:"expires" json:"expires"`
}

// Find returns the single policy file in dir. More than one candidate is an
// error, so the active policy is never ambiguous.
func Find(dir string) (string, error) {
	var found []string
	for _, n := range []string{"policy.toml", "policy.json", "policy.txt"} {
		p := filepath.Join(dir, n)
		_, err := os.Stat(p)
		if err == nil {
			found = append(found, p)
		} else if !errors.Is(err, os.ErrNotExist) {
			// An unreadable candidate must not let a sibling file win silently.
			return "", err
		}
	}
	switch len(found) {
	case 0:
		return "", os.ErrNotExist
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("multiple policy files in %s: keep exactly one", dir)
}

// Load reads path, parses it by extension and validates the result.
func Load(path string) (Policy, error) {
	f, err := os.Open(path)
	if err != nil {
		return Policy{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return Policy{}, err
	}
	if len(data) > MaxFileSize {
		return Policy{}, fmt.Errorf("policy file larger than %d bytes", MaxFileSize)
	}
	return Parse(filepath.Ext(path), data)
}

// Parse parses data in the format named by ext (".toml", ".json", ".txt").
func Parse(ext string, data []byte) (Policy, error) {
	var (
		r   raw
		err error
	)
	switch strings.ToLower(ext) {
	case ".toml":
		r, err = parseTOML(data)
	case ".json":
		r, err = parseJSON(data)
	case ".txt":
		r, err = parseText(data)
	default:
		return Policy{}, fmt.Errorf("unsupported policy format %q (use .toml, .json or .txt)", ext)
	}
	if err != nil {
		return Policy{}, err
	}
	return build(r)
}

func build(r raw) (Policy, error) {
	p := Default()
	if r.MinAgeDays != nil {
		if *r.MinAgeDays < 0 || *r.MinAgeDays > 3650 {
			return Policy{}, errors.New("min_age_days must be between 0 and 3650")
		}
		p.MinAgeDays = *r.MinAgeDays
	}
	if r.Mode != nil {
		if *r.Mode != ModeWarn && *r.Mode != ModeBlock {
			return Policy{}, fmt.Errorf("mode must be %q or %q", ModeWarn, ModeBlock)
		}
		p.Mode = *r.Mode
	}
	if r.AllowedTaps != nil {
		for _, t := range r.AllowedTaps {
			if !nameRe.MatchString(t) {
				return Policy{}, fmt.Errorf("invalid tap name %q", t)
			}
		}
		p.AllowedTaps = r.AllowedTaps
	}
	if r.BlockTelemetry != nil {
		p.BlockTelemetry = *r.BlockTelemetry
	}
	for _, e := range r.Exceptions {
		if !nameRe.MatchString(e.Package) {
			return Policy{}, fmt.Errorf("invalid exception package %q", e.Package)
		}
		if strings.TrimSpace(e.Reason) == "" {
			return Policy{}, fmt.Errorf("exception %q needs a reason", e.Package)
		}
		exp, err := time.Parse(dateLayout, e.Expires)
		if err != nil {
			return Policy{}, fmt.Errorf("exception %q needs expires as YYYY-MM-DD", e.Package)
		}
		p.Exceptions = append(p.Exceptions, Exception{e.Package, e.Reason, exp})
	}
	return p, nil
}

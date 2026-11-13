package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gonzalogtesta/brew-guard/internal/audit"
	"github.com/gonzalogtesta/brew-guard/internal/brewdata"
	"github.com/gonzalogtesta/brew-guard/internal/dates"
	"github.com/gonzalogtesta/brew-guard/internal/decide"
	"github.com/gonzalogtesta/brew-guard/internal/doctor"
	"github.com/gonzalogtesta/brew-guard/internal/policy"
)

// Exit codes of check: 0 all allowed, 1 something blocked, 2 usage or
// operational error, 3 nothing blocked but a third-party tap needs review.
const (
	exitOK     = 0
	exitBlock  = 1
	exitError  = 2
	exitReview = 3
)

// Variables so tests can substitute the outside world.
var (
	brewCandidates = brewdata.Candidates
	analyticsCheck = doctor.Analytics
	newCheckDeps   = defaultCheckDeps
)

type checkDeps struct {
	Runner    brewdata.Runner
	History   dates.History
	Cache     *dates.Cache
	Now       func() time.Time
	AuditPath string
}

func defaultCheckDeps(env func(string) string) (checkDeps, error) {
	brew, err := brewdata.FindBrew(brewCandidates)
	if err != nil {
		return checkDeps{}, err
	}
	token := env("GITHUB_TOKEN")
	if token == "" {
		token = env("GH_TOKEN")
	}
	return checkDeps{
		Runner:    brewdata.ExecRunner{Brew: brew, Parent: os.Environ()},
		History:   dates.NewGitHub(token, "brew-guard/"+version),
		Cache:     dates.OpenCache(filepath.Join(xdgDir(env, "XDG_CACHE_HOME", ".cache"), "brew-guard", "dates.json")),
		Now:       time.Now,
		AuditPath: filepath.Join(xdgDir(env, "XDG_STATE_HOME", ".local/state"), "brew-guard", "audit.jsonl"),
	}, nil
}

func xdgDir(env func(string) string, xdg, fallback string) string {
	if d := env(xdg); d != "" {
		return d
	}
	return filepath.Join(env("HOME"), fallback)
}

func checkCmd(args []string, stdout, stderr io.Writer, env func(string) string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	offline := fs.Bool("offline", false, "no GitHub lookups, use cached release dates only")
	noDeps := fs.Bool("no-deps", false, "check only the named packages, not what brew would install with them")
	outdated := fs.Bool("outdated", false, "check everything brew reports as outdated")
	policyPath := fs.String("policy", "", "policy file (default: the one in the config directory)")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	names := fs.Args()
	if *outdated == (len(names) > 0) {
		fmt.Fprintln(stderr, "check: give package names, or --outdated, not both and not neither")
		return exitError
	}

	p, note, err := loadPolicy(*policyPath, env)
	if err != nil {
		fmt.Fprintln(stderr, "check:", err)
		return exitError
	}
	if note != "" {
		fmt.Fprintln(stdout, note)
	}

	deps, err := newCheckDeps(env)
	if err != nil {
		fmt.Fprintln(stderr, "check:", err)
		return exitError
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if *outdated {
		o, err := brewdata.ListOutdated(ctx, deps.Runner)
		if err != nil {
			fmt.Fprintln(stderr, "check:", err)
			return exitError
		}
		names = outdatedNames(o)
		if len(names) == 0 {
			fmt.Fprintln(stdout, "nothing is outdated")
			return exitOK
		}
	}
	via := map[string]string{}
	if !*outdated && !*noDeps {
		set, err := brewdata.InstallSet(ctx, deps.Runner, names...)
		if err != nil {
			fmt.Fprintln(stderr, "check:", err)
			return exitError
		}
		var extra []string
		names, extra = expandSet(names, set, via)
		switch {
		case len(set) == 0:
			fmt.Fprintf(stdout, "brew has nothing new to install for %s\n", strings.Join(names, ", "))
		case len(extra) > 0:
			fmt.Fprintf(stdout, "installing %s would also install %d more %s: %s\n",
				strings.Join(requested(set), ", "), len(extra), plural(len(extra)), strings.Join(extra, ", "))
		}
	}
	pkgs, err := brewdata.Info(ctx, deps.Runner, names...)
	if err != nil {
		fmt.Fprintln(stderr, "check:", err)
		return exitError
	}
	if *outdated && !*noDeps {
		extra, v, err := brewdata.ExpandDeps(ctx, deps.Runner, pkgs)
		if err != nil {
			fmt.Fprintln(stderr, "check:", err)
			return exitError
		}
		if len(extra) > 0 {
			var en []string
			for _, e := range extra {
				en = append(en, e.Name)
				via[e.Name] = v[e.Name]
			}
			fmt.Fprintf(stdout, "upgrading would also install %d new %s: %s\n", len(en), plural(len(en)), strings.Join(en, ", "))
			pkgs = append(pkgs, extra...)
		}
	}

	code := exitOK
	worse := func(c int) {
		if c == exitBlock || code == exitOK {
			code = c
		}
	}
	record := func(e audit.Entry) {
		e.Time, e.Command = deps.Now().UTC(), "check"
		if err := audit.Append(deps.AuditPath, e); err != nil {
			fmt.Fprintln(stderr, "warning: audit log not written:", err)
		}
	}

	if p.BlockTelemetry && env("HOMEBREW_NO_ANALYTICS") == "" {
		if c := analyticsCheck(); !c.OK {
			label, action := "WARN ", decide.Warn
			if p.Mode == policy.ModeBlock {
				label, action = "BLOCK", decide.Block
				worse(exitBlock)
			}
			fmt.Fprintf(stdout, "%s telemetry: Homebrew analytics are not disabled (%s)\n", label, c.Detail)
			record(audit.Entry{Package: "-", Action: string(action), Reasons: []string{"homebrew analytics enabled"}})
		}
	}

	for _, pkg := range pkgs {
		res, lerr := dates.Lookup(ctx, deps.History, deps.Cache, pkg.Kind, pkg.Tap, pkg.Name, pkg.Version, pkg.SourcePath, *offline)
		d := decide.Evaluate(p, decide.Subject{Name: pkg.Name, Tap: pkg.Tap, Version: pkg.Version,
			Published: res.Time, DateSource: res.Source}, deps.Now())
		reasons := d.Reasons
		if v := via[pkg.Name]; v != "" {
			reasons = append(reasons, "installed as a dependency of "+v)
		}
		if lerr != nil {
			reasons = append(reasons, "date lookup failed: "+lerr.Error())
		}
		if pkg.Disabled {
			reasons = append(reasons, "disabled upstream in Homebrew")
		} else if pkg.Deprecated {
			reasons = append(reasons, "deprecated upstream in Homebrew")
		}

		label := strings.ToUpper(string(d.Action))
		switch {
		case d.Action == decide.Block:
			worse(exitBlock)
		case d.NeedsReview:
			label = "REVIEW"
			worse(exitReview)
		}
		published := "unknown"
		if res.Known() {
			published = res.Time.Format("2006-01-02") + " (" + res.Source + ")"
		}
		fmt.Fprintf(stdout, "%-6s %s %s [%s] published %s\n", label, pkg.Name, pkg.Version, pkg.Tap, published)
		for _, r := range reasons {
			fmt.Fprintf(stdout, "         - %s\n", r)
		}
		for _, sig := range pkg.Signals() {
			fmt.Fprintf(stdout, "         * %s\n", sig)
		}
		if d.ExceptionUsed != "" {
			fmt.Fprintf(stdout, "         - exception: %s\n", d.ExceptionUsed)
		}
		record(audit.Entry{Package: pkg.Name, Tap: pkg.Tap, Version: pkg.Version, Action: string(d.Action), Reasons: reasons})
	}

	if deps.Cache != nil {
		if err := deps.Cache.Save(); err != nil {
			fmt.Fprintln(stderr, "warning: date cache not saved:", err)
		}
	}
	return code
}

// loadPolicy reads the policy file, or falls back to the secure defaults when
// there is none. A file that exists but is invalid is always an error.
func loadPolicy(path string, env func(string) string) (policy.Policy, string, error) {
	if path == "" {
		var err error
		path, err = policy.Find(configDir(env))
		if errors.Is(err, os.ErrNotExist) {
			return policy.Default(), "no policy file found, using the defaults", nil
		}
		if err != nil {
			return policy.Policy{}, "", err
		}
	}
	p, err := policy.Load(path)
	return p, "", err
}

// expandSet adds the dependencies brew would install to the requested names.
// It records in via which package each dependency belongs to, and returns the
// full list (requested first) and the names of the extra packages.
func expandSet(names []string, set []brewdata.SetItem, via map[string]string) (all, extra []string) {
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
		all = append(all, n)
	}
	for _, it := range set {
		if seen[it.Name] {
			continue
		}
		seen[it.Name] = true
		via[it.Name] = it.Via
		all = append(all, it.Name)
		extra = append(extra, it.Name)
	}
	return all, extra
}

// requested lists the packages of the set that were asked for, not pulled in.
func requested(set []brewdata.SetItem) []string {
	var out []string
	for _, it := range set {
		if it.Via == "" {
			out = append(out, it.Name)
		}
	}
	return out
}

func plural(n int) string {
	if n == 1 {
		return "package"
	}
	return "packages"
}

func outdatedNames(o []brewdata.Outdated) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range o {
		if !seen[x.Name] {
			seen[x.Name] = true
			out = append(out, x.Name)
		}
	}
	sort.Strings(out)
	return out
}

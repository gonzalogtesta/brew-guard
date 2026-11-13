package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/gonzalogtesta/brew-guard/internal/brewdata"
)

// Options that install, reinstall and upgrade may carry. Anything else is
// refused: some options (--HEAD, --no-quarantine, --appdir=...) change what
// gets installed or weaken a protection, and the policy cannot see that.
var wrapFlags = map[string]bool{
	"--cask": true, "--casks": true, "--formula": true, "--formulae": true,
	"--force": true, "-f": true, "--verbose": true, "-v": true, "--quiet": true, "-q": true,
	"--build-from-source": true, "-s": true, "--force-bottle": true, "--skip-cask-deps": true,
}

// Commands handed to brew as they are. They don't install software.
var passthrough = map[string]bool{
	"--version": true, "-v": true, "--prefix": true, "--cellar": true, "--cache": true, "--repository": true,
	"help": true, "config": true, "doctor": true, "info": true, "list": true, "ls": true, "outdated": true,
	"deps": true, "uses": true, "search": true, "desc": true, "leaves": true, "missing": true, "home": true,
	"shellenv": true, "commands": true, "update": true, "cleanup": true, "autoremove": true,
	"uninstall": true, "remove": true, "rm": true, "unlink": true, "link": true, "pin": true, "unpin": true,
	"services": true, "analytics": true,
}

// wrapCmd is what runs when brew-guard is called as brew. install, reinstall
// and upgrade go through the policy first; read-only commands go straight to
// the real brew; everything else is refused, because it could install
// software without being checked.
func wrapCmd(args []string, stdout, stderr io.Writer, env func(string) string) int {
	if len(args) == 0 {
		return passCmd(nil, stderr, env)
	}
	switch sub := args[0]; {
	case sub == "install" || sub == "reinstall" || sub == "upgrade":
		return guardedCmd(sub, args[1:], stdout, stderr, env)
	case passthrough[sub]:
		return passCmd(args, stderr, env)
	default:
		fmt.Fprintf(stderr, "brew-guard: `brew %s` is not passed through, it could install software that was not checked.\n", sub)
		fmt.Fprintln(stderr, "Use the real brew directly if you mean it. Passed through: install, reinstall and upgrade after a check, and read-only commands such as info, list, outdated, search, update.")
		return exitError
	}
}

func passCmd(args []string, stderr io.Writer, env func(string) string) int {
	deps, err := newCheckDeps(env)
	if err != nil {
		fmt.Fprintln(stderr, "brew-guard:", err)
		return exitError
	}
	return execBrew(deps, args, stderr)
}

// execBrew hands over to the real brew. A real exec never returns, so
// getting here with an error means brew did not start.
func execBrew(deps checkDeps, args []string, stderr io.Writer) int {
	err := deps.Exec(args)
	if err == nil {
		return exitOK
	}
	fmt.Fprintln(stderr, "brew-guard: could not run brew:", err)
	return exitError
}

func guardedCmd(kind string, args []string, stdout, stderr io.Writer, env func(string) string) int {
	flags, names, err := splitArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, "brew-guard:", err)
		return exitError
	}
	if len(names) == 0 && kind != "upgrade" {
		fmt.Fprintf(stderr, "brew-guard: brew %s needs at least one package name\n", kind)
		return exitError
	}
	p, note, err := loadPolicy("", env)
	if err != nil {
		fmt.Fprintln(stderr, "brew-guard:", err)
		return exitError
	}
	if note != "" {
		fmt.Fprintln(stdout, note)
	}
	deps, err := newCheckDeps(env)
	if err != nil {
		fmt.Fprintln(stderr, "brew-guard:", err)
		return exitError
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if kind == "upgrade" && len(names) == 0 {
		o, err := brewdata.ListOutdated(ctx, deps.Runner)
		if err != nil {
			fmt.Fprintln(stderr, "brew-guard:", err)
			return exitError
		}
		if names = outdatedNames(o); len(names) == 0 {
			fmt.Fprintln(stdout, "nothing is outdated")
			return exitOK
		}
	}
	pkgs, via, err := resolveKind(ctx, stdout, deps, kind, flags, names)
	if err != nil {
		fmt.Fprintln(stderr, "brew-guard:", err)
		return exitError
	}
	command := kind
	if code := evaluate(ctx, stdout, stderr, p, deps, env, pkgs, via, false, command); code != exitOK {
		fmt.Fprintf(stderr, "brew-guard: not running brew %s, the check did not pass (exit %d)\n", kind, code)
		return code
	}
	// Resolve again right before running brew: if the set is not the one that
	// was just checked, nothing runs.
	again, _, err := resolveKind(ctx, io.Discard, deps, kind, flags, names)
	if err != nil {
		fmt.Fprintln(stderr, "brew-guard:", err)
		return exitError
	}
	if !sameSet(pkgs, again) {
		fmt.Fprintf(stderr, "brew-guard: what brew %s would install changed since the check, run the command again\n", kind)
		return exitBlock
	}
	return execBrew(deps, append(append([]string{kind}, flags...), names...), stderr)
}

func resolveKind(ctx context.Context, stdout io.Writer, deps checkDeps, kind string, flags, names []string) ([]brewdata.Package, map[string]string, error) {
	if kind == "install" {
		return resolveInstall(ctx, stdout, deps.Runner, flags, names, true)
	}
	return resolveUpgrade(ctx, stdout, deps.Runner, names, true)
}

// splitArgs separates options from package names and refuses any option that
// is not on the list.
func splitArgs(args []string) (flags, names []string, err error) {
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "-"):
			if !wrapFlags[a] {
				return nil, nil, fmt.Errorf("option %s is not allowed, brew-guard cannot check what it changes", a)
			}
			flags = append(flags, a)
		case a == "":
			return nil, nil, errors.New("empty package name")
		default:
			names = append(names, a)
		}
	}
	return flags, names, nil
}

func sameSet(a, b []brewdata.Package) bool {
	key := func(p []brewdata.Package) []string {
		var out []string
		for _, x := range p {
			out = append(out, x.Name+"@"+x.Version)
		}
		sort.Strings(out)
		return out
	}
	ka, kb := key(a), key(b)
	if len(ka) != len(kb) {
		return false
	}
	for i := range ka {
		if ka[i] != kb[i] {
			return false
		}
	}
	return true
}

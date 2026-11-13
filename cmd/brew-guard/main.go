package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/gonzalogtesta/brew-guard/internal/doctor"
	"github.com/gonzalogtesta/brew-guard/internal/policy"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "0.0.1-dev"

// doctorRun is a variable so tests can substitute fixed checks.
var doctorRun = doctor.Run

func main() {
	args := os.Args[1:]
	// Called through a link named brew: act as the brew wrapper.
	if filepath.Base(os.Args[0]) == "brew" {
		args = append([]string{"brew"}, args...)
	}
	os.Exit(run(args, os.Stdout, os.Stderr, os.Getenv))
}

// run executes the command in args (program name excluded) and returns the
// process exit code.
func run(args []string, stdout, stderr io.Writer, env func(string) string) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, "brew-guard", version)
		return 0
	case "policy":
		return policyCmd(args[1:], stdout, stderr, env)
	case "doctor":
		return doctorCmd(stdout, env)
	case "harden":
		return hardenCmd(stdout, env)
	case "check":
		return checkCmd(args[1:], stdout, stderr, env)
	case "brew":
		return wrapCmd(args[1:], stdout, stderr, env)
	case "shim":
		return shimCmd(args[1:], stdout, stderr, env)
	default:
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: brew-guard version | policy validate [file] | doctor | harden | check [--offline] [--no-deps] [--policy file] (--outdated | name...) | brew <brew arguments> | shim install|uninstall|status")
}

func configDir(env func(string) string) string {
	if d := env("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "brew-guard")
	}
	return filepath.Join(env("HOME"), ".config", "brew-guard")
}

func policyCmd(args []string, stdout, stderr io.Writer, env func(string) string) int {
	if len(args) == 0 || args[0] != "validate" {
		usage(stderr)
		return 2
	}
	var path string
	if len(args) > 1 {
		path = args[1]
	} else {
		var err error
		if path, err = policy.Find(configDir(env)); err != nil {
			fmt.Fprintln(stderr, "policy:", err)
			return 1
		}
	}
	p, err := policy.Load(path)
	if err != nil {
		fmt.Fprintln(stderr, "invalid policy:", err)
		return 1
	}
	fmt.Fprintf(stdout, "ok: %s (min_age_days=%d mode=%s taps=%d exceptions=%d)\n",
		path, p.MinAgeDays, p.Mode, len(p.AllowedTaps), len(p.Exceptions))
	return 0
}

func doctorCmd(stdout io.Writer, env func(string) string) int {
	bad := 0
	for _, c := range doctorRun(env, os.Geteuid()) {
		mark := "ok  "
		if !c.OK {
			mark, bad = "FAIL", bad+1
		}
		fmt.Fprintf(stdout, "%s %s", mark, c.Name)
		if c.Detail != "" {
			fmt.Fprintf(stdout, "  (%s)", c.Detail)
		}
		fmt.Fprintln(stdout)
	}
	if bad > 0 {
		return 1
	}
	return 0
}

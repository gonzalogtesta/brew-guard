// Package doctor reports whether Homebrew is configured privately and safely.
// It only reads state; it never changes Homebrew or the environment.
package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Check struct {
	Name   string
	OK     bool
	Detail string
}

// Setting is an environment variable brew-guard recommends exporting.
type Setting struct {
	Name  string
	Value string
	Why   string
}

// Recommended lists the privacy and security settings checked by doctor and
// printed by harden.
var Recommended = []Setting{
	{"HOMEBREW_NO_ANALYTICS", "1", "turns off Homebrew usage analytics"},
	{"HOMEBREW_NO_AUTO_UPDATE", "1", "no surprise updates before every command"},
	{"HOMEBREW_NO_INSECURE_REDIRECT", "1", "refuse downloads redirected from https to http"},
}

// Package variables so tests can point the checks at temporary repositories.
var (
	brewRepos = []string{"/opt/homebrew", "/usr/local/Homebrew"}
	gitBin    = "/usr/bin/git"

	// Homebrew reads HOMEBREW_* settings from these files on every run, besides
	// the user's own (see brewEnvFiles).
	systemEnvFiles = []string{"/etc/homebrew/brew.env", "/usr/local/etc/homebrew/brew.env", "/opt/homebrew/etc/homebrew/brew.env"}
)

// brewEnvFiles lists Homebrew's settings files that apply to this user.
func brewEnvFiles(env func(string) string) []string {
	user := filepath.Join(env("HOME"), ".homebrew", "brew.env")
	if x := env("XDG_CONFIG_HOME"); x != "" {
		user = filepath.Join(x, "homebrew", "brew.env")
	}
	return append([]string{user}, systemEnvFiles...)
}

// inBrewEnv reports whether one of Homebrew's settings files sets name to a
// value. Lines are NAME=value; Homebrew does no expansion and runs no commands.
func inBrewEnv(env func(string) string, name string) bool {
	for _, f := range brewEnvFiles(env) {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
			if ok && strings.TrimSpace(k) == name && strings.Trim(strings.TrimSpace(v), `"'`) != "" {
				return true
			}
		}
	}
	return false
}

// Run performs every check. env is os.Getenv or a test double.
func Run(env func(string) string, euid int) []Check {
	var out []Check
	out = append(out, Check{"not running as root", euid != 0, ""})
	for _, s := range Recommended {
		ok := env(s.Name) != "" || inBrewEnv(env, s.Name)
		d := ""
		if !ok {
			d = "export " + s.Name + "=" + s.Value
		}
		out = append(out, Check{s.Name + " set", ok, d})
	}
	out = append(out, analytics())
	// Homebrew refuses to load untrusted third-party taps unless this is set.
	// Setting it switches that protection off, so it is reported as a failure.
	out = append(out, Check{"tap trust is not disabled", env("HOMEBREW_NO_REQUIRE_TAP_TRUST") == "",
		tapTrustDetail(env("HOMEBREW_NO_REQUIRE_TAP_TRUST") == "")})
	return out
}

// Analytics reports whether Homebrew analytics are switched off.
func Analytics() Check { return analytics() }

// analytics reads Homebrew's own opt-out flag from its git config, without
// running brew, so it still works when brew itself is broken.
func analytics() Check {
	for _, repo := range brewRepos {
		if _, err := os.Stat(repo); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, gitBin, "-C", repo, "config", "--get", "homebrew.analyticsdisabled")
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + os.Getenv("HOME")}
		b, _ := cmd.Output()
		if strings.TrimSpace(string(b)) == "true" {
			return Check{"analytics disabled in " + repo, true, ""}
		}
		return Check{"analytics disabled in " + repo, false, "run: brew analytics off"}
	}
	return Check{"Homebrew repository found", false, fmt.Sprintf("looked in %v", brewRepos)}
}

func tapTrustDetail(ok bool) string {
	if ok {
		return ""
	}
	return "unset HOMEBREW_NO_REQUIRE_TAP_TRUST"
}

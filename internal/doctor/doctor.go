// Package doctor reports whether Homebrew is configured privately and safely.
// It only reads state; it never changes Homebrew or the environment.
package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Check struct {
	Name   string
	OK     bool
	Detail string
}

// Package variables so tests can point the checks at temporary repositories.
var (
	brewRepos = []string{"/opt/homebrew", "/usr/local/Homebrew"}
	gitBin    = "/usr/bin/git"
)

// Run performs every check. env is os.Getenv or a test double.
func Run(env func(string) string, euid int) []Check {
	var out []Check
	out = append(out, Check{"not running as root", euid != 0, ""})
	for _, v := range []string{"HOMEBREW_NO_ANALYTICS", "HOMEBREW_NO_AUTO_UPDATE", "HOMEBREW_NO_INSECURE_REDIRECT"} {
		ok := env(v) != ""
		d := ""
		if !ok {
			d = "export " + v + "=1"
		}
		out = append(out, Check{v + " set", ok, d})
	}
	out = append(out, analytics())
	return out
}

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

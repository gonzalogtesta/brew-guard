package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func execLine(h *harness) string {
	if len(h.execs) != 1 {
		return "none"
	}
	return strings.Join(h.execs[0], " ")
}

func TestWrapInstallRunsBrewWhenAllowed(t *testing.T) {
	h := setup(t, 30, "")
	code, out, errs := runCmd(h.env, "brew", "install", "jq")
	if code != 0 || errs != "" || execLine(h) != "install jq" {
		t.Fatalf("code=%d err=%q exec=%s out=%s", code, errs, execLine(h), out)
	}
	if !strings.Contains(h.audit(t), `"command":"install"`) {
		t.Fatalf("audit: %s", h.audit(t))
	}
}

func TestWrapInstallPassesAllowedFlags(t *testing.T) {
	h := setup(t, 30, "")
	h.runner.out["install --dry-run --force jq"] = "==> Would install 1 formula:\njq 1.8.2\n"
	if code, _, _ := runCmd(h.env, "brew", "install", "--force", "jq"); code != 0 || execLine(h) != "install --force jq" {
		t.Fatalf("code=%d exec=%s", code, execLine(h))
	}
}

func TestWrapInstallBlocksYoungPackage(t *testing.T) {
	h := setup(t, 3, "")
	code, out, errs := runCmd(h.env, "brew", "install", "jq")
	if code != 1 || execLine(h) != "none" {
		t.Fatalf("code=%d exec=%s out=%s", code, execLine(h), out)
	}
	if !strings.Contains(errs, "not running brew install") || !strings.Contains(out, "BLOCK") {
		t.Fatalf("err=%q out=%q", errs, out)
	}
}

func TestWrapInstallBlocksYoungDependency(t *testing.T) {
	h := setup(t, 30, "")
	withDependency(h, 2)
	if code, _, _ := runCmd(h.env, "brew", "install", "jq"); code != 1 || execLine(h) != "none" {
		t.Fatalf("code=%d exec=%s", code, execLine(h))
	}
}

func TestWrapRefusesOptions(t *testing.T) {
	for _, flag := range []string{"--HEAD", "--no-quarantine", "--appdir=/tmp", "--ignore-dependencies"} {
		h := setup(t, 30, "")
		code, _, errs := runCmd(h.env, "brew", "install", flag, "jq")
		if code != 2 || execLine(h) != "none" || !strings.Contains(errs, "not allowed") {
			t.Errorf("%s: code=%d exec=%s err=%s", flag, code, execLine(h), errs)
		}
		if len(h.runner.calls) != 0 {
			t.Errorf("%s: brew must not be asked anything: %v", flag, h.runner.calls)
		}
	}
}

func TestWrapRefusesPathsAndUrls(t *testing.T) {
	for _, name := range []string{"./evil.rb", "user/tap/evil", "https://x.example/a.rb"} {
		h := setup(t, 30, "")
		if code, _, _ := runCmd(h.env, "brew", "install", name); code != 2 || execLine(h) != "none" {
			t.Errorf("%s: code=%d exec=%s", name, code, execLine(h))
		}
	}
}

func TestWrapInstallNeedsAName(t *testing.T) {
	h := setup(t, 30, "")
	if code, _, _ := runCmd(h.env, "brew", "install"); code != 2 || execLine(h) != "none" {
		t.Fatalf("code=%d exec=%s", code, execLine(h))
	}
}

// changingRunner answers the dry-run differently the second time.
type changingRunner struct {
	*fakeRunner
	dry int
}

func (c *changingRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) > 1 && args[1] == "--dry-run" {
		c.dry++
		if c.dry == 2 {
			return []byte("==> Would install 1 formula:\njq 1.8.2\n==> Would install 1 dependency for jq:\noniguruma 6.9.10\n"), nil
		}
	}
	return c.fakeRunner.Run(ctx, args...)
}

func TestWrapStopsWhenTheSetChanges(t *testing.T) {
	h := setup(t, 30, "")
	withDependency(h, 30)
	h.runner.out["install --dry-run jq"] = "==> Would install 1 formula:\njq 1.8.2\n"
	old := newCheckDeps
	d, _ := old(func(string) string { return "" })
	d.Runner = &changingRunner{fakeRunner: h.runner}
	newCheckDeps = func(func(string) string) (checkDeps, error) { return d, nil }
	code, _, errs := runCmd(h.env, "brew", "install", "jq")
	if code != 1 || execLine(h) != "none" || !strings.Contains(errs, "changed since the check") {
		t.Fatalf("code=%d exec=%s err=%s", code, execLine(h), errs)
	}
}

func TestWrapPassthroughCommands(t *testing.T) {
	for _, c := range []string{"info", "list", "outdated", "update", "search"} {
		h := setup(t, 30, "")
		if code, _, _ := runCmd(h.env, "brew", c, "x"); code != 0 || execLine(h) != c+" x" {
			t.Errorf("%s: code=%d exec=%s", c, code, execLine(h))
		}
		if len(h.runner.calls) != 0 {
			t.Errorf("%s: passthrough must not query brew first", c)
		}
	}
	h := setup(t, 30, "")
	if code, _, _ := runCmd(h.env, "brew"); code != 0 || len(h.execs) != 1 {
		t.Fatalf("plain brew: code=%d execs=%v", code, h.execs)
	}
}

func TestWrapRefusesCommandsThatInstall(t *testing.T) {
	for _, c := range []string{"bundle", "tap", "cask", "install-bundler-gems", "developer"} {
		h := setup(t, 30, "")
		code, _, errs := runCmd(h.env, "brew", c, "x")
		if code != 2 || execLine(h) != "none" || !strings.Contains(errs, "not passed through") {
			t.Errorf("%s: code=%d exec=%s err=%s", c, code, execLine(h), errs)
		}
	}
}

func TestWrapUpgradeAllChecksOutdated(t *testing.T) {
	h := setup(t, 30, "")
	code, out, _ := runCmd(h.env, "brew", "upgrade")
	if code != 0 || execLine(h) != "upgrade jq" {
		t.Fatalf("code=%d exec=%s out=%s", code, execLine(h), out)
	}
}

func TestWrapUpgradeBlocksYoung(t *testing.T) {
	h := setup(t, 3, "")
	if code, _, _ := runCmd(h.env, "brew", "upgrade"); code != 1 || execLine(h) != "none" {
		t.Fatalf("code=%d exec=%s", code, execLine(h))
	}
}

func TestWrapUpgradeNothingOutdated(t *testing.T) {
	h := setup(t, 30, "")
	h.runner.out["outdated --json=v2"] = `{"formulae":[],"casks":[]}`
	code, out, _ := runCmd(h.env, "brew", "upgrade")
	if code != 0 || execLine(h) != "none" || !strings.Contains(out, "nothing is outdated") {
		t.Fatalf("code=%d exec=%s out=%s", code, execLine(h), out)
	}
}

func TestWrapReinstallIsChecked(t *testing.T) {
	h := setup(t, 3, "")
	if code, _, _ := runCmd(h.env, "brew", "reinstall", "jq"); code != 1 || execLine(h) != "none" {
		t.Fatalf("code=%d exec=%s", code, execLine(h))
	}
	h2 := setup(t, 30, "")
	if code, _, _ := runCmd(h2.env, "brew", "reinstall", "jq"); code != 0 || execLine(h2) != "reinstall jq" {
		t.Fatalf("code=%d exec=%s", code, execLine(h2))
	}
}

func TestWrapExecFailure(t *testing.T) {
	h := setup(t, 30, "")
	old := newCheckDeps
	d, _ := old(func(string) string { return "" })
	d.Exec = func([]string) error { return errors.New("no such file") }
	newCheckDeps = func(func(string) string) (checkDeps, error) { return d, nil }
	if code, _, errs := runCmd(h.env, "brew", "info"); code != 2 || !strings.Contains(errs, "could not run brew") {
		t.Fatalf("code=%d err=%s", code, errs)
	}
}

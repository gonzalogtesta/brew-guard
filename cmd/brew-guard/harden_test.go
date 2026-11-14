package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gonzalogtesta/brew-guard/internal/doctor"
	"github.com/gonzalogtesta/brew-guard/internal/harden"
)

func TestHardenPrintsEverySetting(t *testing.T) {
	code, out, errs := runCmd(nil, "harden")
	if code != 0 || errs != "" {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	for _, s := range doctor.Recommended {
		if !strings.Contains(out, "export "+s.Name+"="+s.Value+"\n") {
			t.Errorf("missing export for %s:\n%s", s.Name, out)
		}
	}
	if strings.Contains(out, "already set") {
		t.Errorf("nothing is set, but output says so:\n%s", out)
	}
	if !strings.Contains(out, "brew analytics off") {
		t.Error("the analytics command should be mentioned")
	}
	if !strings.Contains(out, "harden --apply") {
		t.Error("the apply command should be mentioned")
	}
}

func TestHardenMarksSettingsAlreadyPresent(t *testing.T) {
	_, out, _ := runCmd(map[string]string{"HOMEBREW_NO_ANALYTICS": "1"}, "harden")
	if !strings.Contains(out, "export HOMEBREW_NO_ANALYTICS=1  # already set") {
		t.Fatalf("out=%s", out)
	}
	if strings.Contains(out, "HOMEBREW_NO_AUTO_UPDATE=1  # already set") {
		t.Fatal("only the variable that is set may be marked")
	}
}

func TestHardenOnlyPrints(t *testing.T) {
	// It must not touch brew or the filesystem: no deps are created.
	old := newCheckDeps
	newCheckDeps = func(func(string) string) (checkDeps, error) {
		t.Fatal("harden must not build check deps")
		return checkDeps{}, nil
	}
	defer func() { newCheckDeps = old }()
	runCmd(nil, "harden")
}

type hardenEnv struct {
	env     map[string]string
	profile string
	repo    string
}

func hardenSetup(t *testing.T) hardenEnv {
	t.Helper()
	if _, err := os.Stat(harden.Git); err != nil {
		t.Skip("git not available")
	}
	d := t.TempDir()
	repo := filepath.Join(d, "brew")
	writeFile(t, filepath.Join(repo, ".git", "config"), "[core]\n\tbare = false\n")
	old := hardenRepos
	hardenRepos = []string{repo}
	t.Cleanup(func() { hardenRepos = old })
	return hardenEnv{
		env:     map[string]string{"HOME": d, "SHELL": "/bin/zsh", "XDG_STATE_HOME": filepath.Join(d, "state")},
		profile: filepath.Join(d, ".zprofile"),
		repo:    repo,
	}
}

func TestHardenApplyAndUndo(t *testing.T) {
	h := hardenSetup(t)
	writeFile(t, h.profile, "export PATH=/x\n")

	code, out, errs := runCmd(h.env, "harden", "--apply", "--yes")
	if code != 0 || errs != "" {
		t.Fatalf("code=%d out=%s err=%s", code, out, errs)
	}
	b, _ := os.ReadFile(h.profile)
	if !strings.Contains(string(b), "export HOMEBREW_NO_ANALYTICS=1") {
		t.Fatalf("profile=%s", b)
	}
	if !strings.Contains(out, "backup of the profile") || !strings.Contains(out, "harden --undo") {
		t.Errorf("out=%s", out)
	}
	cfg, _ := os.ReadFile(filepath.Join(h.repo, ".git", "config"))
	if !strings.Contains(string(cfg), "analyticsdisabled = true") {
		t.Errorf("git config=%s", cfg)
	}

	code, out, _ = runCmd(h.env, "harden", "--apply", "--yes")
	if code != 0 || !strings.Contains(out, "nothing to change") {
		t.Errorf("second apply: code=%d out=%s", code, out)
	}

	code, out, errs = runCmd(h.env, "harden", "--undo", "--yes")
	if code != 0 || errs != "" {
		t.Fatalf("undo: code=%d out=%s err=%s", code, out, errs)
	}
	b, _ = os.ReadFile(h.profile)
	if string(b) != "export PATH=/x\n" {
		t.Errorf("profile after undo=%q", b)
	}
	cfg, _ = os.ReadFile(filepath.Join(h.repo, ".git", "config"))
	if strings.Contains(string(cfg), "analyticsdisabled") {
		t.Errorf("git config after undo=%s", cfg)
	}

	code, out, _ = runCmd(h.env, "harden", "--undo", "--yes")
	if code != 0 || !strings.Contains(out, "nothing to undo") {
		t.Errorf("second undo: code=%d out=%s", code, out)
	}
}

func TestHardenApplyAsksAndCanBeDeclined(t *testing.T) {
	h := hardenSetup(t)
	old := repairStdin
	repairStdin = strings.NewReader("no\n")
	defer func() { repairStdin = old }()

	code, out, _ := runCmd(h.env, "harden", "--apply")
	if code != exitBlock || !strings.Contains(out, "cancelled") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if _, err := os.Stat(h.profile); !os.IsNotExist(err) {
		t.Error("the profile must not be created when declined")
	}
	if !strings.Contains(out, "will export HOMEBREW_NO_ANALYTICS=1") {
		t.Errorf("the plan should be shown before asking:\n%s", out)
	}
}

func TestHardenApplyUnknownShellNeedsAProfile(t *testing.T) {
	h := hardenSetup(t)
	h.env["SHELL"] = "/usr/bin/fish"
	code, _, errs := runCmd(h.env, "harden", "--apply", "--yes")
	if code != exitError || !strings.Contains(errs, "--profile") {
		t.Fatalf("code=%d err=%s", code, errs)
	}
	code, _, _ = runCmd(h.env, "harden", "--apply", "--yes", "--profile", h.profile)
	if code != 0 {
		t.Fatalf("with --profile: code=%d", code)
	}
}

func TestHardenFlagErrors(t *testing.T) {
	for _, args := range [][]string{
		{"harden", "--apply", "--undo"},
		{"harden", "extra"},
		{"harden", "--nope"},
	} {
		if code, _, _ := runCmd(nil, args...); code != exitError {
			t.Errorf("%v: code=%d", args, code)
		}
	}
}

func TestHardenRefusesADamagedBlock(t *testing.T) {
	h := hardenSetup(t)
	writeFile(t, h.profile, "# >>> brew-guard harden >>>\nexport A=1\n")
	code, _, errs := runCmd(h.env, "harden", "--apply", "--yes")
	if code != exitError || !strings.Contains(errs, "not closed") {
		t.Fatalf("code=%d err=%s", code, errs)
	}
}

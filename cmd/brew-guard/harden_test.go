package main

import (
	"strings"
	"testing"

	"github.com/gonzalogtesta/brew-guard/internal/doctor"
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

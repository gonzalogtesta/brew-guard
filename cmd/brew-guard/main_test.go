package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gonzalogtesta/brew-guard/internal/doctor"
)

func runCmd(env map[string]string, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, func(k string) string { return env[k] })
	return code, out.String(), errb.String()
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestVersion(t *testing.T) {
	code, out, errs := runCmd(nil, "version")
	if code != 0 || out != "brew-guard 0.0.1-dev\n" || errs != "" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
}

func TestUsageAndUnknownCommands(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"nope"},
		{"policy"},
		{"policy", "check"},
	} {
		code, out, errs := runCmd(nil, args...)
		if code != 2 || out != "" || !strings.HasPrefix(errs, "usage:") {
			t.Errorf("args %q: code=%d out=%q err=%q", args, code, out, errs)
		}
	}
}

func TestPolicyValidateFile(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "policy.toml")
	writeFile(t, valid, "min_age_days = 20\nmode = \"warn\"\n")
	bad := filepath.Join(dir, "bad.toml")
	writeFile(t, bad, "min_age_dayz = 3\n")

	code, out, errs := runCmd(nil, "policy", "validate", valid)
	if code != 0 || errs != "" {
		t.Fatalf("valid: code=%d err=%q", code, errs)
	}
	want := "ok: " + valid + " (min_age_days=20 mode=warn taps=2 exceptions=0)\n"
	if out != want {
		t.Fatalf("valid output %q want %q", out, want)
	}

	code, out, errs = runCmd(nil, "policy", "validate", bad)
	if code != 1 || out != "" || !strings.HasPrefix(errs, "invalid policy:") {
		t.Fatalf("invalid: code=%d out=%q err=%q", code, out, errs)
	}

	code, _, errs = runCmd(nil, "policy", "validate", filepath.Join(dir, "missing.toml"))
	if code != 1 || !strings.HasPrefix(errs, "invalid policy:") {
		t.Fatalf("missing: code=%d err=%q", code, errs)
	}
}

func TestPolicyValidateConfigDir(t *testing.T) {
	xdg := t.TempDir()
	env := map[string]string{"XDG_CONFIG_HOME": xdg, "HOME": t.TempDir()}

	code, _, errs := runCmd(env, "policy", "validate")
	if code != 1 || !strings.Contains(errs, "policy:") {
		t.Fatalf("no file: code=%d err=%q", code, errs)
	}

	cfg := filepath.Join(xdg, "brew-guard")
	writeFile(t, filepath.Join(cfg, "policy.toml"), "mode = \"block\"\n")
	writeFile(t, filepath.Join(cfg, "policy.txt"), "mode warn\n")
	code, _, errs = runCmd(env, "policy", "validate")
	if code != 1 || !strings.Contains(errs, "multiple policy files") {
		t.Fatalf("ambiguous: code=%d err=%q", code, errs)
	}

	if err := os.Remove(filepath.Join(cfg, "policy.txt")); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runCmd(env, "policy", "validate")
	if code != 0 || errs != "" || !strings.Contains(out, filepath.Join(cfg, "policy.toml")) {
		t.Fatalf("single: code=%d out=%q err=%q", code, out, errs)
	}
}

func TestPolicyValidateHomeFallback(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".config", "brew-guard", "policy.json"), `{"mode":"warn"}`)
	code, out, errs := runCmd(map[string]string{"HOME": home}, "policy", "validate")
	if code != 0 || errs != "" || !strings.Contains(out, "mode=warn") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
}

func TestDoctorExitCode(t *testing.T) {
	old := doctorRun
	t.Cleanup(func() { doctorRun = old })

	doctorRun = func(func(string) string, int) []doctor.Check {
		return []doctor.Check{{Name: "alpha", OK: true}}
	}
	code, out, _ := runCmd(nil, "doctor")
	if code != 0 || out != "ok   alpha\n" {
		t.Fatalf("all ok: code=%d out=%q", code, out)
	}

	doctorRun = func(func(string) string, int) []doctor.Check {
		return []doctor.Check{
			{Name: "alpha", OK: true},
			{Name: "beta", OK: false, Detail: "export X=1"},
		}
	}
	code, out, _ = runCmd(nil, "doctor")
	if code != 1 || out != "ok   alpha\nFAIL beta  (export X=1)\n" {
		t.Fatalf("one fail: code=%d out=%q", code, out)
	}
}

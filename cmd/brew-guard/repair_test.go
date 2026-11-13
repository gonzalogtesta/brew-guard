package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gonzalogtesta/brew-guard/internal/repair"
)

const headSHA = "0123456789abcdef0123456789abcdef01234567"

type repairFake struct {
	calls   []string
	answers map[string]string
	fail    map[string]error
}

func (f *repairFake) exec(_ context.Context, name string, args ...string) ([]byte, error) {
	key := filepath.Base(name)
	if key == "git" {
		i := 14 // the subcommand follows -C dir and six -c pairs
		if len(args) > i && args[0] == "-C" {
			key = "git " + args[i]
			if args[i] == "ls-files" {
				key += " " + args[i+1]
			}
		} else {
			key = "git " + args[0]
		}
	}
	f.calls = append(f.calls, key)
	if err := f.fail[key]; err != nil {
		return nil, err
	}
	return []byte(f.answers[key]), nil
}

func (f *repairFake) ran(key string) bool {
	for _, c := range f.calls {
		if c == key {
			return true
		}
	}
	return false
}

// repairSetup makes a fake checkout and installs fakes for git, the prompt and brew.
func repairSetup(t *testing.T, config string, answers map[string]string) (*repairFake, map[string]string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Homebrew")
	writeFile(t, filepath.Join(dir, ".git", "config"), config)
	f := &repairFake{answers: map[string]string{"git rev-parse": headSHA + "\n", "git config": repair.OfficialURL + "\n"}, fail: map[string]error{}}
	for k, v := range answers {
		f.answers[k] = v
	}
	oldExec, oldNow, oldVerify, oldIn := repairExec, repairNow, verifyBrew, repairStdin
	repairExec = f.exec
	repairNow = func() time.Time { return time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC) }
	verifyBrew = func(context.Context) (string, error) { return "Homebrew 7.0.13\nsecond line", nil }
	t.Cleanup(func() { repairExec, repairNow, verifyBrew, repairStdin = oldExec, oldNow, oldVerify, oldIn })
	env := map[string]string{"XDG_CONFIG_HOME": filepath.Join(t.TempDir(), "cfg"), "XDG_STATE_HOME": t.TempDir()}
	return f, env, dir
}

const plainConfig = "[core]\n\tbare = false\n[remote \"origin\"]\n\turl = https://github.com/Homebrew/brew\n"

func TestRepairHealthyCheckout(t *testing.T) {
	f, env, dir := repairSetup(t, plainConfig, map[string]string{"git ls-remote": headSHA + "\trefs/tags/5.0.2\n"})
	code, out, errs := runCmd(env, "repair", "--dir", dir)
	if code != 0 || errs != "" || !strings.Contains(out, "nothing to repair") || !strings.Contains(out, "official release 5.0.2") {
		t.Fatalf("code=%d err=%s out=%s", code, errs, out)
	}
	if f.ran("git fetch") || f.ran("tar") {
		t.Fatal("a healthy checkout must not be touched")
	}
}

func TestRepairReportsDamageAndChangesNothing(t *testing.T) {
	f, env, dir := repairSetup(t, plainConfig, map[string]string{"git ls-files --deleted": "Library/a.rb\n", "git ls-files --modified": "Library/a.rb\nLibrary/m.rb\n", "git ls-remote": ""})
	code, out, _ := runCmd(env, "repair", "--dir", dir)
	if code != 1 {
		t.Fatalf("code=%d out=%s", code, out)
	}
	for _, want := range []string{"FAIL tracked files match the commit (1 deleted, 1 modified)", "FAIL commit is not an official release",
		"deleted Library/a.rb", "With --apply it would", "Cellar"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	for _, k := range []string{"git fetch", "git checkout", "git clean", "tar"} {
		if f.ran(k) {
			t.Errorf("preview must not run %s", k)
		}
	}
}

func TestRepairOfflineSkipsGitHub(t *testing.T) {
	f, env, dir := repairSetup(t, plainConfig, nil)
	code, out, _ := runCmd(env, "repair", "--offline", "--dir", dir)
	if code != 0 || f.ran("git ls-remote") || !strings.Contains(out, "skip commit compared") {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestRepairGitHubUnreachableIsOnlyAWarning(t *testing.T) {
	f, env, dir := repairSetup(t, plainConfig, nil)
	f.fail["git ls-remote"] = errors.New("offline")
	code, out, errs := runCmd(env, "repair", "--dir", dir)
	if code != 0 || !strings.Contains(errs, "warning") || !strings.Contains(out, "skip commit compared") {
		t.Fatalf("code=%d err=%s out=%s", code, errs, out)
	}
}

func TestRepairApplyAfterYes(t *testing.T) {
	answers := map[string]string{"git ls-files --deleted": "a.rb\n", "git tag": "5.0.2\n", "git log": "2026-09-01T00:00:00Z\n"}
	f, env, dir := repairSetup(t, plainConfig, answers)
	repairStdin = strings.NewReader("yes\n")
	code, out, errs := runCmd(env, "repair", "--apply", "--dir", dir)
	if code != 0 || errs != "" {
		t.Fatalf("code=%d err=%s out=%s", code, errs, out)
	}
	for _, k := range []string{"tar", "git fetch", "git checkout", "git clean"} {
		if !f.ran(k) {
			t.Errorf("%s did not run", k)
		}
	}
	if !strings.Contains(out, "using release 5.0.2") || !strings.Contains(out, "done, brew now runs: Homebrew 7.0.13") || strings.Contains(out, "second line") {
		t.Fatalf("out=%s", out)
	}
}

func TestRepairApplyCancelled(t *testing.T) {
	f, env, dir := repairSetup(t, plainConfig, map[string]string{"git ls-files --deleted": "a.rb\n"})
	repairStdin = strings.NewReader("no\n")
	code, out, _ := runCmd(env, "repair", "--apply", "--dir", dir)
	if code != 1 || !strings.Contains(out, "cancelled") || f.ran("tar") || f.ran("git fetch") {
		t.Fatalf("code=%d out=%s calls=%v", code, out, f.calls)
	}
	repairStdin = strings.NewReader("")
	if code, _, _ := runCmd(env, "repair", "--apply", "--dir", dir); code != 1 || f.ran("tar") {
		t.Fatal("no answer must mean no")
	}
}

func TestRepairApplyYesFlagSkipsThePrompt(t *testing.T) {
	answers := map[string]string{"git ls-files --deleted": "a.rb\n", "git tag": "5.0.2\n", "git log": "2026-09-01T00:00:00Z\n"}
	_, env, dir := repairSetup(t, plainConfig, answers)
	repairStdin = strings.NewReader("")
	if code, out, _ := runCmd(env, "repair", "--apply", "--yes", "--dir", dir); code != 0 || strings.Contains(out, "[yes/no]") {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestRepairApplyStillBroken(t *testing.T) {
	answers := map[string]string{"git ls-files --deleted": "a.rb\n", "git tag": "5.0.2\n", "git log": "2026-09-01T00:00:00Z\n"}
	_, env, dir := repairSetup(t, plainConfig, answers)
	verifyBrew = func(context.Context) (string, error) { return "", errors.New("missing file") }
	code, _, errs := runCmd(env, "repair", "--apply", "--yes", "--dir", dir)
	if code != 2 || !strings.Contains(errs, "still does not start") {
		t.Fatalf("code=%d err=%s", code, errs)
	}
}

func TestRepairApplyFailure(t *testing.T) {
	f, env, dir := repairSetup(t, plainConfig, map[string]string{"git ls-files --deleted": "a.rb\n"})
	f.fail["git fetch"] = errors.New("network down")
	code, _, errs := runCmd(env, "repair", "--apply", "--yes", "--dir", dir)
	if code != 2 || !strings.Contains(errs, "fetch failed") || f.ran("git checkout") {
		t.Fatalf("code=%d err=%s", code, errs)
	}
}

func TestRepairRefusesUnsafeConfig(t *testing.T) {
	f, env, dir := repairSetup(t, "[core]\n\tfsmonitor = /tmp/evil\n", nil)
	for _, args := range [][]string{{"repair", "--dir", dir}, {"repair", "--apply", "--yes", "--dir", dir}} {
		code, out, _ := runCmd(env, args...)
		if code != 1 || !strings.Contains(out, "can run programs") || !strings.Contains(out, "altered") {
			t.Fatalf("%v: code=%d out=%s", args, code, out)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("git must not run in a checkout with such a config: %v", f.calls)
	}
}

func TestRepairErrors(t *testing.T) {
	_, env, dir := repairSetup(t, plainConfig, nil)
	if code, _, errs := runCmd(env, "repair", "--dir", filepath.Join(dir, "nope")); code != 2 || errs == "" {
		t.Fatalf("missing checkout: code=%d", code)
	}
	if code, _, _ := runCmd(env, "repair", "extra"); code != 2 {
		t.Fatal("extra arguments must be refused")
	}
	if code, _, _ := runCmd(env, "repair", "--nope"); code != 2 {
		t.Fatal("unknown flag must be refused")
	}
	f, env2, dir2 := repairSetup(t, plainConfig, nil)
	f.fail["git rev-parse"] = errors.New("not a repository")
	if code, _, _ := runCmd(env2, "repair", "--dir", dir2); code != 2 {
		t.Fatal("a folder that is not a git checkout must be an error")
	}
	badPolicy := t.TempDir()
	writeFile(t, filepath.Join(badPolicy, "brew-guard", "policy.toml"), "nonsense = 1\n")
	env4 := map[string]string{"XDG_CONFIG_HOME": badPolicy}
	_, _, dir4 := repairSetup(t, plainConfig, nil)
	if code, _, _ := runCmd(env4, "repair", "--dir", dir4); code != 2 {
		t.Fatal("an invalid policy must be an error")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}

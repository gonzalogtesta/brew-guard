package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gonzalogtesta/brew-guard/internal/dates"
	"github.com/gonzalogtesta/brew-guard/internal/doctor"
)

var checkNow = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

type fakeRunner struct {
	out   map[string]string
	calls []string
}

func (f *fakeRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	k := strings.Join(args, " ")
	f.calls = append(f.calls, k)
	if v, ok := f.out[k]; ok {
		return []byte(v), nil
	}
	return nil, errors.New("unexpected brew call: " + k)
}

type fakeHistory struct {
	calls int
	h     []dates.Commit
	err   error
}

func (f *fakeHistory) History(context.Context, string, string) ([]dates.Commit, error) {
	f.calls++
	return f.h, f.err
}

func infoJSON(name, tap, version string) string {
	return `{"formulae":[{"name":"` + name + `","tap":"` + tap + `","ruby_source_path":"Formula/x/` + name +
		`.rb","versions":{"stable":"` + version + `"}}],"casks":[]}`
}

type harness struct {
	runner *fakeRunner
	hist   *fakeHistory
	env    map[string]string
	dir    string
	execs  [][]string // arguments brew would have been started with
}

// setup installs fakes. The package "jq 1.8.2" was introduced `ageDays` ago.
func setup(t *testing.T, ageDays int, policyBody string) *harness {
	t.Helper()
	dir := t.TempDir()
	h := &harness{
		runner: &fakeRunner{out: map[string]string{
			"install --dry-run jq":    "==> Would install 1 formula:\njq 1.8.2\n",
			"install --dry-run weird": "==> Would install 1 formula:\nweird 1.0\n",
			"info --json=v2 jq":       infoJSON("jq", "homebrew/core", "1.8.2"),
			"info --json=v2 weird":    infoJSON("weird", "me/tools", "1.0"),
			"outdated --json=v2":      `{"formulae":[{"name":"jq","installed_versions":["1.8.1"],"current_version":"1.8.2"}],"casks":[]}`,
		}},
		hist: &fakeHistory{h: []dates.Commit{
			{SHA: "abcdef123456", Time: checkNow.AddDate(0, 0, -ageDays), Message: "jq 1.8.2"},
			{SHA: "ff", Time: checkNow.AddDate(0, 0, -ageDays), Message: "weird 1.0"},
		}},
		env: map[string]string{"XDG_CONFIG_HOME": filepath.Join(dir, "cfg"), "HOMEBREW_NO_ANALYTICS": "1"},
		dir: dir,
	}
	if policyBody != "" {
		writeFile(t, filepath.Join(dir, "cfg", "brew-guard", "policy.toml"), policyBody)
	}
	oldDeps, oldAn := newCheckDeps, analyticsCheck
	newCheckDeps = func(func(string) string) (checkDeps, error) {
		return checkDeps{Runner: h.runner, History: h.hist,
			Cache: dates.OpenCache(filepath.Join(dir, "cache.json")), Now: func() time.Time { return checkNow },
			AuditPath: filepath.Join(dir, "audit.jsonl"),
			Exec:      func(a []string) error { h.execs = append(h.execs, a); return nil }}, nil
	}
	analyticsCheck = func() doctor.Check { return doctor.Check{Name: "analytics", OK: true} }
	t.Cleanup(func() { newCheckDeps, analyticsCheck = oldDeps, oldAn })
	return h
}

func (h *harness) audit(t *testing.T) string {
	b, _ := os.ReadFile(filepath.Join(h.dir, "audit.jsonl"))
	return string(b)
}

func TestCheckAllowsOldVersion(t *testing.T) {
	h := setup(t, 30, "")
	code, out, errs := runCmd(h.env, "check", "jq")
	if code != 0 || errs != "" {
		t.Fatalf("code=%d err=%q out=%q", code, errs, out)
	}
	for _, want := range []string{"ALLOW", "jq 1.8.2", "[homebrew/core]", "2026-10-13", "Homebrew/homebrew-core commit abcdef1234", "no policy file found"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if a := h.audit(t); !strings.Contains(a, `"package":"jq"`) || !strings.Contains(a, `"action":"allow"`) || !strings.Contains(a, `"command":"check"`) {
		t.Fatalf("audit: %s", a)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "cache.json")); err != nil {
		t.Fatal("date cache must be saved")
	}
}

func TestCheckBlocksYoungVersion(t *testing.T) {
	h := setup(t, 3, "")
	code, out, _ := runCmd(h.env, "check", "jq")
	if code != 1 || !strings.Contains(out, "BLOCK") || !strings.Contains(out, "3.0 days old (minimum 12)") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if !strings.Contains(h.audit(t), `"action":"block"`) {
		t.Fatal("block must be audited")
	}
}

func TestCheckWarnMode(t *testing.T) {
	h := setup(t, 3, "mode = \"warn\"\n")
	code, out, _ := runCmd(h.env, "check", "jq")
	if code != 0 || !strings.Contains(out, "WARN") {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestCheckLookupFailureBlocks(t *testing.T) {
	h := setup(t, 30, "")
	h.hist.err = errors.New("GitHub rate limit reached")
	code, out, _ := runCmd(h.env, "check", "jq")
	if code != 1 || !strings.Contains(out, "publication date unknown") || !strings.Contains(out, "rate limit") {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestCheckOfflineUsesNoNetwork(t *testing.T) {
	h := setup(t, 30, "")
	code, out, _ := runCmd(h.env, "check", "--offline", "jq")
	if h.hist.calls != 0 {
		t.Fatal("offline must not query GitHub")
	}
	if code != 1 || !strings.Contains(out, "offline") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	// After an online run the cache serves the offline one.
	runCmd(h.env, "check", "jq")
	if code, _, _ := runCmd(h.env, "check", "--offline", "jq"); code != 0 {
		t.Fatalf("cached offline run: code=%d", code)
	}
}

func TestCheckOutdated(t *testing.T) {
	h := setup(t, 30, "")
	code, out, _ := runCmd(h.env, "check", "--outdated")
	if code != 0 || !strings.Contains(out, "jq 1.8.2") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if strings.Join(h.runner.calls, "|") != "outdated --json=v2|info --json=v2 jq" {
		t.Fatalf("calls: %v", h.runner.calls)
	}
}

func TestCheckNothingOutdated(t *testing.T) {
	h := setup(t, 30, "")
	h.runner.out["outdated --json=v2"] = `{"formulae":[],"casks":[]}`
	if code, out, _ := runCmd(h.env, "check", "--outdated"); code != 0 || !strings.Contains(out, "nothing is outdated") {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestCheckUnlistedTapBlocksAndThirdPartyNeedsReview(t *testing.T) {
	h := setup(t, 30, "")
	if code, out, _ := runCmd(h.env, "check", "weird"); code != 1 || !strings.Contains(out, `tap "me/tools" is not in allowed_taps`) {
		t.Fatalf("unlisted: code=%d out=%s", code, out)
	}
	h2 := setup(t, 30, "allowed_taps = [\"homebrew/core\", \"me/tools\"]\n")
	if _, ok := dates.Repo("me/tools"); ok {
		t.Fatal("test assumes third-party taps have no date source")
	}
	// No date source for a third-party tap: the date is unknown, so it still blocks.
	if code, out, _ := runCmd(h2.env, "check", "weird"); code != 1 || !strings.Contains(out, "no date source") {
		t.Fatalf("third-party: code=%d out=%s", code, out)
	}
}

func TestCheckThirdPartyReviewExitCode(t *testing.T) {
	h := setup(t, 30, "mode = \"warn\"\nallowed_taps = [\"homebrew/core\", \"me/tools\"]\n")
	code, out, _ := runCmd(h.env, "check", "weird")
	if code != 3 || !strings.Contains(out, "REVIEW") || !strings.Contains(out, "review code and diff") {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestCheckTelemetry(t *testing.T) {
	h := setup(t, 30, "")
	delete(h.env, "HOMEBREW_NO_ANALYTICS")
	analyticsCheck = func() doctor.Check { return doctor.Check{OK: false, Detail: "run: brew analytics off"} }
	code, out, _ := runCmd(h.env, "check", "jq")
	if code != 1 || !strings.Contains(out, "BLOCK telemetry") || !strings.Contains(h.audit(t), "homebrew analytics enabled") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	// The variable alone is enough, and a policy can turn the rule off.
	h.env["HOMEBREW_NO_ANALYTICS"] = "1"
	if code, _, _ := runCmd(h.env, "check", "jq"); code != 0 {
		t.Fatalf("env set: code=%d", code)
	}
	h3 := setup(t, 30, "block_telemetry = false\n")
	delete(h3.env, "HOMEBREW_NO_ANALYTICS")
	analyticsCheck = func() doctor.Check { return doctor.Check{OK: false} }
	if code, _, _ := runCmd(h3.env, "check", "jq"); code != 0 {
		t.Fatalf("rule off: code=%d", code)
	}
}

func TestCheckUsageErrors(t *testing.T) {
	h := setup(t, 30, "")
	cases := [][]string{
		{"check"},
		{"check", "--outdated", "jq"},
		{"check", "--nope"},
		{"check", "-v"},
		{"check", "evil/tap/foo"},
		{"check", "unknownpkg"},
	}
	for _, args := range cases {
		if code, _, _ := runCmd(h.env, args...); code != 2 {
			t.Errorf("%v: code=%d, want 2", args, code)
		}
	}
}

func TestCheckPolicyProblems(t *testing.T) {
	h := setup(t, 30, "min_age_dayz = 3\n")
	if code, _, errs := runCmd(h.env, "check", "jq"); code != 2 || !strings.Contains(errs, "unknown key") {
		t.Fatalf("invalid policy must stop the check: code=%d err=%q", code, errs)
	}
	h2 := setup(t, 30, "")
	p := filepath.Join(h2.dir, "other.toml")
	writeFile(t, p, "min_age_days = 1\n")
	if code, _, _ := runCmd(h2.env, "check", "--policy", p, "jq"); code != 0 {
		t.Fatalf("--policy: code=%d", code)
	}
	if code, _, _ := runCmd(h2.env, "check", "--policy", filepath.Join(h2.dir, "missing.toml"), "jq"); code != 2 {
		t.Fatalf("missing --policy file: code=%d", code)
	}
}

func TestCheckExceptionShown(t *testing.T) {
	h := setup(t, 3, "[[exception]]\npackage = \"jq\"\nreason = \"urgent fix\"\nexpires = 2026-11-20\n")
	code, out, _ := runCmd(h.env, "check", "jq")
	if code != 0 || !strings.Contains(out, "exception: urgent fix") {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestCheckDeprecatedNote(t *testing.T) {
	h := setup(t, 30, "")
	h.runner.out["info --json=v2 jq"] = `{"formulae":[{"name":"jq","tap":"homebrew/core","deprecated":true,"ruby_source_path":"Formula/j/jq.rb","versions":{"stable":"1.8.2"}}]}`
	if _, out, _ := runCmd(h.env, "check", "jq"); !strings.Contains(out, "deprecated upstream") {
		t.Fatalf("out=%s", out)
	}
}

func TestCheckBrewMissing(t *testing.T) {
	h := setup(t, 30, "")
	newCheckDeps = defaultCheckDeps
	old := brewCandidates
	brewCandidates = []string{filepath.Join(h.dir, "no-brew")}
	defer func() { brewCandidates = old }()
	if code, _, errs := runCmd(h.env, "check", "jq"); code != 2 || !strings.Contains(errs, "no brew found") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}

func TestDefaultCheckDeps(t *testing.T) {
	dir := t.TempDir()
	brew := filepath.Join(dir, "brew")
	writeFile(t, brew, "#!/bin/sh\n")
	os.Chmod(brew, 0o755)
	old := brewCandidates
	brewCandidates = []string{brew}
	defer func() { brewCandidates = old }()
	env := map[string]string{"HOME": "/home/x", "XDG_STATE_HOME": "/state", "GH_TOKEN": "t"}
	d, err := defaultCheckDeps(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if d.AuditPath != "/state/brew-guard/audit.jsonl" || d.Now == nil || d.Cache == nil {
		t.Fatalf("%+v", d)
	}
	if gh, ok := d.History.(*dates.GitHub); !ok || gh.Token != "t" || gh.BaseURL != dates.APIBase {
		t.Fatalf("history: %+v", d.History)
	}
	delete(env, "XDG_STATE_HOME")
	if d, _ := defaultCheckDeps(func(k string) string { return env[k] }); d.AuditPath != "/home/x/.local/state/brew-guard/audit.jsonl" {
		t.Fatalf("fallback path: %s", d.AuditPath)
	}
}

func TestCheckPrintsInstallSignals(t *testing.T) {
	h := setup(t, 30, "")
	h.runner.out["info --json=v2 jq"] = `{"formulae":[{"name":"jq","tap":"homebrew/core","post_install_defined":true,"service":{"run":["x"]},"ruby_source_path":"Formula/j/jq.rb","versions":{"stable":"1.8.2"}}]}`
	code, out, _ := runCmd(h.env, "check", "jq")
	if code != 0 || !strings.Contains(out, "* runs a post-install step") || !strings.Contains(out, "* installs a background service") {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

// withDependency makes brew say that installing jq also installs oniguruma,
// introduced `ageDays` ago.
func withDependency(h *harness, ageDays int) {
	h.runner.out["install --dry-run jq"] = "==> Would install 1 formula:\njq 1.8.2\n==> Would install 1 dependency for jq:\noniguruma 6.9.10\n"
	h.runner.out["info --json=v2 jq oniguruma"] = `{"formulae":[` +
		`{"name":"jq","tap":"homebrew/core","versions":{"stable":"1.8.2"},"ruby_source_path":"Formula/j/jq.rb"},` +
		`{"name":"oniguruma","tap":"homebrew/core","versions":{"stable":"6.9.10"},"ruby_source_path":"Formula/o/oniguruma.rb"}],"casks":[]}`
	h.hist.h = []dates.Commit{
		{SHA: "aaaaaaaaaaaa", Time: checkNow.AddDate(0, 0, -30), Message: "jq 1.8.2"},
		{SHA: "bbbbbbbbbbbb", Time: checkNow.AddDate(0, 0, -ageDays), Message: "oniguruma 6.9.10"},
	}
}

func TestCheckListsAndChecksDependencies(t *testing.T) {
	h := setup(t, 30, "")
	withDependency(h, 30)
	code, out, errs := runCmd(h.env, "check", "jq")
	if code != 0 || errs != "" {
		t.Fatalf("code=%d err=%q out=%q", code, errs, out)
	}
	for _, want := range []string{"installing jq would also install 1 more package: oniguruma", "oniguruma 6.9.10", "installed as a dependency of jq"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if a := h.audit(t); !strings.Contains(a, `"package":"oniguruma"`) {
		t.Fatalf("dependency must be audited: %s", a)
	}
}

func TestCheckYoungDependencyBlocksTheInstall(t *testing.T) {
	h := setup(t, 30, "")
	withDependency(h, 2)
	code, out, _ := runCmd(h.env, "check", "jq")
	if code != 1 {
		t.Fatalf("a young dependency must block, code=%d out=%s", code, out)
	}
	if !strings.Contains(out, "ALLOW  jq") || !strings.Contains(out, "BLOCK  oniguruma") {
		t.Fatalf("jq is old enough, only the dependency should block:\n%s", out)
	}
}

func TestCheckNoDepsSkipsTheInstallSet(t *testing.T) {
	h := setup(t, 30, "")
	withDependency(h, 2)
	code, out, _ := runCmd(h.env, "check", "--no-deps", "jq")
	if code != 0 || strings.Contains(out, "oniguruma") {
		t.Fatalf("--no-deps must check only jq, code=%d out=%s", code, out)
	}
	for _, c := range h.runner.calls {
		if strings.HasPrefix(c, "install") {
			t.Fatalf("--no-deps must not call install: %v", h.runner.calls)
		}
	}
}

func TestCheckNothingToInstall(t *testing.T) {
	h := setup(t, 30, "")
	h.runner.out["install --dry-run jq"] = "Warning: jq 1.8.2 is already installed and up-to-date.\n"
	code, out, _ := runCmd(h.env, "check", "jq")
	if code != 0 || !strings.Contains(out, "brew has nothing new to install for jq") || !strings.Contains(out, "ALLOW  jq") {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestCheckUnreadableDryRunIsAnError(t *testing.T) {
	h := setup(t, 30, "")
	h.runner.out["install --dry-run jq"] = "==> Would install 3 formulae:\njq 1.8.2\n"
	if code, _, errs := runCmd(h.env, "check", "jq"); code != 2 || !strings.Contains(errs, "announced 3") {
		t.Fatalf("code=%d err=%s", code, errs)
	}
}

func TestCheckOutdatedChecksNewDependencies(t *testing.T) {
	h := setup(t, 30, "")
	h.runner.out["info --json=v2 jq"] = `{"formulae":[{"name":"jq","tap":"homebrew/core","versions":{"stable":"1.8.2"},` +
		`"ruby_source_path":"Formula/j/jq.rb","dependencies":["oniguruma","zlib"],"installed":[{"version":"1.8.1"}]}],"casks":[]}`
	h.runner.out["info --json=v2 oniguruma zlib"] = `{"formulae":[` +
		`{"name":"oniguruma","tap":"homebrew/core","versions":{"stable":"6.9.10"},"ruby_source_path":"Formula/o/oniguruma.rb","installed":[]},` +
		`{"name":"zlib","tap":"homebrew/core","versions":{"stable":"1.3"},"ruby_source_path":"Formula/z/zlib.rb","installed":[{"version":"1.3"}]}],"casks":[]}`
	h.hist.h = []dates.Commit{
		{SHA: "aaaaaaaaaaaa", Time: checkNow.AddDate(0, 0, -30), Message: "jq 1.8.2"},
		{SHA: "bbbbbbbbbbbb", Time: checkNow.AddDate(0, 0, -2), Message: "oniguruma 6.9.10"},
	}
	code, out, errs := runCmd(h.env, "check", "--outdated")
	if code != 1 || errs != "" {
		t.Fatalf("a young new dependency must block, code=%d err=%s out=%s", code, errs, out)
	}
	for _, want := range []string{"upgrading would also install 1 new package: oniguruma", "BLOCK  oniguruma", "installed as a dependency of jq"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "zlib") {
		t.Fatalf("an installed dependency is not new:\n%s", out)
	}
}

func TestCheckOutdatedNoDeps(t *testing.T) {
	h := setup(t, 30, "")
	h.runner.out["info --json=v2 jq"] = `{"formulae":[{"name":"jq","tap":"homebrew/core","versions":{"stable":"1.8.2"},"ruby_source_path":"Formula/j/jq.rb","dependencies":["oniguruma"]}],"casks":[]}`
	if code, out, _ := runCmd(h.env, "check", "--outdated", "--no-deps"); code != 0 || strings.Contains(out, "oniguruma") {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestCheckNamesTheRequestedPackageWhenOnlyDependenciesAreNew(t *testing.T) {
	h := setup(t, 30, "")
	// jq is installed already, brew only lists what its upgrade would add.
	h.runner.out["install --dry-run jq"] = "==> Would install 1 dependency for jq:\noniguruma 6.9.10\n"
	h.runner.out["info --json=v2 jq oniguruma"] = `{"formulae":[` +
		`{"name":"jq","tap":"homebrew/core","versions":{"stable":"1.8.2"},"ruby_source_path":"Formula/j/jq.rb"},` +
		`{"name":"oniguruma","tap":"homebrew/core","versions":{"stable":"6.9.10"},"ruby_source_path":"Formula/o/oniguruma.rb"}],"casks":[]}`
	h.hist.h = []dates.Commit{
		{SHA: "aaaaaaaaaaaa", Time: checkNow.AddDate(0, 0, -30), Message: "jq 1.8.2"},
		{SHA: "bbbbbbbbbbbb", Time: checkNow.AddDate(0, 0, -30), Message: "oniguruma 6.9.10"},
	}
	_, out, _ := runCmd(h.env, "check", "jq")
	if !strings.Contains(out, "installing jq would also install 1 more package: oniguruma") {
		t.Fatalf("the summary must name jq:\n%s", out)
	}
}

// limitedHistory answers the first lookups, then reports the GitHub limit.
type limitedHistory struct {
	calls, okCalls int
	reset          time.Time
}

func (l *limitedHistory) History(_ context.Context, _, path string) ([]dates.Commit, error) {
	l.calls++
	if l.calls > l.okCalls {
		return nil, &dates.RateLimitError{Reset: l.reset}
	}
	name := strings.TrimSuffix(filepath.Base(path), ".rb")
	return []dates.Commit{{SHA: "abcdef123456", Time: checkNow.AddDate(0, 0, -30), Message: name + " 1.0"}}, nil
}

func TestCheckStopsAskingGitHubAfterTheLimit(t *testing.T) {
	h := setup(t, 30, "")
	pkg := func(n string) string {
		return `{"name":"` + n + `","tap":"homebrew/core","versions":{"stable":"1.0"},"ruby_source_path":"Formula/` + n[:1] + `/` + n + `.rb"}`
	}
	h.runner.out["info --json=v2 aa bb cc dd"] = `{"formulae":[` + pkg("aa") + "," + pkg("bb") + "," + pkg("cc") + "," + pkg("dd") + `],"casks":[]}`
	lim := &limitedHistory{okCalls: 1, reset: time.Date(2026, 11, 13, 20, 30, 0, 0, time.UTC)}
	old := newCheckDeps
	d, _ := old(func(string) string { return "" })
	d.History = lim
	newCheckDeps = func(func(string) string) (checkDeps, error) { return d, nil }

	code, out, _ := runCmd(h.env, "check", "--no-deps", "aa", "bb", "cc", "dd")
	if code != 1 {
		t.Fatalf("unchecked packages must block, code=%d\n%s", code, out)
	}
	if lim.calls != 2 {
		t.Fatalf("after the limit nothing more may be asked: %d calls", lim.calls)
	}
	for _, want := range []string{"ALLOW  aa", "BLOCK  bb", "BLOCK  cc", "BLOCK  dd", "3 of 4 packages could not be checked",
		"bb, cc, dd", "GITHUB_TOKEN raises the limit but is optional", "the dates found so far are saved"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// What was found is saved: the next run needs no request for aa.
	lim2 := &limitedHistory{okCalls: 10}
	d.History = lim2
	runCmd(h.env, "check", "--no-deps", "aa")
	if lim2.calls != 0 {
		t.Fatalf("aa must come from the cache, got %d requests", lim2.calls)
	}
}

func TestFirstNames(t *testing.T) {
	if got := firstNames([]string{"a", "b"}, 6); got != "a, b" {
		t.Fatal(got)
	}
	if got := firstNames([]string{"a", "b", "c", "d"}, 2); got != "a, b and 2 more" {
		t.Fatal(got)
	}
}

// withUpgradedDependency makes brew say that installing jq would also upgrade
// the installed dependency oniguruma, whose new version is `ageDays` old.
func withUpgradedDependency(h *harness, ageDays int) {
	h.runner.out["install --dry-run jq"] = "==> Would install 1 formula:\njq 1.8.2\n==> Would upgrade 1 dependency for jq:\noniguruma 6.9.9 -> 6.9.10\n"
	h.runner.out["info --json=v2 jq oniguruma"] = `{"formulae":[` +
		`{"name":"jq","tap":"homebrew/core","versions":{"stable":"1.8.2"},"ruby_source_path":"Formula/j/jq.rb"},` +
		`{"name":"oniguruma","tap":"homebrew/core","versions":{"stable":"6.9.10"},"ruby_source_path":"Formula/o/oniguruma.rb"}],"casks":[]}`
	h.hist.h = []dates.Commit{
		{SHA: "aaaaaaaaaaaa", Time: checkNow.AddDate(0, 0, -30), Message: "jq 1.8.2"},
		{SHA: "bbbbbbbbbbbb", Time: checkNow.AddDate(0, 0, -ageDays), Message: "oniguruma 6.9.10"},
	}
}

func TestCheckChecksDependenciesThatBrewWouldUpgrade(t *testing.T) {
	h := setup(t, 30, "")
	withUpgradedDependency(h, 2)
	code, out, _ := runCmd(h.env, "check", "jq")
	if code != exitBlock {
		t.Fatalf("code=%d, a young upgrade of a dependency must block:\n%s", code, out)
	}
	for _, want := range []string{"brew would also upgrade oniguruma 6.9.9 -> 6.9.10", "BLOCK  oniguruma 6.9.10", "installed as a dependency of jq"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestWrapDoesNotRunBrewWhenAnUpgradeIsTooYoung(t *testing.T) {
	h := setup(t, 30, "")
	withUpgradedDependency(h, 2)
	code, _, _ := runCmd(h.env, "brew", "install", "jq")
	if code != exitBlock || len(h.execs) != 0 {
		t.Fatalf("code=%d execs=%v: brew must not run", code, h.execs)
	}
	h2 := setup(t, 30, "")
	withUpgradedDependency(h2, 30)
	if code, _, _ := runCmd(h2.env, "brew", "install", "jq"); code != 0 || len(h2.execs) != 1 {
		t.Fatalf("an old upgrade should pass: code=%d execs=%v", code, h2.execs)
	}
}

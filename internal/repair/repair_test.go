package repair

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const cleanConfig = `[core]
	repositoryformatversion = 0
	filemode = true
	bare = false
	logallrefupdates = true
[remote "origin"]
	url = https://github.com/Homebrew/brew
	fetch = +refs/heads/*:refs/remotes/origin/*
[branch "stable"]
	remote = origin
	merge = refs/heads/stable
[homebrew]
	analyticsdisabled = true
`

func TestUnsafeConfigCleanHomebrew(t *testing.T) {
	if got := UnsafeConfig([]byte(cleanConfig)); len(got) != 0 {
		t.Fatalf("a normal Homebrew config must be clean, got %v", got)
	}
}

func TestUnsafeConfigFindsRiskySettings(t *testing.T) {
	for name, cfg := range map[string]string{
		"fsmonitor":  "[core]\n\tfsmonitor = /tmp/x\n",
		"hooksPath":  "[core]\n\thooksPath = /tmp/h\n",
		"sshCommand": "[core]\n\tsshCommand = evil\n",
		"alias":      "[alias]\n\tco = !evil\n",
		"include":    "[include]\n\tpath = /tmp/x\n",
		"includeIf":  "[includeIf \"gitdir:/\"]\n\tpath = /tmp/x\n",
		"credential": "[credential]\n\thelper = !evil\n",
		"filter":     "[filter \"x\"]\n\tclean = evil\n",
		"insteadOf":  "[url \"https://evil/\"]\n\tinsteadOf = https://github.com/\n",
		"textconv":   "[diff \"x\"]\n\ttextconv = evil\n",
		"sslverify":  "[http]\n\tsslVerify = false\n",
		"proxy":      "[http]\n\tproxy = http://evil\n",
		"uploadpack": "[remote \"origin\"]\n\tuploadpack = evil\n",
		"protocol":   "[protocol \"ext\"]\n\tallow = always\n",
		"same line":  "[core] fsmonitor = /tmp/x\n",
		"case":       "[CORE]\n\tHooksPath = /tmp\n",
	} {
		if got := UnsafeConfig([]byte(cfg)); len(got) == 0 {
			t.Errorf("%s: not detected", name)
		}
	}
}

func TestFsmonitorFalseIsHarmless(t *testing.T) {
	for _, v := range []string{"false", "False", "no", "off", "0"} {
		if got := UnsafeConfig([]byte("[core]\n\tfsmonitor = " + v + "\n")); len(got) != 0 {
			t.Errorf("fsmonitor = %s is not dangerous: %v", v, got)
		}
	}
	for _, v := range []string{"true", "/tmp/hook", "", "watchman"} {
		if got := UnsafeConfig([]byte("[core]\n\tfsmonitor = " + v + "\n")); len(got) == 0 {
			t.Errorf("fsmonitor = %q must be reported", v)
		}
	}
}

func TestInstalledHooks(t *testing.T) {
	dir := t.TempDir()
	hooks := filepath.Join(dir, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := InstalledHooks(dir); len(got) != 0 {
		t.Fatalf("empty: %v", got)
	}
	for _, n := range []string{"pre-commit.sample", "post-checkout"} {
		if err := os.WriteFile(filepath.Join(hooks, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := InstalledHooks(dir); len(got) != 1 || got[0] != "post-checkout" {
		t.Fatalf("got %v", got)
	}
	if got := InstalledHooks(filepath.Join(dir, "nothing")); got != nil {
		t.Fatalf("a missing folder: %v", got)
	}
}

func TestSortedTagsAndPick(t *testing.T) {
	got := SortedTags([]string{"4.9.0", "5.0.10", "5.0.2", "5.0.2-rc1", "v5.1.0", "5.0", "05.0.1", "5.1.0"})
	want := []string{"5.1.0", "5.0.10", "5.0.2", "4.9.0"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	rels := []Release{{"5.1.0", now.AddDate(0, 0, -3)}, {"5.0.10", now.AddDate(0, 0, -12)}, {"5.0.2", now.AddDate(0, 0, -40)}}
	if r, err := Pick(rels, now, 12); err != nil || r.Name != "5.0.10" {
		t.Fatalf("%+v %v", r, err)
	}
	if r, err := Pick(rels, now, 30); err != nil || r.Name != "5.0.2" {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := Pick(rels, now, 100); err == nil {
		t.Fatal("nothing old enough must be an error")
	}
	if _, err := Pick([]Release{{"1.0.0", time.Time{}}}, now, 1); err == nil {
		t.Fatal("an unknown date must not count")
	}
}

func TestMatchTag(t *testing.T) {
	out := "aaa\trefs/tags/5.0.1\nbbb\trefs/tags/5.0.2\nccc\trefs/tags/5.0.2^{}\nddd\trefs/tags/nightly\n"
	if got := matchTag(out, "ccc"); got != "5.0.2" {
		t.Fatalf("annotated tag: %q", got)
	}
	if got := matchTag(out, "aaa"); got != "5.0.1" {
		t.Fatalf("lightweight tag: %q", got)
	}
	if matchTag(out, "ddd") != "" || matchTag(out, "zzz") != "" {
		t.Fatal("a non-release tag or unknown commit must not match")
	}
}

// fake records calls and answers by git subcommand.
type fake struct {
	calls   []string
	answers map[string]string
	fail    map[string]error
}

func (f *fake) exec(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name
	if name == gitBin {
		key = "git " + subcommand(args)
		if key == "git ls-files" {
			key += " " + args[len(gitArgs("", "x"))]
		}
	} else if name == "/usr/bin/tar" {
		key = "tar"
	}
	f.calls = append(f.calls, key+"|"+strings.Join(args, " "))
	if err := f.fail[key]; err != nil {
		return nil, err
	}
	return []byte(f.answers[key]), nil
}

func subcommand(args []string) string {
	i := len(gitArgs("", "x")) - 1 // position of the subcommand
	if len(args) > i && args[0] == "-C" {
		return args[i]
	}
	return args[0]
}

func (f *fake) ran(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func damaged() *fake {
	return &fake{answers: map[string]string{
		"git rev-parse":           "0123456789abcdef0123456789abcdef01234567\n",
		"git config":              "https://example.com/other\n",
		"git ls-files --deleted":  "a.rb\nb.rb\n",
		"git ls-files --modified": "a.rb\nb.rb\nc.rb\nd.rb\n",
		"git ls-files --others":   "x.rb\ny.rb\n",
		"git tag":                 "5.1.0\n5.0.10\n5.0.2\n",
		"git log":                 "2026-10-01T00:00:00Z\n",
	}}
}

func TestDiagnoseCounts(t *testing.T) {
	f := damaged()
	r, err := Diagnose(context.Background(), f.exec, "/h", []byte(cleanConfig))
	if err != nil {
		t.Fatal(err)
	}
	if r.Deleted != 2 || r.Modified != 2 || r.Untracked != 2 || r.Head != "0123456789" || r.Healthy() {
		t.Fatalf("%+v", r)
	}
	if len(r.Changed) != 6 || r.Changed[0] != "deleted a.rb" {
		t.Fatalf("changed: %v", r.Changed)
	}
	for _, c := range f.calls {
		for _, want := range []string{"core.hooksPath=/dev/null", "core.fsmonitor=false", "credential.helper=", "protocol.https.allow=always"} {
			if !strings.Contains(c, want) {
				t.Errorf("git call lacks %s: %s", want, c)
			}
		}
	}
}

func TestDiagnoseHealthy(t *testing.T) {
	f := &fake{answers: map[string]string{"git rev-parse": "abc\n", "git config": OfficialURL + "\n"}}
	r, err := Diagnose(context.Background(), f.exec, "/h", []byte(cleanConfig))
	if err != nil || !r.Healthy() {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestDiagnoseCapsTheList(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString("f\n")
	}
	f := &fake{answers: map[string]string{"git rev-parse": "abc\n", "git ls-files --deleted": b.String()}}
	r, _ := Diagnose(context.Background(), f.exec, "/h", nil)
	if len(r.Changed) != maxListed || r.Deleted != 50 {
		t.Fatalf("%d listed, %d deleted", len(r.Changed), r.Deleted)
	}
}

func TestDiagnoseUnsafeConfigRunsNoGit(t *testing.T) {
	f := damaged()
	r, err := Diagnose(context.Background(), f.exec, "/h", []byte("[core]\n\tfsmonitor = /tmp/x\n"))
	if err != nil || len(r.Unsafe) == 0 || len(f.calls) != 0 {
		t.Fatalf("%+v %v calls=%v", r, err, f.calls)
	}
}

func TestDiagnoseNotAGitCheckout(t *testing.T) {
	f := &fake{fail: map[string]error{"git rev-parse": errors.New("not a git repository")}}
	if _, err := Diagnose(context.Background(), f.exec, "/h", nil); err == nil {
		t.Fatal("expected an error")
	}
}

func applyOpts(t *testing.T) Options {
	return Options{MinAgeDays: 12, BackupDir: filepath.Join(t.TempDir(), "backups"),
		Now: func() time.Time { return time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC) }}
}

func TestApplyOrderAndSafety(t *testing.T) {
	f := damaged()
	rep := Report{Dir: "/usr/local/Homebrew", Origin: "https://example.com/other"}
	var out strings.Builder
	rel, err := Apply(context.Background(), f.exec, rep, applyOpts(t), &out)
	if err != nil || rel.Name != "5.1.0" {
		t.Fatalf("%+v %v", rel, err)
	}
	var order []string
	for _, c := range f.calls {
		k, _, _ := strings.Cut(c, "|")
		order = append(order, k)
	}
	want := "tar,git fetch,git tag,git log,git log,git log,git remote,git checkout,git clean"
	if got := strings.Join(order, ","); got != want {
		t.Fatalf("order:\n got %s\nwant %s", got, want)
	}
	all := strings.Join(f.calls, "\n")
	for _, want := range []string{"fetch --force --no-tags " + OfficialURL, "remote set-url origin " + OfficialURL,
		"checkout --force -B stable refs/tags/5.1.0", "clean -fd -- Library/Homebrew", "-czf"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if !strings.Contains(out.String(), "backing up") || !strings.Contains(out.String(), "using release 5.1.0") {
		t.Fatalf("progress: %s", out.String())
	}
}

func TestApplyKeepsOfficialOrigin(t *testing.T) {
	f := damaged()
	if _, err := Apply(context.Background(), f.exec, Report{Dir: "/h", Origin: OfficialURL}, applyOpts(t), &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	if f.ran("git remote") {
		t.Fatal("an official origin must be left alone")
	}
}

func TestApplyPicksAnOldEnoughRelease(t *testing.T) {
	f := damaged()
	dates := map[string]string{"5.1.0": "2026-11-10T00:00:00Z", "5.0.10": "2026-11-01T00:00:00Z", "5.0.2": "2026-09-01T00:00:00Z"}
	exec := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == gitBin && subcommand(args) == "log" {
			for tag, d := range dates {
				if strings.Contains(strings.Join(args, " "), "refs/tags/"+tag+"^") {
					return []byte(d + "\n"), nil
				}
			}
		}
		return f.exec(ctx, name, args...)
	}
	rel, err := Apply(context.Background(), exec, Report{Dir: "/h", Origin: OfficialURL}, applyOpts(t), &strings.Builder{})
	if err != nil || rel.Name != "5.0.10" {
		t.Fatalf("a 3 day old release must be skipped: %+v %v", rel, err)
	}
}

func TestApplyRefusesUnsafeConfig(t *testing.T) {
	f := damaged()
	_, err := Apply(context.Background(), f.exec, Report{Dir: "/h", Unsafe: []string{"core fsmonitor"}}, applyOpts(t), &strings.Builder{})
	if err == nil || len(f.calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, f.calls)
	}
}

func TestApplyStopsOnBackupFailure(t *testing.T) {
	f := damaged()
	f.fail = map[string]error{"tar": errors.New("disk full")}
	_, err := Apply(context.Background(), f.exec, Report{Dir: "/h"}, applyOpts(t), &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "nothing was changed") || f.ran("git") {
		t.Fatalf("err=%v calls=%v", err, f.calls)
	}
}

func TestApplyStopsOnEachFailure(t *testing.T) {
	for _, step := range []string{"git fetch", "git tag", "git log", "git checkout", "git clean", "git remote"} {
		f := damaged()
		f.fail = map[string]error{step: errors.New("boom")}
		if _, err := Apply(context.Background(), f.exec, Report{Dir: "/h", Origin: "x"}, applyOpts(t), &strings.Builder{}); err == nil {
			t.Errorf("%s: expected an error", step)
		}
		if step == "git checkout" && f.ran("git clean") {
			t.Error("clean must not run after a failed checkout")
		}
	}
}

func TestApplyBadDate(t *testing.T) {
	f := damaged()
	f.answers["git log"] = "yesterday\n"
	if _, err := Apply(context.Background(), f.exec, Report{Dir: "/h"}, applyOpts(t), &strings.Builder{}); err == nil {
		t.Fatal("an unreadable tag date must stop the repair")
	}
}

func TestApplyNoOldEnoughRelease(t *testing.T) {
	f := damaged()
	f.answers["git log"] = "2026-11-12T00:00:00Z\n"
	if _, err := Apply(context.Background(), f.exec, Report{Dir: "/h"}, applyOpts(t), &strings.Builder{}); err == nil || f.ran("git checkout") {
		t.Fatalf("err=%v", err)
	}
}

func TestOfficialRelease(t *testing.T) {
	f := &fake{answers: map[string]string{"git ls-remote": "abc\trefs/tags/5.0.2\n"}}
	tag, err := OfficialRelease(context.Background(), f.exec, Report{HeadFull: "abc"})
	if err != nil || tag != "5.0.2" {
		t.Fatalf("%q %v", tag, err)
	}
	if _, err := OfficialRelease(context.Background(), f.exec, Report{}); err == nil {
		t.Fatal("no commit must be an error")
	}
	f.fail = map[string]error{"git ls-remote": errors.New("offline")}
	if _, err := OfficialRelease(context.Background(), f.exec, Report{HeadFull: "abc"}); err == nil {
		t.Fatal("network error must be returned")
	}
}

func TestReadConfig(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadConfig(dir); err == nil {
		t.Fatal("no .git must be an error")
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(cleanConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadConfig(dir); err != nil || !strings.Contains(string(b), "Homebrew/brew") {
		t.Fatalf("%v", err)
	}
	file := t.TempDir()
	if err := os.WriteFile(filepath.Join(file, ".git"), []byte("gitdir: /elsewhere"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadConfig(file); err == nil {
		t.Fatal(".git as a file must be refused")
	}
}

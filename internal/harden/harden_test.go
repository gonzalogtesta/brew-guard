package harden

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type env struct {
	profile, state, repo string
	opts                 Options
}

func setup(t *testing.T, profile string) env {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	git, _ := exec.LookPath("git")
	old := Git
	Git = git
	t.Cleanup(func() { Git = old })

	d := t.TempDir()
	e := env{profile: filepath.Join(d, ".zprofile"), state: filepath.Join(d, "state"), repo: filepath.Join(d, "brew")}
	if err := os.MkdirAll(filepath.Join(e.repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.repo, ".git", "config"), []byte("[core]\n\tbare = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if profile != "" {
		if err := os.WriteFile(e.profile, []byte(profile), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	e.opts = Options{Profile: e.profile, Repos: []string{filepath.Join(d, "missing"), e.repo}, StateDir: e.state,
		Now: func() time.Time { return time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC) }}
	return e
}

func (e env) text(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(e.profile)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (e env) apply(t *testing.T) Plan {
	t.Helper()
	p, err := MakePlan(e.opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(e.opts, p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestApplyAddsABlockAndTurnsAnalyticsOff(t *testing.T) {
	e := setup(t, "export PATH=/x\n")
	p := e.apply(t)
	if len(p.Add) != 3 {
		t.Fatalf("add=%v", p.Add)
	}
	got := e.text(t)
	for _, want := range []string{"export PATH=/x\n\n" + begin + "\n", "export HOMEBREW_NO_ANALYTICS=1\n", "export HOMEBREW_NO_AUTO_UPDATE=1\n", "export HOMEBREW_NO_INSECURE_REDIRECT=1\n", end + "\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if v, _ := gitGet(e.repo); v != "true" {
		t.Errorf("analytics=%q", v)
	}
	fi, _ := os.Stat(e.profile)
	if fi.Mode().Perm() != 0o640 {
		t.Errorf("mode changed to %v", fi.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(e.state, "backups", ".zprofile.20261114-100000")); err != nil {
		t.Errorf("no backup: %v", err)
	}
}

func TestApplyTwiceChangesNothing(t *testing.T) {
	e := setup(t, "")
	e.apply(t)
	p, err := MakePlan(e.opts)
	if err != nil {
		t.Fatal(err)
	}
	if p.Changes() {
		t.Fatalf("second plan still has changes: %+v", p)
	}
}

func TestPlanSkipsWhatTheProfileAlreadyExports(t *testing.T) {
	e := setup(t, "export HOMEBREW_NO_ANALYTICS=1\n  HOMEBREW_NO_AUTO_UPDATE=1\n# export HOMEBREW_NO_INSECURE_REDIRECT=1\n")
	p, err := MakePlan(e.opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Skip) != 2 || len(p.Add) != 1 || p.Add[0].Name != "HOMEBREW_NO_INSECURE_REDIRECT" {
		t.Fatalf("skip=%v add=%v", p.Skip, p.Add)
	}
}

func TestUndoRestoresTheProfileAndAnalytics(t *testing.T) {
	orig := "export PATH=/x\n"
	e := setup(t, orig)
	e.apply(t)
	_, notes, err := Undo(e.opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 {
		t.Errorf("notes=%v", notes)
	}
	if got := e.text(t); got != orig {
		t.Errorf("profile=%q, want %q", got, orig)
	}
	if v, _ := gitGet(e.repo); v != "" {
		t.Errorf("analytics should be unset again, got %q", v)
	}
	if _, err := os.Stat(filepath.Join(e.state, stateFile)); !os.IsNotExist(err) {
		t.Error("the record should be gone")
	}
}

func TestUndoPutsBackAnalyticsThatWereFalse(t *testing.T) {
	e := setup(t, "")
	if err := gitSet(e.repo, "false"); err != nil {
		t.Fatal(err)
	}
	e.apply(t)
	if _, _, err := Undo(e.opts); err != nil {
		t.Fatal(err)
	}
	if v, _ := gitGet(e.repo); v != "false" {
		t.Errorf("analytics=%q, want false", v)
	}
}

func TestUndoLeavesAnalyticsTheUserChangedSince(t *testing.T) {
	e := setup(t, "")
	e.apply(t)
	if err := gitSet(e.repo, "false"); err != nil {
		t.Fatal(err)
	}
	_, notes, err := Undo(e.opts)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := gitGet(e.repo); v != "false" {
		t.Errorf("analytics=%q", v)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "changed since") {
		t.Errorf("notes=%v", notes)
	}
}

func TestUndoKeepsAnalyticsItNeverChanged(t *testing.T) {
	e := setup(t, "")
	if err := gitSet(e.repo, "true"); err != nil {
		t.Fatal(err)
	}
	e.apply(t)
	if _, _, err := Undo(e.opts); err != nil {
		t.Fatal(err)
	}
	if v, _ := gitGet(e.repo); v != "true" {
		t.Errorf("analytics were already off before, undo must not turn them on: %q", v)
	}
}

func TestUndoWithoutRecord(t *testing.T) {
	e := setup(t, "")
	if _, _, err := Undo(e.opts); !errors.Is(err, ErrNoRecord) {
		t.Fatalf("err=%v", err)
	}
}

func TestDamagedBlocksAreRefused(t *testing.T) {
	for name, text := range map[string]string{
		"not closed": begin + "\nexport A=1\n",
		"no start":   "export A=1\n" + end + "\n",
		"twice":      begin + "\n" + end + "\n" + begin + "\n" + end + "\n",
	} {
		e := setup(t, text)
		if _, err := MakePlan(e.opts); err == nil {
			t.Errorf("%s: want an error", name)
		}
		if got := e.text(t); got != text {
			t.Errorf("%s: the file was changed", name)
		}
	}
}

func TestApplyRewritesAnOldBlockInPlace(t *testing.T) {
	e := setup(t, "a\n"+begin+"\nexport OLD=1\n"+end+"\nb\n")
	e.apply(t)
	got := e.text(t)
	if strings.Contains(got, "OLD") || !strings.HasPrefix(got, "a\n"+begin+"\n") || !strings.HasSuffix(got, end+"\nb\n") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestSymlinkedProfileIsWrittenThroughTheLink(t *testing.T) {
	e := setup(t, "")
	target := filepath.Join(t.TempDir(), "dotfiles-zprofile")
	if err := os.WriteFile(target, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(e.profile), ".zprofile-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	e.opts.Profile = link
	p, err := MakePlan(e.opts)
	if err != nil {
		t.Fatal(err)
	}
	if p.Profile != target {
		t.Fatalf("profile=%s", p.Profile)
	}
	if _, err := Apply(e.opts, p); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the link was replaced by a file")
	}
	if b, _ := os.ReadFile(target); !strings.Contains(string(b), begin) {
		t.Error("the target was not written")
	}
}

func TestProfileThatIsADirectoryIsRefused(t *testing.T) {
	e := setup(t, "")
	e.opts.Profile = filepath.Dir(e.profile)
	if _, err := MakePlan(e.opts); err == nil {
		t.Fatal("want an error")
	}
}

func TestNoHomebrewCheckout(t *testing.T) {
	e := setup(t, "")
	e.opts.Repos = []string{filepath.Join(t.TempDir(), "none")}
	p, err := MakePlan(e.opts)
	if err != nil {
		t.Fatal(err)
	}
	if p.Repo != "" || !p.Changes() {
		t.Fatalf("plan=%+v", p)
	}
	e.apply(t)
	if !strings.Contains(e.text(t), begin) {
		t.Error("the profile should still be written")
	}
}

func TestDefaultProfile(t *testing.T) {
	for shell, want := range map[string]string{"/bin/zsh": "/h/.zprofile", "/opt/homebrew/bin/bash": "/h/.bash_profile"} {
		got, err := DefaultProfile(shell, "/h")
		if err != nil || got != want {
			t.Errorf("%s: %q %v", shell, got, err)
		}
	}
	if _, err := DefaultProfile("/usr/bin/fish", "/h"); err == nil {
		t.Error("fish should need --profile")
	}
	if _, err := DefaultProfile("", "/h"); err == nil {
		t.Error("an empty SHELL should need --profile")
	}
}

func TestRoundTripOfAnEmptyProfile(t *testing.T) {
	e := setup(t, "")
	e.apply(t)
	if _, _, err := Undo(e.opts); err != nil {
		t.Fatal(err)
	}
	if got := e.text(t); got != "" {
		t.Errorf("profile=%q", got)
	}
}

func TestUndoKeepsTheNewlineOfABlockWithoutSeparator(t *testing.T) {
	e := setup(t, "a\n"+begin+"\nexport HOMEBREW_NO_ANALYTICS=1\n"+end+"\nb\n")
	e.apply(t)
	if _, _, err := Undo(e.opts); err != nil {
		t.Fatal(err)
	}
	if got := e.text(t); got != "a\nb\n" {
		t.Errorf("profile=%q", got)
	}
}

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
	dir     string
	profile string
	state   string
	repo    string
	opts    Options
}

// setup makes a home folder with a fake Homebrew checkout. profile is the
// content of ~/.zprofile, "-" means the file does not exist.
func setup(t *testing.T, profile string) env {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	old := Git
	Git = git
	t.Cleanup(func() { Git = old })

	d := t.TempDir()
	e := env{dir: d, profile: filepath.Join(d, ".zprofile"), state: filepath.Join(d, "state"), repo: filepath.Join(d, "brew")}
	write(t, filepath.Join(e.repo, ".git", "config"), "[core]\n\tbare = false\n", 0o644)
	if profile != "-" {
		write(t, e.profile, profile, 0o640)
	}
	e.opts = Options{
		Targets:  []Target{{Path: e.profile}},
		Repos:    []string{filepath.Join(d, "missing"), e.repo},
		StateDir: e.state,
		Now:      func() time.Time { return time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC) },
	}
	return e
}

func write(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
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
	if len(p.Files) != 1 || len(p.Files[0].Add) != 3 {
		t.Fatalf("plan=%+v", p)
	}
	got := read(t, e.profile)
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
	matches, _ := filepath.Glob(filepath.Join(e.state, "backups", ".zprofile.*"))
	if len(matches) != 1 || read(t, matches[0]) != "export PATH=/x\n" {
		t.Errorf("backups=%v", matches)
	}
}

func TestApplyTwiceChangesNothing(t *testing.T) {
	e := setup(t, "-")
	e.apply(t)
	p, err := MakePlan(e.opts)
	if err != nil {
		t.Fatal(err)
	}
	if p.Changes() {
		t.Fatalf("second plan still has changes: %+v", p)
	}
}

func TestPlanSkipsWhatTheFileAlreadyExports(t *testing.T) {
	e := setup(t, "export HOMEBREW_NO_ANALYTICS=1\n  HOMEBREW_NO_AUTO_UPDATE=1\n# export HOMEBREW_NO_INSECURE_REDIRECT=1\n")
	p, err := MakePlan(e.opts)
	if err != nil {
		t.Fatal(err)
	}
	f := p.Files[0]
	if len(f.Skip) != 2 || len(f.Add) != 1 || f.Add[0].Name != "HOMEBREW_NO_INSECURE_REDIRECT" {
		t.Fatalf("skip=%v add=%v", f.Skip, f.Add)
	}
}

func TestUndoRestoresTheOriginalFileFromTheBackup(t *testing.T) {
	orig := "export PATH=/x\nalias ll='ls -l'"
	e := setup(t, orig) // no trailing newline on purpose: only a restore gets it back exactly
	e.apply(t)
	_, notes, err := Undo(e.opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 || !strings.Contains(notes[0], "restored") {
		t.Errorf("notes=%v", notes)
	}
	if got := read(t, e.profile); got != orig {
		t.Errorf("profile=%q, want %q", got, orig)
	}
	if v, _ := gitGet(e.repo); v != "" {
		t.Errorf("analytics should be unset again, got %q", v)
	}
	if _, err := os.Stat(filepath.Join(e.state, stateFile)); !os.IsNotExist(err) {
		t.Error("the record should be gone")
	}
}

func TestUndoRemovesAFileThatDidNotExist(t *testing.T) {
	e := setup(t, "-")
	e.apply(t)
	if _, err := os.Stat(e.profile); err != nil {
		t.Fatal(err)
	}
	if _, notes, err := Undo(e.opts); err != nil || !strings.Contains(notes[0], "did not exist before") {
		t.Fatalf("notes=%v err=%v", notes, err)
	}
	if _, err := os.Stat(e.profile); !os.IsNotExist(err) {
		t.Error("the file brew-guard created should be gone")
	}
}

func TestUndoAfterTheUserEditedTheFileOnlyTakesOutTheBlock(t *testing.T) {
	e := setup(t, "export PATH=/x\n")
	e.apply(t)
	write(t, e.profile, read(t, e.profile)+"export MINE=1\n", 0o640)
	backups, notes, err := Undo(e.opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, e.profile); got != "export PATH=/x\nexport MINE=1\n" {
		t.Errorf("profile=%q", got)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "edited after harden ran") {
		t.Errorf("notes=%v", notes)
	}
	if len(backups) != 1 || !strings.Contains(read(t, backups[0]), "MINE") {
		t.Errorf("the edited file should be backed up before it is cut: %v", backups)
	}
}

func TestUndoGoesBackToTheFirstBackupAfterASecondApply(t *testing.T) {
	orig := "export PATH=/x\n"
	e := setup(t, orig)
	e.apply(t)
	// An old block with other content gets rewritten by a second apply.
	write(t, e.profile, strings.Replace(read(t, e.profile), "HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_AUTO_UPDATE=0", 1), 0o640)
	e.apply(t)
	if !strings.Contains(read(t, e.profile), "HOMEBREW_NO_AUTO_UPDATE=1") {
		t.Fatal("the second apply should rewrite the block")
	}
	if _, _, err := Undo(e.opts); err != nil {
		t.Fatal(err)
	}
	if got := read(t, e.profile); got != orig {
		t.Errorf("profile=%q, want the original %q", got, orig)
	}
}

func TestUndoPutsBackAnalyticsThatWereFalse(t *testing.T) {
	e := setup(t, "-")
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
	e := setup(t, "-")
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
	e := setup(t, "-")
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
	e := setup(t, "-")
	if _, _, err := Undo(e.opts); !errors.Is(err, ErrNoRecord) {
		t.Fatalf("err=%v", err)
	}
}

func TestUndoKeepsWhatItCouldNotDoSoARerunFinishesTheJob(t *testing.T) {
	e := setup(t, "x\n")
	other := filepath.Join(e.dir, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	e.apply(t)
	// Make the first file impossible to write: a directory sits where it was.
	if err := os.Remove(e.profile); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(e.profile, "f"), "", 0o644)
	if _, _, err := Undo(e.opts); err == nil {
		t.Fatal("want an error")
	}
	if _, err := os.Stat(filepath.Join(e.state, stateFile)); err != nil {
		t.Error("the record must stay so undo can be run again")
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
		if got := read(t, e.profile); got != text {
			t.Errorf("%s: the file was changed", name)
		}
	}
}

func TestSymlinkedFileIsWrittenThroughTheLink(t *testing.T) {
	e := setup(t, "-")
	target := filepath.Join(t.TempDir(), "dotfiles-zprofile")
	write(t, target, "x\n", 0o644)
	link := filepath.Join(e.dir, ".zprofile-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	e.opts.Targets = []Target{{Path: link}}
	p, err := MakePlan(e.opts)
	if err != nil {
		t.Fatal(err)
	}
	if p.Files[0].Path != target {
		t.Fatalf("path=%s", p.Files[0].Path)
	}
	if _, err := Apply(e.opts, p); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the link was replaced by a file")
	}
	if !strings.Contains(read(t, target), begin) {
		t.Error("the target was not written")
	}
	if _, _, err := Undo(e.opts); err != nil {
		t.Fatal(err)
	}
	if got := read(t, target); got != "x\n" {
		t.Errorf("target=%q", got)
	}
}

func TestFileThatIsADirectoryIsRefused(t *testing.T) {
	e := setup(t, "-")
	e.opts.Targets = []Target{{Path: e.dir}}
	if _, err := MakePlan(e.opts); err == nil {
		t.Fatal("want an error")
	}
	e.opts.Targets = []Target{{Path: ""}}
	if _, err := MakePlan(e.opts); err == nil {
		t.Fatal("an empty path should be an error")
	}
}

func TestNoHomebrewCheckout(t *testing.T) {
	e := setup(t, "-")
	e.opts.Repos = []string{filepath.Join(t.TempDir(), "none")}
	p, err := MakePlan(e.opts)
	if err != nil {
		t.Fatal(err)
	}
	if p.Repo != "" || !p.Changes() {
		t.Fatalf("plan=%+v", p)
	}
	e.apply(t)
	if !strings.Contains(read(t, e.profile), begin) {
		t.Error("the file should still be written")
	}
}

func TestSeveralFilesAreAllWrittenAndAllRestored(t *testing.T) {
	e := setup(t, "a\n")
	rc := filepath.Join(e.dir, ".bashrc")
	write(t, rc, "b\n", 0o644)
	e.opts.Targets = []Target{{Path: e.profile}, {Path: rc}}
	e.apply(t)
	for _, f := range []string{e.profile, rc} {
		if !strings.Contains(read(t, f), begin) {
			t.Errorf("%s has no block", f)
		}
	}
	if _, _, err := Undo(e.opts); err != nil {
		t.Fatal(err)
	}
	if read(t, e.profile) != "a\n" || read(t, rc) != "b\n" {
		t.Errorf("profile=%q rc=%q", read(t, e.profile), read(t, rc))
	}
}

func TestFishFileUsesFishSyntaxAndIsCreatedAndRemoved(t *testing.T) {
	e := setup(t, "-")
	home := e.dir
	ts, err := Targets("/opt/homebrew/bin/fish", home, "")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".config", "fish", "conf.d", "brew-guard.fish")
	if len(ts) != 1 || ts[0].Path != want || ts[0].Syntax != Fish || !ts[0].MakeDir {
		t.Fatalf("targets=%+v", ts)
	}
	e.opts.Targets = ts
	e.apply(t)
	got := read(t, want)
	if !strings.Contains(got, "set -gx HOMEBREW_NO_ANALYTICS 1\n") || strings.Contains(got, "export ") {
		t.Errorf("fish file:\n%s", got)
	}
	// A rerun sees the variables it wrote and has nothing to do.
	if p, _ := MakePlan(e.opts); p.Changes() {
		t.Errorf("plan=%+v", p)
	}
	if _, _, err := Undo(e.opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Error("the fish file should be removed")
	}
}

func TestFishSkipsWhatItAlreadySets(t *testing.T) {
	e := setup(t, "-")
	f := filepath.Join(e.dir, "config.fish")
	write(t, f, "set -Ux HOMEBREW_NO_ANALYTICS 1\nset -gx HOMEBREW_NO_AUTO_UPDATE 1\nset HOMEBREW_NO_INSECURE_REDIRECT_X 1\n", 0o644)
	e.opts.Targets = []Target{{Path: f, Syntax: Fish}}
	p, err := MakePlan(e.opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Files[0].Skip) != 2 || len(p.Files[0].Add) != 1 {
		t.Fatalf("skip=%v add=%v", p.Files[0].Skip, p.Files[0].Add)
	}
}

func TestTargets(t *testing.T) {
	home := t.TempDir()
	path := func(n string) string { return filepath.Join(home, n) }

	got, err := Targets("/bin/zsh", home, "")
	if err != nil || len(got) != 1 || got[0].Path != path(".zprofile") {
		t.Errorf("zsh: %+v %v", got, err)
	}

	// bash with nothing there: .bash_profile
	got, _ = Targets("/bin/bash", home, "")
	if len(got) != 1 || got[0].Path != path(".bash_profile") {
		t.Errorf("bash empty: %+v", got)
	}
	// bash reads the first that exists, so a lone .profile must not be hidden
	write(t, path(".profile"), "p\n", 0o644)
	got, _ = Targets("/bin/bash", home, "")
	if len(got) != 1 || got[0].Path != path(".profile") {
		t.Errorf("bash with .profile: %+v", got)
	}
	// .bash_login comes before .profile; a .bashrc is added
	write(t, path(".bash_login"), "l\n", 0o644)
	write(t, path(".bashrc"), "r\n", 0o644)
	got, _ = Targets("/bin/bash", home, "")
	if len(got) != 2 || got[0].Path != path(".bash_login") || got[1].Path != path(".bashrc") {
		t.Errorf("bash with login and rc: %+v", got)
	}
	// .bash_profile wins over both
	write(t, path(".bash_profile"), "b\n", 0o644)
	got, _ = Targets("/bin/bash", home, "")
	if got[0].Path != path(".bash_profile") {
		t.Errorf("bash_profile should come first: %+v", got)
	}

	for _, sh := range []string{"/bin/sh", "/bin/dash", "/bin/ksh"} {
		got, err := Targets(sh, home, "")
		if err != nil || len(got) != 1 || got[0].Path != path(".profile") {
			t.Errorf("%s: %+v %v", sh, got, err)
		}
	}

	got, _ = Targets("fish", home, "/xdg")
	if got[0].Path != "/xdg/fish/conf.d/brew-guard.fish" {
		t.Errorf("fish with XDG_CONFIG_HOME: %+v", got)
	}

	for _, sh := range []string{"/usr/bin/nu", "/bin/tcsh", ""} {
		if _, err := Targets(sh, home, ""); err == nil {
			t.Errorf("%q should be refused", sh)
		}
	}
}

func TestSyntaxFor(t *testing.T) {
	if SyntaxFor("/usr/bin/fish") != Fish || SyntaxFor("/bin/zsh") != POSIX || SyntaxFor("") != POSIX {
		t.Error("wrong syntax")
	}
}

func TestUndoKeepsTheNewlineOfABlockWithoutSeparator(t *testing.T) {
	e := setup(t, "a\n"+begin+"\nexport HOMEBREW_NO_ANALYTICS=1\n"+end+"\nb\n")
	e.apply(t)
	// The file was changed by apply, then edited, so only the block is cut.
	write(t, e.profile, read(t, e.profile)+"c\n", 0o640)
	if _, _, err := Undo(e.opts); err != nil {
		t.Fatal(err)
	}
	if got := read(t, e.profile); got != "a\nb\nc\n" {
		t.Errorf("profile=%q", got)
	}
}

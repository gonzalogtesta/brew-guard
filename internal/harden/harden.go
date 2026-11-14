// Package harden applies the privacy settings that `brew-guard harden` prints:
// a marked block of exports in the shell's startup file, and Homebrew's
// analytics opt-out in its git config. Everything it changes is recorded and
// every file it touches is backed up first, so Undo can put it back exactly as
// it was. It never runs brew.
package harden

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gonzalogtesta/brew-guard/internal/doctor"
)

const (
	begin = "# >>> brew-guard harden >>>"
	end   = "# <<< brew-guard harden <<<"

	analyticsKey = "homebrew.analyticsdisabled"
	stateFile    = "harden.json"
)

// ErrNoRecord is returned by Undo when brew-guard has not changed anything.
var ErrNoRecord = errors.New("brew-guard has no record of changes made by harden")

// DefaultRepos are the places Homebrew keeps its git checkout.
var DefaultRepos = []string{"/opt/homebrew", "/usr/local/Homebrew"}

// Git is the git binary used to read and write Homebrew's config.
var Git = "/usr/bin/git"

// Syntax is how a shell exports a variable.
type Syntax int

const (
	POSIX Syntax = iota // export NAME=value (sh, bash, zsh, ksh)
	Fish                // set -gx NAME value
	Env                 // NAME=value, Homebrew's own brew.env file
)

// SyntaxFor returns the syntax a shell understands. Unknown shells get POSIX.
func SyntaxFor(shell string) Syntax {
	if filepath.Base(shell) == "fish" {
		return Fish
	}
	return POSIX
}

// ExportLine is the line that exports s in the given syntax.
func ExportLine(sy Syntax, s doctor.Setting) string {
	switch sy {
	case Fish:
		return fmt.Sprintf("set -gx %s %s", s.Name, s.Value)
	case Env:
		return fmt.Sprintf("%s=%s", s.Name, s.Value)
	}
	return fmt.Sprintf("export %s=%s", s.Name, s.Value)
}

// Target is one startup file that gets the block.
type Target struct {
	Path    string
	Syntax  Syntax
	MakeDir bool // create the parent folder when it is missing (fish's conf.d)
}

type Options struct {
	Targets  []Target
	Repos    []string // candidate Homebrew checkouts
	StateDir string   // where the record and the backups go
	Now      func() time.Time
}

// FilePlan is what Apply would do to one file.
type FilePlan struct {
	Target                  // Path is the real file, symlinks resolved
	Add    []doctor.Setting // exports missing from the file
	Skip   []string         // exports the file already has outside our block
	Block  bool             // the file already has our block
	Same   bool             // and it is exactly what Add would write
}

func (f FilePlan) changes() bool { return !f.Same && (len(f.Add) > 0 || f.Block) }

// Plan is what Apply would do.
type Plan struct {
	Files []FilePlan
	Repo  string // Homebrew checkout whose git config is used, "" if none found
	Off   bool   // analytics are already off in that checkout
}

// Changes reports whether Apply would change anything.
func (p Plan) Changes() bool {
	for _, f := range p.Files {
		if f.changes() {
			return true
		}
	}
	return p.Repo != "" && !p.Off
}

type fileRec struct {
	Path    string `json:"path"`
	Existed bool   `json:"existed"`          // the file was there before the first apply
	Backup  string `json:"backup,omitempty"` // copy of it as it was then
	Written string `json:"written"`          // sha256 of what brew-guard wrote last
}

type state struct {
	Files             []fileRec `json:"files"`
	Repo              string    `json:"repo,omitempty"`
	AnalyticsChanged  bool      `json:"analytics_changed"`
	PreviousAnalytics string    `json:"previous_analytics,omitempty"` // "" means it was not set
}

func (s *state) file(path string) *fileRec {
	for i := range s.Files {
		if s.Files[i].Path == path {
			return &s.Files[i]
		}
	}
	return nil
}

// Targets returns the startup files for a shell. Terminals only start a shell,
// so what matters is which files that shell reads:
//
//	zsh   ~/.zprofile (login shells, which macOS terminals start)
//	bash  the login file bash picks (~/.bash_profile, else ~/.bash_login, else
//	      ~/.profile) and ~/.bashrc if there is one, for terminals that start
//	      a shell that is not a login shell
//	fish  conf.d/brew-guard.fish, which every fish reads
//	sh, dash, ksh  ~/.profile
//
// Other shells (nushell, xonsh, csh, ...) need --profile, or the settings by hand.
func Targets(shell, home, xdgConfig string) ([]Target, error) {
	join := func(n string) string { return filepath.Join(home, n) }
	switch filepath.Base(shell) {
	case "zsh":
		return []Target{{Path: join(".zprofile")}}, nil
	case "bash":
		login := join(".bash_profile")
		// bash reads only the first of these that exists, so creating
		// .bash_profile next to a .profile would hide the .profile.
		for _, n := range []string{".bash_profile", ".bash_login", ".profile"} {
			if isFile(join(n)) {
				login = join(n)
				break
			}
		}
		out := []Target{{Path: login}}
		if rc := join(".bashrc"); rc != login && isFile(rc) {
			out = append(out, Target{Path: rc})
		}
		return out, nil
	case "sh", "dash", "ksh":
		return []Target{{Path: join(".profile")}}, nil
	case "fish":
		if xdgConfig == "" {
			xdgConfig = join(".config")
		}
		return []Target{{Path: filepath.Join(xdgConfig, "fish", "conf.d", "brew-guard.fish"), Syntax: Fish, MakeDir: true}}, nil
	}
	return nil, fmt.Errorf("don't know where shell %q reads its settings, name the file with --profile (it is written with export NAME=value lines) or add the settings by hand", shell)
}

// BrewEnv is Homebrew's own settings file. brew reads it on every run, so it
// works in any terminal and for any app that starts brew, whatever the shell
// reads. It supports plain NAME=value lines only: no variable expansion and no
// commands.
func BrewEnv(home, xdgConfig string) Target {
	p := filepath.Join(home, ".homebrew", "brew.env")
	if xdgConfig != "" {
		p = filepath.Join(xdgConfig, "homebrew", "brew.env")
	}
	return Target{Path: p, Syntax: Env, MakeDir: true}
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// MakePlan reads the files and Homebrew's git config. It writes nothing.
func MakePlan(o Options) (Plan, error) {
	var p Plan
	for _, t := range o.Targets {
		path, err := resolve(t.Path)
		if err != nil {
			return Plan{}, err
		}
		t.Path = path
		text, err := readIfExists(path)
		if err != nil {
			return Plan{}, err
		}
		before, after, found, err := splitBlock(text)
		if err != nil {
			return Plan{}, fmt.Errorf("%s: %w", path, err)
		}
		f := FilePlan{Target: t, Block: found}
		for _, s := range doctor.Recommended {
			if exports(before+after, t.Syntax, s.Name) {
				f.Skip = append(f.Skip, s.Name)
			} else {
				f.Add = append(f.Add, s)
			}
		}
		f.Same = found && withBlock(t.Syntax, before, after, true, f.Add) == text
		p.Files = append(p.Files, f)
	}
	p.Repo = findRepo(o.Repos)
	if p.Repo != "" {
		v, err := gitGet(p.Repo)
		if err != nil {
			return Plan{}, err
		}
		p.Off = v == "true"
	}
	return p, nil
}

// Apply makes the changes in p. Each file is backed up first, and what was
// changed is recorded before it returns, even if a later step fails.
func Apply(o Options, p Plan) (backups []string, err error) {
	st := state{}
	if old, err := loadState(o.StateDir); err == nil {
		st = old
	}
	var firstErr error
	fail := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}

	for _, f := range p.Files {
		if !f.changes() {
			continue
		}
		text, err := readIfExists(f.Path)
		if err != nil {
			fail(err)
			continue
		}
		before, after, found, err := splitBlock(text)
		if err != nil {
			fail(fmt.Errorf("%s: %w", f.Path, err))
			continue
		}
		existed := isFile(f.Path)
		backup, err := backupFile(o, f.Path, text)
		if err != nil {
			fail(err)
			continue
		}
		if backup != "" {
			backups = append(backups, backup)
		}
		out := withBlock(f.Syntax, before, after, found, f.Add)
		if f.MakeDir {
			if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
				fail(err)
				continue
			}
		}
		if err := writeAtomic(f.Path, []byte(out)); err != nil {
			fail(err)
			continue
		}
		// The record keeps the first backup: that is the file as it was before
		// brew-guard ever touched it, which is what Undo goes back to.
		if rec := st.file(f.Path); rec != nil {
			rec.Written = sum(out)
		} else {
			st.Files = append(st.Files, fileRec{Path: f.Path, Existed: existed, Backup: backup, Written: sum(out)})
		}
	}

	if p.Repo != "" && !p.Off {
		prev, err := gitGet(p.Repo)
		if err == nil {
			err = gitSet(p.Repo, "true")
		}
		if err != nil {
			fail(err)
		} else if !st.AnalyticsChanged {
			st.Repo, st.AnalyticsChanged, st.PreviousAnalytics = p.Repo, true, prev
		}
	}
	if len(st.Files) > 0 || st.AnalyticsChanged {
		if err := saveState(o.StateDir, st); err != nil {
			fail(err)
		}
	}
	return backups, firstErr
}

// Undo puts back what Apply changed. A file that is still exactly as brew-guard
// wrote it goes back to the backup of the original (or is removed if it did
// not exist). A file edited since only loses our block, so nothing of the
// user's is lost. Analytics are restored unless the user changed them since.
func Undo(o Options) (backups, notes []string, err error) {
	st, err := loadState(o.StateDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, ErrNoRecord
	}
	if err != nil {
		return nil, nil, err
	}
	var errs []error
	var left []fileRec
	for _, rec := range st.Files {
		b, note, err := undoFile(o, rec)
		if b != "" {
			backups = append(backups, b)
		}
		if note != "" {
			notes = append(notes, note)
		}
		if err != nil {
			errs = append(errs, err)
			left = append(left, rec)
		}
	}

	if st.AnalyticsChanged {
		cur, err := gitGet(st.Repo)
		switch {
		case err != nil:
			errs = append(errs, err)
		case cur != "true":
			notes = append(notes, "analytics in "+st.Repo+" were changed since, left as they are")
			st.AnalyticsChanged = false
		default:
			if st.PreviousAnalytics == "" {
				err = gitUnset(st.Repo)
			} else {
				err = gitSet(st.Repo, st.PreviousAnalytics)
			}
			if err != nil {
				errs = append(errs, err)
			} else {
				notes = append(notes, "analytics in "+st.Repo+" are back to how they were")
				st.AnalyticsChanged = false
			}
		}
	}

	if len(errs) > 0 {
		// Keep what is still to do, so running undo again finishes the job.
		st.Files = left
		if err := saveState(o.StateDir, st); err != nil {
			errs = append(errs, err)
		}
		return backups, notes, errors.Join(errs...)
	}
	if err := os.Remove(filepath.Join(o.StateDir, stateFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return backups, notes, err
	}
	return backups, notes, nil
}

func undoFile(o Options, rec fileRec) (backup, note string, err error) {
	text, err := readIfExists(rec.Path)
	if err != nil {
		return "", "", err
	}
	if !isFile(rec.Path) {
		return "", rec.Path + " is gone already, nothing to undo there", nil
	}
	if sum(text) == rec.Written {
		if !rec.Existed {
			if err := os.Remove(rec.Path); err != nil {
				return "", "", err
			}
			return "", "removed " + rec.Path + ", it did not exist before", nil
		}
		if orig, err := os.ReadFile(rec.Backup); err == nil {
			if err := writeAtomic(rec.Path, orig); err != nil {
				return "", "", err
			}
			return "", "restored " + rec.Path + " from " + rec.Backup, nil
		}
		note = "the backup " + rec.Backup + " can't be read. "
	}

	// Edited since, or no usable backup: take out our block and nothing else.
	before, after, found, err := splitBlock(text)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", rec.Path, err)
	}
	if !found {
		return "", note + rec.Path + " has no brew-guard block, nothing to remove there", nil
	}
	if backup, err = backupFile(o, rec.Path, text); err != nil {
		return "", "", err
	}
	if err := writeAtomic(rec.Path, []byte(withBlock(POSIX, before, after, true, nil))); err != nil {
		return backup, "", err
	}
	return backup, note + rec.Path + " was edited after harden ran, so only the brew-guard block was removed. The original is still in " + rec.Backup, nil
}

// splitBlock returns the text around our block. found is false when there is
// none. A block that is not closed, or that appears twice, is an error: it
// would be a guess where to cut.
func splitBlock(text string) (before, after string, found bool, err error) {
	lines := strings.SplitAfter(text, "\n")
	start, stop := -1, -1
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case begin:
			if start >= 0 {
				return "", "", false, errors.New("more than one brew-guard block, remove the extra ones by hand")
			}
			start = i
		case end:
			if start < 0 || stop >= 0 {
				return "", "", false, errors.New("the end marker of the brew-guard block has no start, fix it by hand")
			}
			stop = i
		}
	}
	if start < 0 && stop < 0 {
		return text, "", false, nil
	}
	if start < 0 || stop < 0 {
		return "", "", false, errors.New("the brew-guard block is not closed, fix it by hand")
	}
	return strings.Join(lines[:start], ""), strings.Join(lines[stop+1:], ""), true, nil
}

// withBlock puts the block for add between before and after. With add empty
// the block is removed, together with the blank line that was put before it.
func withBlock(sy Syntax, before, after string, found bool, add []doctor.Setting) string {
	if len(add) == 0 {
		if strings.HasSuffix(before, "\n\n") { // the blank separator line put before the block
			before = before[:len(before)-1]
		}
		return before + after
	}
	var b strings.Builder
	b.WriteString(begin + "\n")
	for _, s := range add {
		fmt.Fprintf(&b, "# %s\n%s\n", s.Why, ExportLine(sy, s))
	}
	b.WriteString(end + "\n")
	if found {
		return before + b.String() + after
	}
	out := before
	if out != "" {
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += "\n"
	}
	return out + b.String() + after
}

func exports(text string, sy Syntax, name string) bool {
	n := regexp.QuoteMeta(name)
	re := `(?m)^[ \t]*(export[ \t]+)?` + n + `=`
	if sy == Fish {
		re = `(?m)^[ \t]*set[ \t]+(-[A-Za-z]+[ \t]+)*` + n + `([ \t]|$)`
	}
	return regexp.MustCompile(re).MatchString(text)
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// resolve follows a symlinked file (dotfile managers use them) and insists on
// a regular file, or on a path that does not exist yet.
func resolve(path string) (string, error) {
	if path == "" {
		return "", errors.New("no profile given")
	}
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", fmt.Errorf("%s: %w", path, err)
		}
		path = real
	}
	if fi, err := os.Stat(path); err == nil && !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	return path, nil
}

func readIfExists(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

// writeAtomic writes to a temporary file next to path and renames it, keeping
// the mode of the file it replaces.
func writeAtomic(path string, data []byte) error {
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".brew-guard-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// backupFile copies the file as it is now into the state folder. It returns ""
// when the file does not exist, since there is nothing to keep. The name has
// a short hash of the path in it, so two files with the same name (.profile in
// two places) don't overwrite each other.
func backupFile(o Options, path, text string) (string, error) {
	if !isFile(path) {
		return "", nil
	}
	dir := filepath.Join(o.StateDir, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tag := sum(path)[:8]
	base := filepath.Join(dir, filepath.Base(path)+"."+tag+"."+o.Now().UTC().Format("20060102-150405"))
	// Never overwrite an earlier backup: the record may point to it.
	for i := 0; i < 1000; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s.%d", base, i)
		}
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.WriteString(text); err != nil {
			f.Close()
			return "", err
		}
		return name, f.Close()
	}
	return "", errors.New("too many backups with the same name")
}

func loadState(dir string) (state, error) {
	var st state
	b, err := os.ReadFile(filepath.Join(dir, stateFile))
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("%s: %w", stateFile, err)
	}
	return st, nil
}

func saveState(dir string, st state) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, stateFile), b, 0o600)
}

// findRepo returns the first candidate that has a plain .git/config file.
func findRepo(repos []string) string {
	for _, r := range repos {
		if isFile(filepath.Join(r, ".git", "config")) {
			return r
		}
	}
	return ""
}

// git runs `git config --file <repo>/.git/config ...`. Naming the file keeps
// git from reading any other config, and from running anything the checkout's
// config could point to.
func git(repo string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	full := append([]string{"config", "--file", filepath.Join(repo, ".git", "config")}, args...)
	cmd := exec.CommandContext(ctx, Git, full...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"}
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func gitGet(repo string) (string, error) {
	v, err := git(repo, "--get", analyticsKey)
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return "", nil // not set
	}
	if err != nil {
		return "", fmt.Errorf("reading the git config of %s: %w", repo, err)
	}
	return v, nil
}

func gitSet(repo, value string) error {
	if _, err := git(repo, "--replace-all", analyticsKey, value); err != nil {
		return fmt.Errorf("writing the git config of %s: %w", repo, err)
	}
	return nil
}

func gitUnset(repo string) error {
	_, err := git(repo, "--unset-all", analyticsKey)
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 5 {
		return nil // already gone
	}
	if err != nil {
		return fmt.Errorf("writing the git config of %s: %w", repo, err)
	}
	return nil
}

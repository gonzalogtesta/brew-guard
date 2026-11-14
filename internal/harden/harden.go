// Package harden applies the privacy settings that `brew-guard harden` prints:
// a marked block of exports in the shell profile, and Homebrew's analytics
// opt-out in its git config. Everything it changes is recorded, so Undo can put
// it back. It never runs brew.
package harden

import (
	"context"
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

type Options struct {
	Profile  string   // shell profile to edit
	Repos    []string // candidate Homebrew checkouts
	StateDir string   // where the record and the backups go
	Now      func() time.Time
}

// Plan is what Apply would do.
type Plan struct {
	Profile string           // the file that will be written, symlinks resolved
	Add     []doctor.Setting // exports missing from the profile
	Skip    []string         // exports the profile already has outside our block
	Repo    string           // Homebrew checkout whose git config is used, "" if none found
	Off     bool             // analytics are already off in that checkout
	Block   bool             // the profile already has our block
	Same    bool             // and it is exactly what Add would write
}

// Changes reports whether Apply would change anything.
func (p Plan) Changes() bool {
	profile := !p.Same && (len(p.Add) > 0 || p.Block)
	return profile || (p.Repo != "" && !p.Off)
}

type state struct {
	Profile           string `json:"profile"`
	Block             bool   `json:"block"`
	Repo              string `json:"repo,omitempty"`
	AnalyticsChanged  bool   `json:"analytics_changed"`
	PreviousAnalytics string `json:"previous_analytics,omitempty"` // "" means it was not set
}

// DefaultProfile picks the login profile of the shell: ~/.zprofile for zsh,
// ~/.bash_profile for bash. Anything else must be named by the user.
func DefaultProfile(shell, home string) (string, error) {
	switch filepath.Base(shell) {
	case "zsh":
		return filepath.Join(home, ".zprofile"), nil
	case "bash":
		return filepath.Join(home, ".bash_profile"), nil
	}
	return "", fmt.Errorf("don't know the profile of shell %q, give it with --profile", shell)
}

// MakePlan reads the profile and Homebrew's git config. It writes nothing.
func MakePlan(o Options) (Plan, error) {
	path, err := resolve(o.Profile)
	if err != nil {
		return Plan{}, err
	}
	text, err := readIfExists(path)
	if err != nil {
		return Plan{}, err
	}
	before, after, found, err := splitBlock(text)
	if err != nil {
		return Plan{}, fmt.Errorf("%s: %w", path, err)
	}
	p := Plan{Profile: path, Block: found}
	outside := before + after
	for _, s := range doctor.Recommended {
		if exports(outside, s.Name) {
			p.Skip = append(p.Skip, s.Name)
		} else {
			p.Add = append(p.Add, s)
		}
	}
	p.Same = found && withBlock(before, after, true, p.Add) == text
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

// Apply makes the changes in p. The profile is backed up first, and what was
// changed is recorded before it returns, even if a later step fails.
func Apply(o Options, p Plan) (backup string, err error) {
	text, err := readIfExists(p.Profile)
	if err != nil {
		return "", err
	}
	before, after, found, err := splitBlock(text)
	if err != nil {
		return "", fmt.Errorf("%s: %w", p.Profile, err)
	}
	st := state{Profile: p.Profile}
	if old, err := loadState(o.StateDir); err == nil && old.Profile == p.Profile {
		st = old
	}
	st.Block = len(p.Add) > 0

	if !p.Same && (len(p.Add) > 0 || found) {
		if backup, err = backupFile(o, p.Profile, text); err != nil {
			return "", err
		}
		out := withBlock(before, after, found, p.Add)
		if err := writeAtomic(p.Profile, []byte(out)); err != nil {
			return backup, err
		}
	}

	var gerr error
	if p.Repo != "" && !p.Off {
		prev, err := gitGet(p.Repo)
		if err == nil {
			err = gitSet(p.Repo, "true")
		}
		if err != nil {
			gerr = err
		} else {
			st.Repo, st.AnalyticsChanged, st.PreviousAnalytics = p.Repo, true, prev
		}
	}
	if err := saveState(o.StateDir, st); err != nil {
		return backup, err
	}
	return backup, gerr
}

// Undo removes our block from the profile and puts analytics back as they were.
// Settings the user changed since are left alone.
func Undo(o Options) (backup string, notes []string, err error) {
	st, err := loadState(o.StateDir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil, ErrNoRecord
	}
	if err != nil {
		return "", nil, err
	}
	text, err := readIfExists(st.Profile)
	if err != nil {
		return "", nil, err
	}
	before, after, found, err := splitBlock(text)
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", st.Profile, err)
	}
	if found {
		if backup, err = backupFile(o, st.Profile, text); err != nil {
			return "", nil, err
		}
		if err := writeAtomic(st.Profile, []byte(withBlock(before, after, true, nil))); err != nil {
			return backup, nil, err
		}
		notes = append(notes, "removed the block from "+st.Profile)
	} else if st.Block {
		notes = append(notes, "the block is no longer in "+st.Profile+", nothing to remove there")
	}

	if st.AnalyticsChanged {
		cur, err := gitGet(st.Repo)
		switch {
		case err != nil:
			return backup, notes, err
		case cur != "true":
			notes = append(notes, "analytics in "+st.Repo+" were changed since, left as they are")
		default:
			if st.PreviousAnalytics == "" {
				err = gitUnset(st.Repo)
			} else {
				err = gitSet(st.Repo, st.PreviousAnalytics)
			}
			if err != nil {
				return backup, notes, err
			}
			notes = append(notes, "analytics in "+st.Repo+" are back to how they were")
		}
	}
	if err := os.Remove(filepath.Join(o.StateDir, stateFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return backup, notes, err
	}
	return backup, notes, nil
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
func withBlock(before, after string, found bool, add []doctor.Setting) string {
	var b strings.Builder
	if len(add) == 0 {
		if strings.HasSuffix(before, "\n\n") { // the blank separator line put before the block
			before = before[:len(before)-1]
		}
		return before + after
	}
	b.WriteString(begin + "\n")
	for _, s := range add {
		fmt.Fprintf(&b, "# %s\nexport %s=%s\n", s.Why, s.Name, s.Value)
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

func exports(text, name string) bool {
	return regexp.MustCompile(`(?m)^[ \t]*(export[ \t]+)?` + regexp.QuoteMeta(name) + `=`).MatchString(text)
}

// resolve follows a symlinked profile (dotfile managers use them) and insists
// on a regular file, or on a path that does not exist yet.
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

func backupFile(o Options, path, text string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", nil // nothing to back up
	}
	dir := filepath.Join(o.StateDir, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := filepath.Join(dir, filepath.Base(path)+"."+o.Now().UTC().Format("20060102-150405"))
	return name, os.WriteFile(name, []byte(text), 0o600)
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
		if fi, err := os.Stat(filepath.Join(r, ".git", "config")); err == nil && fi.Mode().IsRegular() {
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

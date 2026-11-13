package repair

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OfficialURL is the only place a repair fetches from. The remote configured in
// the checkout is never used, since it is part of what may be damaged.
const OfficialURL = "https://github.com/Homebrew/brew"

// Candidates are the places a Homebrew checkout normally lives.
var Candidates = []string{"/opt/homebrew", "/usr/local/Homebrew", "/home/linuxbrew/.linuxbrew/Homebrew"}

// Exec runs a program and returns its standard output.
type Exec func(ctx context.Context, name string, args ...string) ([]byte, error)

const gitBin = "/usr/bin/git"

// gitArgs turns off everything in a checkout's config that could run a
// program or leave GitHub: hooks, file system monitor, credential helpers,
// and every protocol except https.
func gitArgs(dir string, args ...string) []string {
	base := []string{"-C", dir,
		"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "core.pager=cat",
		"-c", "credential.helper=", "-c", "protocol.allow=never", "-c", "protocol.https.allow=always"}
	return append(base, args...)
}

func git(ctx context.Context, ex Exec, dir string, args ...string) (string, error) {
	b, err := ex(ctx, gitBin, gitArgs(dir, args...)...)
	return strings.TrimSpace(string(b)), err
}

func nonEmpty(s string) []string {
	var out []string
	for _, f := range strings.Split(s, "\n") {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func lines(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Split(s, "\n"))
}

// Report says what state a checkout is in.
type Report struct {
	Dir       string
	Head      string
	HeadFull  string
	Origin    string
	Deleted   int      // tracked files that are missing
	Modified  int      // tracked files that differ from the commit
	Untracked int      // extra files under Library/Homebrew
	Changed   []string // the first few of them, "deleted path", "modified path", "extra path"
	Hooks     []string // git hooks that are installed, a Homebrew checkout has none
	Unsafe    []string
}

const maxListed = 20

// Healthy is true when the checkout matches its commit and its config is plain.
func (r Report) Healthy() bool {
	return r.Deleted == 0 && r.Modified == 0 && r.Untracked == 0 && len(r.Unsafe) == 0 && len(r.Hooks) == 0
}

// Diagnose reads the state of the checkout in dir. It changes nothing.
// config is the content of dir/.git/config.
func Diagnose(ctx context.Context, ex Exec, dir string, config []byte) (Report, error) {
	r := Report{Dir: dir, Unsafe: UnsafeConfig(config), Hooks: InstalledHooks(dir)}
	if len(r.Unsafe) > 0 {
		// Do not run git at all in a checkout whose config can run programs.
		return r, nil
	}
	var err error
	if r.HeadFull, err = git(ctx, ex, dir, "rev-parse", "HEAD"); err != nil {
		return r, fmt.Errorf("%s does not look like a git checkout: %w", dir, err)
	}
	r.Head = r.HeadFull
	if len(r.Head) > 10 {
		r.Head = r.Head[:10]
	}
	r.Origin, _ = git(ctx, ex, dir, "config", "--local", "--get", "remote.origin.url")
	deleted, err := git(ctx, ex, dir, "ls-files", "--deleted")
	if err != nil {
		return r, err
	}
	// git lists a deleted file under --modified too; count it once.
	gone := map[string]bool{}
	for _, f := range strings.Split(deleted, "\n") {
		gone[f] = true
	}
	modified, err := git(ctx, ex, dir, "ls-files", "--modified")
	if err != nil {
		return r, err
	}
	var changed []string
	for _, f := range strings.Split(modified, "\n") {
		if f != "" && !gone[f] {
			changed = append(changed, f)
		}
	}
	extra, err := git(ctx, ex, dir, "ls-files", "--others", "--exclude-standard", "--", "Library/Homebrew")
	if err != nil {
		return r, err
	}
	for _, q := range []struct {
		label string
		files []string
		count *int
	}{
		{"deleted", nonEmpty(deleted), &r.Deleted},
		{"modified", changed, &r.Modified},
		{"extra", nonEmpty(extra), &r.Untracked},
	} {
		*q.count = len(q.files)
		for _, f := range q.files {
			if len(r.Changed) < maxListed {
				r.Changed = append(r.Changed, q.label+" "+f)
			}
		}
	}
	return r, nil
}

// Options controls Apply.
type Options struct {
	MinAgeDays int
	Now        func() time.Time
	BackupDir  string // where the backup archive goes
}

// Apply backs the checkout up and puts it on the newest release that is old
// enough. Steps, in order, and it stops at the first one that fails:
//  1. back up the whole folder
//  2. fetch the release tags from the official URL
//  3. choose the release
//  4. check it out, replacing every tracked file
//  5. remove untracked files under Library/Homebrew only
func Apply(ctx context.Context, ex Exec, rep Report, opt Options, w io.Writer) (Release, error) {
	if len(rep.Unsafe) > 0 {
		return Release{}, fmt.Errorf("git config of %s has settings that can run programs (%s), not touching it",
			rep.Dir, strings.Join(rep.Unsafe, ", "))
	}
	if err := os.MkdirAll(opt.BackupDir, 0o700); err != nil {
		return Release{}, err
	}
	stamp := opt.Now().UTC().Format("20060102-150405")
	backup := filepath.Join(opt.BackupDir, "homebrew-"+stamp+".tar.gz")
	fmt.Fprintln(w, "backing up", rep.Dir, "to", backup)
	if _, err := ex(ctx, "/usr/bin/tar", "-czf", backup, "-C", filepath.Dir(rep.Dir), filepath.Base(rep.Dir)); err != nil {
		return Release{}, fmt.Errorf("backup failed, nothing was changed: %w", err)
	}

	fmt.Fprintln(w, "fetching release tags from", OfficialURL)
	if _, err := git(ctx, ex, rep.Dir, "fetch", "--force", "--no-tags", OfficialURL, "+refs/tags/*:refs/tags/*"); err != nil {
		return Release{}, fmt.Errorf("fetch failed: %w", err)
	}
	names, err := git(ctx, ex, rep.Dir, "tag", "--list")
	if err != nil {
		return Release{}, err
	}
	var releases []Release
	for _, n := range SortedTags(strings.Split(names, "\n")) {
		date, err := git(ctx, ex, rep.Dir, "log", "-1", "--format=%cI", "refs/tags/"+n+"^{commit}")
		if err != nil {
			return Release{}, err
		}
		t, err := time.Parse(time.RFC3339, date)
		if err != nil {
			return Release{}, fmt.Errorf("tag %s: %w", n, err)
		}
		releases = append(releases, Release{Name: n, Time: t})
		if len(releases) == 12 { // enough to find one that is old enough
			break
		}
	}
	rel, err := Pick(releases, opt.Now(), opt.MinAgeDays)
	if err != nil {
		return Release{}, err
	}
	fmt.Fprintf(w, "using release %s (%s)\n", rel.Name, rel.Time.Format("2006-01-02"))

	if rep.Origin != OfficialURL {
		if _, err := git(ctx, ex, rep.Dir, "remote", "set-url", "origin", OfficialURL); err != nil {
			return Release{}, err
		}
	}
	if _, err := git(ctx, ex, rep.Dir, "checkout", "--force", "-B", "stable", "refs/tags/"+rel.Name); err != nil {
		return Release{}, fmt.Errorf("checkout failed: %w", err)
	}
	if _, err := git(ctx, ex, rep.Dir, "clean", "-fd", "--", "Library/Homebrew"); err != nil {
		return Release{}, fmt.Errorf("clean failed: %w", err)
	}
	return rel, nil
}

// ReadConfig returns the content of dir/.git/config. A checkout where .git is
// not a plain folder is refused.
func ReadConfig(dir string) ([]byte, error) {
	st, err := os.Lstat(filepath.Join(dir, ".git"))
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, errors.New(".git is not a plain folder")
	}
	return os.ReadFile(filepath.Join(dir, ".git", "config"))
}

// OfficialRelease asks GitHub which release tag points at the checkout's
// commit. It returns "" when none does: then the checkout is on a commit that
// Homebrew never released (a development install, or a rewritten history).
// It only reads, nothing in the checkout changes.
func OfficialRelease(ctx context.Context, ex Exec, rep Report) (string, error) {
	if rep.HeadFull == "" {
		return "", errors.New("no commit to compare")
	}
	b, err := ex(ctx, gitBin, "ls-remote", "--tags", OfficialURL)
	if err != nil {
		return "", fmt.Errorf("could not ask GitHub for the release tags: %w", err)
	}
	return matchTag(string(b), rep.HeadFull), nil
}

// matchTag finds the plain x.y.z tag whose commit is head in the output of
// git ls-remote --tags. An annotated tag lists its commit on a ^{} line.
func matchTag(lsRemote, head string) string {
	for _, l := range strings.Split(lsRemote, "\n") {
		sha, ref, ok := strings.Cut(strings.TrimSpace(l), "\t")
		if !ok || sha != head || !strings.HasPrefix(ref, "refs/tags/") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(ref, "refs/tags/"), "^{}")
		if _, ok := semver(name); ok {
			return name
		}
	}
	return ""
}

// InstalledHooks lists the git hooks in dir/.git/hooks that are not the
// *.sample files git creates. A Homebrew checkout has none.
func InstalledHooks(dir string) []string {
	entries, err := os.ReadDir(filepath.Join(dir, ".git", "hooks"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sample") {
			out = append(out, e.Name())
		}
	}
	return out
}

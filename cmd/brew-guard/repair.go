package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gonzalogtesta/brew-guard/internal/brewdata"
	"github.com/gonzalogtesta/brew-guard/internal/repair"
)

// Variables so tests can substitute the outside world.
var (
	repairExec  repair.Exec = defaultRepairExec
	repairStdin io.Reader   = os.Stdin
	repairNow               = time.Now
	verifyBrew              = defaultVerifyBrew
)

// defaultRepairExec runs a program with a short, fixed environment: no
// credentials, no prompts, no git config outside the checkout.
func defaultRepairExec(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "HOME=" + os.Getenv("HOME"), "LANG=C",
		"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return out.Bytes(), fmt.Errorf("%s: %w: %s", filepath.Base(name), err, msg)
	}
	return out.Bytes(), nil
}

func defaultVerifyBrew(ctx context.Context) (string, error) {
	brew, err := brewdata.FindBrew(brewCandidates)
	if err != nil {
		return "", err
	}
	b, err := brewdata.ExecRunner{Brew: brew, Parent: os.Environ()}.Run(ctx, "--version")
	return strings.TrimSpace(string(b)), err
}

// repairCmd checks a Homebrew checkout for damage or tampering and, with
// --apply, puts it back on an official release. Without --apply it only reads.
func repairCmd(args []string, stdout, stderr io.Writer, env func(string) string) int {
	fs := flag.NewFlagSet("repair", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dirFlag := fs.String("dir", "", "Homebrew checkout (default: the first one found)")
	offline := fs.Bool("offline", false, "do not ask GitHub which release the commit is")
	apply := fs.Bool("apply", false, "repair it: back up, then reset to an official release")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return exitError
	}
	p, _, err := loadPolicy("", env)
	if err != nil {
		fmt.Fprintln(stderr, "repair:", err)
		return exitError
	}
	dir, err := findCheckout(*dirFlag)
	if err != nil {
		fmt.Fprintln(stderr, "repair:", err)
		return exitError
	}
	ctx := context.Background()
	cfg, err := repair.ReadConfig(dir)
	if err != nil {
		fmt.Fprintln(stderr, "repair:", err)
		return exitError
	}
	rep, err := repair.Diagnose(ctx, repairExec, dir, cfg)
	if err != nil {
		fmt.Fprintln(stderr, "repair:", err)
		return exitError
	}

	official, known := "", false
	if !*offline && len(rep.Unsafe) == 0 {
		if official, err = repair.OfficialRelease(ctx, repairExec, rep); err != nil {
			fmt.Fprintln(stderr, "warning:", err)
		} else {
			known = true
		}
	}
	problems := printRepairReport(stdout, rep, official, known)
	if problems == 0 {
		fmt.Fprintln(stdout, "nothing to repair")
		return exitOK
	}
	if len(rep.Unsafe) > 0 {
		fmt.Fprintln(stdout, "\nThe git config of this checkout can run programs. brew-guard did not run git in it and will not repair it.")
		fmt.Fprintln(stdout, "Treat the Homebrew folder as altered: remove those settings by hand, or reinstall Homebrew from its official instructions.")
		return exitBlock
	}
	backups := filepath.Join(xdgDir(env, "XDG_STATE_HOME", ".local/state"), "brew-guard", "backups")
	if !*apply {
		fmt.Fprintf(stdout, "\nWith --apply it would: back up %s to %s, fetch the release tags from %s,\n", dir, backups, repair.OfficialURL)
		fmt.Fprintf(stdout, "reset the checkout to the newest release that is at least %d days old, and remove extra files under Library/Homebrew only.\n", p.MinAgeDays)
		fmt.Fprintln(stdout, "Installed packages (Cellar, Caskroom) and your taps are not touched.")
		return exitBlock
	}
	if !*yes && !confirm(stdout, fmt.Sprintf("\nBack up and repair %s now?", dir)) {
		fmt.Fprintln(stdout, "cancelled, nothing was changed")
		return exitBlock
	}
	rel, err := repair.Apply(ctx, repairExec, rep, repair.Options{MinAgeDays: p.MinAgeDays, Now: repairNow, BackupDir: backups}, stdout)
	if err != nil {
		fmt.Fprintln(stderr, "repair:", err)
		return exitError
	}
	v, err := verifyBrew(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "repair: reset to %s, but brew still does not start: %v\n", rel.Name, err)
		return exitError
	}
	fmt.Fprintf(stdout, "done, brew now runs: %s\n", firstLine(v))
	return exitOK
}

// printRepairReport prints one line per check and returns how many failed.
func printRepairReport(w io.Writer, r repair.Report, official string, known bool) int {
	bad := 0
	line := func(ok bool, format string, a ...any) {
		mark := "ok  "
		if !ok {
			mark, bad = "FAIL", bad+1
		}
		fmt.Fprintf(w, "%s %s\n", mark, fmt.Sprintf(format, a...))
	}
	fmt.Fprintf(w, "Homebrew checkout: %s (commit %s)\n", r.Dir, r.Head)
	if len(r.Unsafe) > 0 {
		line(false, "git config has settings that can run programs: %s", strings.Join(r.Unsafe, ", "))
		return bad
	}
	line(true, "git config has no settings that can run programs")
	line(len(r.Hooks) == 0, "no git hooks installed (%d found%s)", len(r.Hooks), listed(r.Hooks))
	line(r.Deleted == 0 && r.Modified == 0, "tracked files match the commit (%d deleted, %d modified)", r.Deleted, r.Modified)
	line(r.Untracked == 0, "no extra files under Library/Homebrew (%d found)", r.Untracked)
	line(r.Origin == repair.OfficialURL, "origin is the official repository (%q)", r.Origin)
	switch {
	case !known:
		fmt.Fprintln(w, "skip commit compared with the official release tags (offline or GitHub not reachable)")
	case official != "":
		line(true, "commit is the official release %s", official)
	default:
		line(false, "commit is not an official release (a development install, or a rewritten history)")
	}
	for _, c := range r.Changed {
		fmt.Fprintln(w, "       ", c)
	}
	if n := r.Deleted + r.Modified + r.Untracked; n > len(r.Changed) {
		fmt.Fprintf(w, "        ... and %d more\n", n-len(r.Changed))
	}
	return bad
}

func listed(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return ": " + strings.Join(names, ", ")
}

func findCheckout(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	for _, c := range repair.Candidates {
		if st, err := os.Stat(filepath.Join(c, ".git")); err == nil && st.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("no Homebrew checkout found in %s, give one with --dir", strings.Join(repair.Candidates, ", "))
}

func confirm(w io.Writer, question string) bool {
	fmt.Fprintf(w, "%s [yes/no] ", question)
	line, _ := bufio.NewReader(repairStdin).ReadString('\n')
	return strings.TrimSpace(line) == "yes"
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/gonzalogtesta/brew-guard/internal/doctor"
	"github.com/gonzalogtesta/brew-guard/internal/harden"
)

// hardenRepos is a variable so tests don't touch a real Homebrew.
var hardenRepos = harden.DefaultRepos

// hardenCmd prints the settings to add to a shell's startup file. With --apply
// it writes them (and Homebrew's analytics opt-out), with --undo it puts back
// what --apply changed. Without a flag it only prints.
func hardenCmd(args []string, stdout, stderr io.Writer, env func(string) string) int {
	fs := flag.NewFlagSet("harden", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apply := fs.Bool("apply", false, "write the settings to the shell's startup file and Homebrew's git config")
	undo := fs.Bool("undo", false, "put back what --apply changed")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	shell := fs.String("shell", "", "shell to set up: zsh, bash, fish, sh, ksh (default: the one in $SHELL)")
	profile := fs.String("profile", "", "write only this file, instead of Homebrew's brew.env and the shell's startup files")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		if err == nil {
			fmt.Fprintln(stderr, "harden: unexpected argument", fs.Arg(0))
		}
		return exitError
	}
	if *apply && *undo {
		fmt.Fprintln(stderr, "harden: --apply and --undo can't be used together")
		return exitError
	}
	sh := *shell
	if sh == "" {
		sh = env("SHELL")
	}
	if !*apply && !*undo {
		printSettings(stdout, sh, env)
		return 0
	}

	o := harden.Options{
		Repos:    hardenRepos,
		StateDir: filepath.Join(xdgDir(env, "XDG_STATE_HOME", ".local/state"), "brew-guard"),
		Now:      repairNow,
	}
	if *undo {
		return hardenUndo(o, *yes, stdout, stderr)
	}
	if *profile != "" {
		// Naming a file means that file and nothing else.
		o.Targets = []harden.Target{{Path: *profile, Syntax: harden.SyntaxFor(sh)}}
	} else {
		// Homebrew's own file first: it covers every terminal and app. The
		// shell's files come on top, for tools that read the environment.
		o.Targets = []harden.Target{harden.BrewEnv(env("HOME"), env("XDG_CONFIG_HOME"))}
		t, err := harden.Targets(sh, env("HOME"), env("XDG_CONFIG_HOME"))
		if err != nil {
			fmt.Fprintln(stdout, "note:", err)
			fmt.Fprintln(stdout, "note: only Homebrew's own settings file is written")
		}
		o.Targets = append(o.Targets, t...)
	}
	return hardenApply(o, *yes, stdout, stderr)
}

func printSettings(stdout io.Writer, shell string, env func(string) string) {
	sy := harden.SyntaxFor(shell)
	fmt.Fprintln(stdout, "# Privacy and security settings for Homebrew.")
	fmt.Fprintln(stdout, "# Review them, then add what you want to your shell's startup file.")
	for _, s := range doctor.Recommended {
		note := ""
		if env(s.Name) != "" {
			note = "  # already set"
		}
		fmt.Fprintf(stdout, "\n# %s\n%s%s\n", s.Why, harden.ExportLine(sy, s), note)
	}
	fmt.Fprintln(stdout, "\n# Homebrew analytics are also stored in its git config. To turn them off there, run:")
	fmt.Fprintln(stdout, "#   brew analytics off")
	fmt.Fprintln(stdout, "\n# To make these changes for you, with a backup and a way back: brew-guard harden --apply")
}

func hardenApply(o harden.Options, yes bool, stdout, stderr io.Writer) int {
	p, err := harden.MakePlan(o)
	if err != nil {
		fmt.Fprintln(stderr, "harden:", err)
		return exitError
	}
	for _, f := range p.Files {
		fmt.Fprintf(stdout, "file: %s\n", f.Path)
		for _, n := range f.Skip {
			fmt.Fprintf(stdout, "  %s is already set there, left as it is\n", n)
		}
		for _, s := range f.Add {
			fmt.Fprintf(stdout, "  will add: %s\n", harden.ExportLine(f.Syntax, s))
		}
		if f.Same {
			fmt.Fprintln(stdout, "  the brew-guard block is already there and up to date")
		}
	}
	switch {
	case p.Repo == "":
		fmt.Fprintln(stdout, "Homebrew checkout: not found, analytics in its git config are not touched")
	case p.Off:
		fmt.Fprintf(stdout, "Homebrew checkout: %s, analytics are already off\n", p.Repo)
	default:
		fmt.Fprintf(stdout, "Homebrew checkout: %s, will turn analytics off in its git config\n", p.Repo)
	}
	if !p.Changes() {
		fmt.Fprintln(stdout, "\nnothing to change")
		return 0
	}
	if !yes && !confirm(stdout, "\nMake these changes? Every file is backed up first.") {
		fmt.Fprintln(stdout, "cancelled, nothing was changed")
		return exitBlock
	}
	backups, err := harden.Apply(o, p)
	for _, b := range backups {
		fmt.Fprintf(stdout, "backup: %s\n", b)
	}
	if err != nil {
		fmt.Fprintln(stderr, "harden:", err)
		return exitError
	}
	fmt.Fprintln(stdout, "done. Open a new terminal for the settings to apply, then run: brew-guard doctor")
	fmt.Fprintln(stdout, "to put everything back as it was: brew-guard harden --undo")
	return 0
}

func hardenUndo(o harden.Options, yes bool, stdout, stderr io.Writer) int {
	if !yes && !confirm(stdout, "Put the startup files and the analytics setting back as they were before harden --apply?") {
		fmt.Fprintln(stdout, "cancelled, nothing was changed")
		return exitBlock
	}
	backups, notes, err := harden.Undo(o)
	if errors.Is(err, harden.ErrNoRecord) {
		fmt.Fprintln(stdout, "nothing to undo:", err)
		return 0
	}
	for _, n := range notes {
		fmt.Fprintln(stdout, n)
	}
	for _, b := range backups {
		fmt.Fprintf(stdout, "backup: %s\n", b)
	}
	if err != nil {
		fmt.Fprintln(stderr, "harden:", err)
		return exitError
	}
	fmt.Fprintln(stdout, "done. Open a new terminal for it to take effect.")
	return 0
}

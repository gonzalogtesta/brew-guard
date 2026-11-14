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

// hardenCmd prints the settings to add to a shell profile. With --apply it
// writes them (and Homebrew's analytics opt-out), with --undo it puts back what
// --apply changed. Without a flag it only prints.
func hardenCmd(args []string, stdout, stderr io.Writer, env func(string) string) int {
	fs := flag.NewFlagSet("harden", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apply := fs.Bool("apply", false, "write the settings to the shell profile and Homebrew's git config")
	undo := fs.Bool("undo", false, "put back what --apply changed")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	profile := fs.String("profile", "", "shell profile to edit (default: ~/.zprofile for zsh, ~/.bash_profile for bash)")
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
	if !*apply && !*undo {
		printSettings(stdout, env)
		return 0
	}

	o := harden.Options{
		Profile:  *profile,
		Repos:    hardenRepos,
		StateDir: filepath.Join(xdgDir(env, "XDG_STATE_HOME", ".local/state"), "brew-guard"),
		Now:      repairNow,
	}
	if *undo {
		return hardenUndo(o, *yes, stdout, stderr)
	}
	if o.Profile == "" {
		p, err := harden.DefaultProfile(env("SHELL"), env("HOME"))
		if err != nil {
			fmt.Fprintln(stderr, "harden:", err)
			return exitError
		}
		o.Profile = p
	}
	return hardenApply(o, *yes, stdout, stderr)
}

func printSettings(stdout io.Writer, env func(string) string) {
	fmt.Fprintln(stdout, "# Privacy and security settings for Homebrew.")
	fmt.Fprintln(stdout, "# Review them, then add what you want to your shell profile (for example ~/.zprofile).")
	for _, s := range doctor.Recommended {
		note := ""
		if env(s.Name) != "" {
			note = "  # already set"
		}
		fmt.Fprintf(stdout, "\n# %s\nexport %s=%s%s\n", s.Why, s.Name, s.Value, note)
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
	fmt.Fprintf(stdout, "profile: %s\n", p.Profile)
	for _, n := range p.Skip {
		fmt.Fprintf(stdout, "  %s is already exported there, left as it is\n", n)
	}
	for _, s := range p.Add {
		fmt.Fprintf(stdout, "  will export %s=%s\n", s.Name, s.Value)
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
	if !yes && !confirm(stdout, "\nMake these changes?") {
		fmt.Fprintln(stdout, "cancelled, nothing was changed")
		return exitBlock
	}
	backup, err := harden.Apply(o, p)
	if backup != "" {
		fmt.Fprintf(stdout, "backup of the profile: %s\n", backup)
	}
	if err != nil {
		fmt.Fprintln(stderr, "harden:", err)
		return exitError
	}
	fmt.Fprintln(stdout, "done. Open a new terminal for the settings to apply, then run: brew-guard doctor")
	fmt.Fprintln(stdout, "to put everything back: brew-guard harden --undo")
	return 0
}

func hardenUndo(o harden.Options, yes bool, stdout, stderr io.Writer) int {
	if !yes && !confirm(stdout, "Remove what brew-guard harden added and restore the analytics setting?") {
		fmt.Fprintln(stdout, "cancelled, nothing was changed")
		return exitBlock
	}
	backup, notes, err := harden.Undo(o)
	if errors.Is(err, harden.ErrNoRecord) {
		fmt.Fprintln(stdout, "nothing to undo:", err)
		return 0
	}
	for _, n := range notes {
		fmt.Fprintln(stdout, n)
	}
	if backup != "" {
		fmt.Fprintf(stdout, "backup of the profile: %s\n", backup)
	}
	if err != nil {
		fmt.Fprintln(stderr, "harden:", err)
		return exitError
	}
	fmt.Fprintln(stdout, "done. Open a new terminal for it to take effect.")
	return 0
}

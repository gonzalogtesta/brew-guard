package main

import (
	"fmt"
	"io"

	"github.com/gonzalogtesta/brew-guard/internal/doctor"
)

// hardenCmd prints the settings to add to a shell profile. It only prints:
// it never edits a file or runs brew.
func hardenCmd(stdout io.Writer, env func(string) string) int {
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
	return 0
}

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// executablePath is a variable so tests can substitute the program's path.
var executablePath = os.Executable

// shimCmd installs, removes or reports the link that makes the shell run
// brew-guard when someone types brew. It never edits a shell profile and
// never touches Homebrew's own files.
func shimCmd(args []string, stdout, stderr io.Writer, env func(string) string) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: brew-guard shim install|uninstall|status [--dir directory]")
		return exitError
	}
	fs := flag.NewFlagSet("shim", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", filepath.Join(env("HOME"), ".local", "bin"), "directory for the brew link")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		return exitError
	}
	self, err := executablePath()
	if err == nil {
		self, err = filepath.EvalSymlinks(self)
	}
	if err != nil {
		fmt.Fprintln(stderr, "shim:", err)
		return exitError
	}
	link := filepath.Join(*dir, "brew")

	switch args[0] {
	case "install":
		return shimInstall(link, self, stdout, stderr, env)
	case "uninstall":
		return shimUninstall(link, self, stdout, stderr)
	case "status":
		return shimStatus(link, self, stdout, env)
	}
	fmt.Fprintln(stderr, "usage: brew-guard shim install|uninstall|status [--dir directory]")
	return exitError
}

// pointsAtSelf reports whether link is a symlink that resolves to self.
func pointsAtSelf(link, self string) bool {
	st, err := os.Lstat(link)
	if err != nil || st.Mode()&os.ModeSymlink == 0 {
		return false
	}
	target, err := filepath.EvalSymlinks(link)
	return err == nil && target == self
}

func shimInstall(link, self string, stdout, stderr io.Writer, env func(string) string) int {
	if pointsAtSelf(link, self) {
		fmt.Fprintln(stdout, "already installed:", link)
	} else {
		if _, err := os.Lstat(link); err == nil {
			fmt.Fprintf(stderr, "shim: %s already exists and is not a link to brew-guard, not touching it\n", link)
			return exitError
		} else if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(stderr, "shim:", err)
			return exitError
		}
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			fmt.Fprintln(stderr, "shim:", err)
			return exitError
		}
		if err := os.Symlink(self, link); err != nil {
			fmt.Fprintln(stderr, "shim:", err)
			return exitError
		}
		fmt.Fprintf(stdout, "created %s -> %s\n", link, self)
	}
	if first := firstInPath(env("PATH"), "brew"); first != link {
		fmt.Fprintf(stdout, "\nThe shell does not use it yet. Put %s before the Homebrew directory in PATH,\n", filepath.Dir(link))
		fmt.Fprintln(stdout, "for example in ~/.zprofile (brew-guard does not edit your profile):")
		fmt.Fprintf(stdout, "  export PATH=%q:$PATH\n", filepath.Dir(link))
	}
	return exitOK
}

func shimUninstall(link, self string, stdout, stderr io.Writer) int {
	if _, err := os.Lstat(link); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(stdout, "nothing to remove:", link)
		return exitOK
	}
	if !pointsAtSelf(link, self) {
		fmt.Fprintf(stderr, "shim: %s is not a link to brew-guard, not removing it\n", link)
		return exitError
	}
	if err := os.Remove(link); err != nil {
		fmt.Fprintln(stderr, "shim:", err)
		return exitError
	}
	fmt.Fprintln(stdout, "removed", link)
	return exitOK
}

func shimStatus(link, self string, stdout io.Writer, env func(string) string) int {
	code := exitOK
	if pointsAtSelf(link, self) {
		fmt.Fprintln(stdout, "ok   the brew link exists:", link)
	} else {
		fmt.Fprintln(stdout, "FAIL no brew link to brew-guard at", link)
		code = exitBlock
	}
	first := firstInPath(env("PATH"), "brew")
	switch {
	case first == "":
		fmt.Fprintln(stdout, "FAIL no brew in PATH")
		code = exitBlock
	case first == link:
		fmt.Fprintln(stdout, "ok   typing brew runs brew-guard")
	default:
		fmt.Fprintf(stdout, "FAIL typing brew runs %s, not brew-guard\n", first)
		code = exitBlock
	}
	return code
}

// firstInPath returns the first executable called name in the PATH string,
// without resolving links, or "" if there is none.
func firstInPath(path, name string) string {
	for _, d := range strings.Split(path, ":") {
		if d == "" {
			continue
		}
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0 {
			return p
		}
	}
	return ""
}

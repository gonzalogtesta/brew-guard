package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shimEnv sets up a fake brew-guard binary and a directory for the link.
func shimEnv(t *testing.T) (env map[string]string, dir, self string) {
	t.Helper()
	root := t.TempDir()
	self = filepath.Join(root, "bin", "brew-guard")
	writeFile(t, self, "#!/bin/sh\n")
	if err := os.Chmod(self, 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(self)
	if err != nil {
		t.Fatal(err)
	}
	old := executablePath
	executablePath = func() (string, error) { return resolved, nil }
	t.Cleanup(func() { executablePath = old })
	dir = filepath.Join(root, "home", ".local", "bin")
	return map[string]string{"HOME": filepath.Join(root, "home"), "PATH": "/usr/bin:/bin"}, dir, resolved
}

func TestShimInstallCreatesLink(t *testing.T) {
	env, dir, self := shimEnv(t)
	code, out, errs := runCmd(env, "shim", "install")
	if code != 0 || errs != "" {
		t.Fatalf("code=%d err=%s out=%s", code, errs, out)
	}
	target, err := os.Readlink(filepath.Join(dir, "brew"))
	if err != nil || target != self {
		t.Fatalf("link: %v %v", target, err)
	}
	if !strings.Contains(out, "PATH") {
		t.Fatalf("it must say how to put the link first in PATH:\n%s", out)
	}
}

func TestShimInstallTwiceAndPathOk(t *testing.T) {
	env, dir, _ := shimEnv(t)
	runCmd(env, "shim", "install")
	env["PATH"] = dir + ":/usr/bin"
	code, out, _ := runCmd(env, "shim", "install")
	if code != 0 || !strings.Contains(out, "already installed") || strings.Contains(out, "export PATH") {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestShimInstallRefusesToOverwrite(t *testing.T) {
	env, dir, _ := shimEnv(t)
	writeFile(t, filepath.Join(dir, "brew"), "#!/bin/sh\necho mine\n")
	code, _, errs := runCmd(env, "shim", "install")
	if code != 2 || !strings.Contains(errs, "not touching it") {
		t.Fatalf("code=%d err=%s", code, errs)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "brew")); !strings.Contains(string(b), "mine") {
		t.Fatal("the existing file must be left alone")
	}
}

func TestShimUninstall(t *testing.T) {
	env, dir, _ := shimEnv(t)
	if code, out, _ := runCmd(env, "shim", "uninstall"); code != 0 || !strings.Contains(out, "nothing to remove") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	runCmd(env, "shim", "install")
	if code, _, _ := runCmd(env, "shim", "uninstall"); code != 0 {
		t.Fatal("uninstall failed")
	}
	if _, err := os.Lstat(filepath.Join(dir, "brew")); !os.IsNotExist(err) {
		t.Fatal("link must be gone")
	}
}

func TestShimUninstallKeepsForeignFile(t *testing.T) {
	env, dir, _ := shimEnv(t)
	writeFile(t, filepath.Join(dir, "brew"), "x")
	if code, _, errs := runCmd(env, "shim", "uninstall"); code != 2 || !strings.Contains(errs, "not removing") {
		t.Fatalf("code=%d err=%s", code, errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "brew")); err != nil {
		t.Fatal("a file that is not our link must stay")
	}
}

func TestShimStatus(t *testing.T) {
	env, dir, _ := shimEnv(t)
	if code, out, _ := runCmd(env, "shim", "status"); code != 1 || !strings.Contains(out, "FAIL no brew link") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	runCmd(env, "shim", "install")
	if code, out, _ := runCmd(env, "shim", "status"); code != 1 || !strings.Contains(out, "FAIL no brew in PATH") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	other := t.TempDir()
	writeFile(t, filepath.Join(other, "brew"), "#!/bin/sh\n")
	os.Chmod(filepath.Join(other, "brew"), 0o755)
	env["PATH"] = other + ":" + dir
	if code, out, _ := runCmd(env, "shim", "status"); code != 1 || !strings.Contains(out, "runs "+filepath.Join(other, "brew")) {
		t.Fatalf("code=%d out=%s", code, out)
	}
	env["PATH"] = dir + ":" + other
	if code, out, _ := runCmd(env, "shim", "status"); code != 0 || !strings.Contains(out, "typing brew runs brew-guard") {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestShimUsage(t *testing.T) {
	env, _, _ := shimEnv(t)
	for _, a := range [][]string{{"shim"}, {"shim", "nope"}, {"shim", "install", "extra"}, {"shim", "install", "--nope"}} {
		if code, _, _ := runCmd(env, a...); code != 2 {
			t.Errorf("%v: code=%d", a, code)
		}
	}
}

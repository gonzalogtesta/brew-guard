package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func mapEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func useRepos(t *testing.T, repos ...string) {
	t.Helper()
	old := brewRepos
	brewRepos = repos
	t.Cleanup(func() { brewRepos = old })
}

func useGit(t *testing.T, bin string) {
	t.Helper()
	old := gitBin
	gitBin = bin
	t.Cleanup(func() { gitBin = old })
}

// requireGit skips the test unless /usr/bin/git can run.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/usr/bin/git"); err != nil {
		t.Skip("/usr/bin/git not available")
	}
	if err := exec.Command("/usr/bin/git", "--version").Run(); err != nil {
		t.Skip("/usr/bin/git not usable: ", err)
	}
}

func findCheck(t *testing.T, checks []Check, name string) Check {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q in %+v", name, checks)
	return Check{}
}

func TestRootCheck(t *testing.T) {
	useRepos(t, filepath.Join(t.TempDir(), "absent"))
	for _, c := range []struct {
		euid int
		ok   bool
	}{{0, false}, {501, true}} {
		got := findCheck(t, Run(mapEnv(nil), c.euid), "not running as root")
		if got.OK != c.ok {
			t.Errorf("euid %d: OK=%v want %v", c.euid, got.OK, c.ok)
		}
	}
}

func TestEnvVars(t *testing.T) {
	useRepos(t, filepath.Join(t.TempDir(), "absent"))
	all := []string{"HOMEBREW_NO_ANALYTICS", "HOMEBREW_NO_AUTO_UPDATE", "HOMEBREW_NO_INSECURE_REDIRECT"}
	cases := []struct {
		name string
		env  map[string]string
		ok   map[string]bool
	}{
		{"all set", map[string]string{all[0]: "1", all[1]: "1", all[2]: "1"},
			map[string]bool{all[0]: true, all[1]: true, all[2]: true}},
		{"none set", nil, map[string]bool{all[0]: false, all[1]: false, all[2]: false}},
		{"empty counts as unset", map[string]string{all[0]: ""}, map[string]bool{all[0]: false}},
		{"only one set", map[string]string{all[1]: "yes"}, map[string]bool{all[0]: false, all[1]: true, all[2]: false}},
	}
	for _, c := range cases {
		checks := Run(mapEnv(c.env), 501)
		for v, want := range c.ok {
			got := findCheck(t, checks, v+" set")
			if got.OK != want {
				t.Errorf("%s: %s OK=%v want %v", c.name, v, got.OK, want)
			}
			if want && got.Detail != "" {
				t.Errorf("%s: %s has detail %q when set", c.name, v, got.Detail)
			}
			if !want && got.Detail != "export "+v+"=1" {
				t.Errorf("%s: %s detail %q", c.name, v, got.Detail)
			}
		}
	}
}

func TestAnalyticsRepoMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	useRepos(t, missing)
	c := findCheck(t, Run(mapEnv(nil), 501), "Homebrew repository found")
	if c.OK || !strings.Contains(c.Detail, missing) {
		t.Fatalf("got %+v", c)
	}
}

func TestAnalyticsGitMissing(t *testing.T) {
	repo := t.TempDir()
	useRepos(t, repo)
	useGit(t, filepath.Join(t.TempDir(), "no-such-git"))
	c := findCheck(t, Run(mapEnv(nil), 501), "analytics disabled in "+repo)
	if c.OK || c.Detail != "run: brew analytics off" {
		t.Fatalf("got %+v", c)
	}
}

func TestAnalyticsWithGitRepo(t *testing.T) {
	requireGit(t)
	repo := t.TempDir()
	if out, err := exec.Command("/usr/bin/git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	useRepos(t, filepath.Join(t.TempDir(), "absent"), repo)
	name := "analytics disabled in " + repo
	setCfg := func(v string) {
		t.Helper()
		if out, err := exec.Command("/usr/bin/git", "-C", repo, "config", "homebrew.analyticsdisabled", v).CombinedOutput(); err != nil {
			t.Fatalf("git config: %v %s", err, out)
		}
	}

	// Not set: analytics are enabled.
	if c := findCheck(t, Run(mapEnv(nil), 501), name); c.OK {
		t.Fatalf("unset should fail: %+v", c)
	}
	setCfg("false")
	if c := findCheck(t, Run(mapEnv(nil), 501), name); c.OK {
		t.Fatalf("false should fail: %+v", c)
	}
	setCfg("true")
	if c := findCheck(t, Run(mapEnv(nil), 501), name); !c.OK {
		t.Fatalf("true should pass: %+v", c)
	}
}

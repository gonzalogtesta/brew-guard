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

func TestAnalyticsExported(t *testing.T) {
	old := brewRepos
	brewRepos = []string{filepath.Join(t.TempDir(), "missing")}
	defer func() { brewRepos = old }()
	if c := Analytics(); c.OK || !strings.Contains(c.Name, "Homebrew repository") {
		t.Fatalf("got %+v", c)
	}
}

func TestTapTrustCheck(t *testing.T) {
	const name = "tap trust is not disabled"
	if c := findCheck(t, Run(mapEnv(nil), 501), name); !c.OK || c.Detail != "" {
		t.Fatalf("unset must pass: %+v", c)
	}
	c := findCheck(t, Run(mapEnv(map[string]string{"HOMEBREW_NO_REQUIRE_TAP_TRUST": "1"}), 501), name)
	if c.OK || !strings.Contains(c.Detail, "HOMEBREW_NO_REQUIRE_TAP_TRUST") {
		t.Fatalf("set must fail with a hint: %+v", c)
	}
}

func init() { systemEnvFiles = nil } // tests must not read the machine's real files

func TestBrewEnvFileCountsAsSet(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".homebrew"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# comment\nHOMEBREW_NO_AUTO_UPDATE=1\nHOMEBREW_NO_ANALYTICS=\nHOMEBREW_NO_INSECURE_REDIRECT=\"1\"\n"
	if err := os.WriteFile(filepath.Join(home, ".homebrew", "brew.env"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	env := mapEnv(map[string]string{"HOME": home})
	for name, want := range map[string]bool{"HOMEBREW_NO_AUTO_UPDATE": true, "HOMEBREW_NO_ANALYTICS": false, "HOMEBREW_NO_INSECURE_REDIRECT": true} {
		if got := inBrewEnv(env, name); got != want {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}
	c := findCheck(t, Run(env, 1000), "HOMEBREW_NO_AUTO_UPDATE set")
	if !c.OK {
		t.Errorf("doctor should accept a setting from brew.env: %+v", c)
	}
}

func TestBrewEnvFileFollowsXDGConfigHome(t *testing.T) {
	x := t.TempDir()
	if err := os.MkdirAll(filepath.Join(x, "homebrew"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(x, "homebrew", "brew.env"), []byte("HOMEBREW_NO_AUTO_UPDATE=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !inBrewEnv(mapEnv(map[string]string{"XDG_CONFIG_HOME": x, "HOME": t.TempDir()}), "HOMEBREW_NO_AUTO_UPDATE") {
		t.Error("XDG_CONFIG_HOME/homebrew/brew.env should be read")
	}
}

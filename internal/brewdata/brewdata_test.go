package brewdata

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gonzalogtesta/brew-guard/internal/dates"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseInfo(t *testing.T) {
	pkgs, err := ParseInfo(fixture(t, "info_v2.json"))
	if err != nil || len(pkgs) != 2 {
		t.Fatalf("%v %v", pkgs, err)
	}
	jq, cask := pkgs[0], pkgs[1]
	if jq.Name != "jq" || jq.Kind != dates.Formula || jq.Tap != "homebrew/core" || jq.Version != "1.8.2" ||
		jq.SourcePath != "Formula/j/jq.rb" || jq.Checksum != "abc123" || !strings.HasPrefix(jq.URL, "https://") {
		t.Fatalf("formula: %+v", jq)
	}
	if cask.Name != "iterm2" || cask.Kind != dates.Cask || cask.Version != "3.6.0" || !cask.Disabled || cask.Checksum != "def456" {
		t.Fatalf("cask: %+v", cask)
	}
}

func TestParseInfoErrors(t *testing.T) {
	if _, err := ParseInfo([]byte("not json")); err == nil {
		t.Fatal("expected error")
	}
	if p, err := ParseInfo([]byte(`{}`)); err != nil || len(p) != 0 {
		t.Fatalf("empty: %v %v", p, err)
	}
}

func TestParseOutdated(t *testing.T) {
	o, err := ParseOutdated(fixture(t, "outdated_v2.json"))
	if err != nil || len(o) != 3 {
		t.Fatalf("%v %v", o, err)
	}
	if o[0].Name != "jq" || o[0].Current != "1.8.2" || o[0].Kind != dates.Formula || o[0].Installed[0] != "1.8.1" {
		t.Fatalf("jq: %+v", o[0])
	}
	if len(o[1].Installed) != 1 || o[1].Installed[0] != "3.5.0" || o[1].Kind != dates.Cask {
		t.Fatalf("string installed_versions: %+v", o[1])
	}
	if len(o[2].Installed) != 2 {
		t.Fatalf("list installed_versions: %+v", o[2])
	}
}

func TestParseOutdatedBadShape(t *testing.T) {
	for _, in := range []string{`{"casks":[{"name":"x","installed_versions":5}]}`, `[`} {
		if _, err := ParseOutdated([]byte(in)); err == nil {
			t.Errorf("%s: expected error", in)
		}
	}
}

type fake struct {
	args []string
	out  []byte
	err  error
}

func (f *fake) Run(_ context.Context, args ...string) ([]byte, error) {
	f.args = args
	return f.out, f.err
}

func TestInfoValidatesNames(t *testing.T) {
	f := &fake{out: fixture(t, "info_v2.json")}
	for _, bad := range []string{"", "-v", "--json=v1", "a b", "../x", "evil/tap/foo", "a;b", "$(x)"} {
		if _, err := Info(context.Background(), f, "jq", bad); err == nil {
			t.Errorf("name %q should be rejected", bad)
		}
	}
	if f.args != nil {
		t.Fatal("brew must not run when a name is invalid")
	}
	if _, err := Info(context.Background(), f, "jq", "python@3.13", "gcc-12", "c++"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.args, " ") != "info --json=v2 jq python@3.13 gcc-12 c++" {
		t.Fatalf("args: %v", f.args)
	}
	if _, err := Info(context.Background(), f); err == nil {
		t.Fatal("no names must be an error")
	}
}

func TestInfoErrors(t *testing.T) {
	if _, err := Info(context.Background(), &fake{err: errors.New("boom")}, "jq"); err == nil {
		t.Fatal("runner error must surface")
	}
	if _, err := Info(context.Background(), &fake{out: []byte(`{}`)}, "jq"); err == nil {
		t.Fatal("empty result must be an error")
	}
}

func TestListOutdated(t *testing.T) {
	f := &fake{out: fixture(t, "outdated_v2.json")}
	o, err := ListOutdated(context.Background(), f)
	if err != nil || len(o) != 3 || strings.Join(f.args, " ") != "outdated --json=v2" {
		t.Fatalf("%v %v %v", o, err, f.args)
	}
}

func TestEnvDropsDangerousVariables(t *testing.T) {
	parent := []string{
		"HOME=/Users/x", "PATH=/tmp/evil", "DYLD_INSERT_LIBRARIES=/tmp/x.dylib", "LD_PRELOAD=/tmp/x",
		"HOMEBREW_BREW_GIT_REMOTE=https://evil", "HOMEBREW_API_DOMAIN=https://evil", "HOMEBREW_NO_AUTO_UPDATE=0",
		"GITHUB_TOKEN=secret", "LANG=en_US.UTF-8",
	}
	env := strings.Join(Env(parent), "\n")
	for _, bad := range []string{"evil", "DYLD_", "LD_PRELOAD", "GITHUB_TOKEN", "NO_AUTO_UPDATE=0"} {
		if strings.Contains(env, bad) {
			t.Errorf("environment still contains %q:\n%s", bad, env)
		}
	}
	for _, want := range []string{"HOME=/Users/x", "LANG=en_US.UTF-8", "PATH=" + safePath,
		"HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ANALYTICS=1", "HOMEBREW_NO_INSECURE_REDIRECT=1"} {
		if !strings.Contains(env, want) {
			t.Errorf("environment lacks %q", want)
		}
	}
}

func script(t *testing.T, dir, name, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFindBrew(t *testing.T) {
	dir := t.TempDir()
	good := script(t, dir, "brew", "#!/bin/sh\n", 0o755)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	got, err := FindBrew([]string{filepath.Join(dir, "missing"), link})
	want, _ := filepath.EvalSymlinks(good)
	if err != nil || got != want {
		t.Fatalf("got %q %v want %q", got, err, want)
	}
}

func TestFindBrewRejects(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"group-writable": script(t, dir, "gw", "#!/bin/sh\n", 0o775),
		"world-writable": script(t, dir, "ww", "#!/bin/sh\n", 0o777),
		"not-executable": script(t, dir, "ne", "#!/bin/sh\n", 0o644),
	}
	for name, p := range cases {
		if got, err := FindBrew([]string{p}); err == nil {
			t.Errorf("%s accepted: %q", name, got)
		}
	}
	if _, err := FindBrew([]string{dir}); err == nil {
		t.Error("a directory must be rejected")
	}
	if _, err := FindBrew([]string{filepath.Join(dir, "nope")}); err == nil {
		t.Error("missing brew must be an error")
	}
}

func TestExecRunnerUsesCleanEnvironment(t *testing.T) {
	dir := t.TempDir()
	brew := script(t, dir, "brew", "#!/bin/sh\nenv\n", 0o755)
	out, err := ExecRunner{Brew: brew, Parent: []string{"HOME=/h", "HOMEBREW_API_DOMAIN=https://evil", "DYLD_X=1"}}.
		Run(context.Background(), "info")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "evil") || strings.Contains(s, "DYLD_X") || !strings.Contains(s, "HOMEBREW_NO_ANALYTICS=1") {
		t.Fatalf("environment not cleaned:\n%s", s)
	}
}

func TestExecRunnerFailureAndLimits(t *testing.T) {
	dir := t.TempDir()
	fail := script(t, dir, "fail", "#!/bin/sh\necho oops >&2\nexit 3\n", 0o755)
	if _, err := (ExecRunner{Brew: fail}).Run(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "oops") {
		t.Fatalf("got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (ExecRunner{Brew: script(t, dir, "ok", "#!/bin/sh\necho hi\n", 0o755)}).Run(ctx, "x"); err == nil {
		t.Fatal("cancelled context must fail")
	}
}

func TestLimitedBuffer(t *testing.T) {
	l := &limited{max: 4}
	l.Write([]byte("abc"))
	l.Write([]byte("def"))
	if !l.over || l.String() != "abc" {
		t.Fatalf("%q over=%v", l.String(), l.over)
	}
}

func TestSignalsFormula(t *testing.T) {
	in := `{"formulae":[
	  {"name":"a","post_install_defined":true,"service":{"run":["a"]},"caveats":"read me","versions":{"stable":"1"}},
	  {"name":"b","post_install_defined":false,"service":null,"caveats":null,"versions":{"stable":"1"}},
	  {"name":"c","service":{},"caveats":"  ","versions":{"stable":"1"}}]}`
	pkgs, err := ParseInfo([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := pkgs[0], pkgs[1], pkgs[2]
	if !a.PostInstall || !a.HasService || !a.HasCaveats {
		t.Fatalf("a: %+v", a)
	}
	if strings.Join(a.Signals(), "|") != "runs a post-install step|installs a background service" {
		t.Fatalf("signals: %v", a.Signals())
	}
	if b.PostInstall || b.HasService || b.HasCaveats || len(b.Signals()) != 0 {
		t.Fatalf("b: %+v", b)
	}
	if c.HasService || c.HasCaveats {
		t.Fatalf("empty service and blank caveats must not count: %+v", c)
	}
}

func TestSignalsCask(t *testing.T) {
	in := `{"casks":[{"token":"x","sha256":"no_check","version":"1","artifacts":[
	  {"app":["X.app"]},{"pkg":["X.pkg"]},{"postflight_steps":[]},{"uninstall":[{"launchctl":"x"}]},{"app":["Y.app"]}]}]}`
	pkgs, err := ParseInfo([]byte(in))
	if err != nil || len(pkgs) != 1 {
		t.Fatal(err)
	}
	p := pkgs[0]
	if strings.Join(p.ArtifactKinds, ",") != "app,pkg,postflight_steps,uninstall" || !p.NoCheck {
		t.Fatalf("%+v", p)
	}
	s := strings.Join(p.Signals(), "|")
	for _, want := range []string{"no_check", "installer (pkg)", "install scripts (postflight_steps)"} {
		if !strings.Contains(s, want) {
			t.Errorf("signals lack %q: %s", want, s)
		}
	}
	if strings.Contains(s, "uninstall") || strings.Contains(s, "app") {
		t.Errorf("plain artifacts must not raise signals: %s", s)
	}
}

func TestSignalsIgnoreMalformedArtifacts(t *testing.T) {
	if _, err := ParseInfo([]byte(`{"casks":[{"token":"x","artifacts":"nope"}]}`)); err == nil {
		t.Fatal("artifacts of the wrong type must be an error, not silently ignored")
	}
}

package brewdata

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type stubRunner struct {
	out  string
	err  error
	args []string
}

func (s *stubRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	s.args = args
	return []byte(s.out), s.err
}

func TestParseDryRunRecorded(t *testing.T) {
	items, err := ParseDryRun(fixture(t, "recorded_dryrun_jq.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 14 {
		t.Fatalf("want 14 items, got %d: %+v", len(items), items)
	}
	if items[0] != (SetItem{Name: "jq", Version: "1.8.2"}) {
		t.Fatalf("first: %+v", items[0])
	}
	last := items[13]
	if last.Name != "oniguruma" || last.Version != "6.9.10" || last.Via != "jq" {
		t.Fatalf("last: %+v", last)
	}
	if items[1].Name != "linux-headers@6.8" || items[1].Via != "jq" {
		t.Fatalf("second: %+v", items[1])
	}
}

func TestParseDryRunNothingToInstall(t *testing.T) {
	items, err := ParseDryRun([]byte("Warning: jq 1.8.2 is already installed and up-to-date.\n"))
	if err != nil || len(items) != 0 {
		t.Fatalf("%v %v", items, err)
	}
}

func TestParseDryRunTwoRequested(t *testing.T) {
	in := "==> Would install 2 formulae:\njq 1.8.2\nwget 1.25.0\n==> Would install 1 dependency for wget:\nopenssl@4 4.0.0\n"
	items, err := ParseDryRun([]byte(in))
	if err != nil || len(items) != 3 || items[2].Via != "wget" || items[0].Via != "" {
		t.Fatalf("%+v %v", items, err)
	}
}

func TestParseDryRunFailsClosed(t *testing.T) {
	for name, in := range map[string]string{
		"count mismatch":  "==> Would install 2 formulae:\njq 1.8.2\n",
		"bad count":       "==> Would install many formulae:\njq 1.8.2\n",
		"bad name":        "==> Would install 1 formula:\n;rm 1\n",
		"too many fields": "==> Would install 1 formula:\njq 1.8.2 extra\n",
		"bad via":         "==> Would install 1 dependency for $(x):\njq 1\n",
	} {
		if _, err := ParseDryRun([]byte(in)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestInstallSetRunsDryRun(t *testing.T) {
	r := &stubRunner{out: "==> Would install 1 formula:\njq 1.8.2\n"}
	items, err := InstallSet(context.Background(), r, []string{"--cask"}, "jq")
	if err != nil || len(items) != 1 {
		t.Fatalf("%v %v", items, err)
	}
	if got := strings.Join(r.args, " "); got != "install --dry-run --cask jq" {
		t.Fatalf("args: %s", got)
	}
}

func TestInstallSetErrors(t *testing.T) {
	if _, err := InstallSet(context.Background(), &stubRunner{}, nil); err == nil {
		t.Fatal("no names must fail")
	}
	r := &stubRunner{}
	if _, err := InstallSet(context.Background(), r, nil, "--force"); err == nil || r.args != nil {
		t.Fatal("option-looking names must be refused before brew runs")
	}
	if _, err := InstallSet(context.Background(), &stubRunner{err: errors.New("boom")}, nil, "jq"); err == nil {
		t.Fatal("brew failure must be returned")
	}
}

func TestInstallSetRefusesOddFlags(t *testing.T) {
	r := &stubRunner{}
	for _, f := range []string{"--appdir=/tmp", "x", "--", "-1"} {
		if _, err := InstallSet(context.Background(), r, []string{f}, "jq"); err == nil || r.args != nil {
			t.Errorf("flag %q must be refused before brew runs", f)
		}
	}
}

func TestParseDryRunReadsUpgradesOfInstalledDependencies(t *testing.T) {
	// Lines copied from real `brew install --dry-run` output of Homebrew 7.0.11
	// on an Intel Mac: installing a formula would upgrade an outdated dependency.
	items, err := ParseDryRun(fixture(t, "recorded_dryrun_upgrade.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := SetItem{Name: "go", Version: "1.27.4", From: "1.23.5", Via: "brew-guard"}
	if len(items) != 1 || items[0] != want {
		t.Fatalf("items=%+v, want [%+v]", items, want)
	}
}

func TestParseDryRunUpgradeOfTheRequestedPackage(t *testing.T) {
	items, err := ParseDryRun([]byte("==> Would upgrade 1 outdated package:\njq 1.7 -> 1.8\n"))
	if err != nil || len(items) != 1 || items[0] != (SetItem{Name: "jq", Version: "1.8", From: "1.7"}) {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestParseDryRunRefusesWhatItCannotReadInAnUpgrade(t *testing.T) {
	for name, in := range map[string]string{
		"no arrow":         "==> Would upgrade 1 dependency for x:\ngo 1.23.5 1.27.4 extra\n",
		"only a name":      "==> Would upgrade 1 outdated package:\njq\n",
		"count mismatch":   "==> Would upgrade 2 dependencies for x:\ngo 1.23.5 -> 1.27.4\n",
		"bad name":         "==> Would upgrade 1 dependency for x:\n../go 1 -> 2\n",
		"bad target":       "==> Would upgrade 1 dependency for ../x:\ngo 1 -> 2\n",
		"arrow in install": "==> Would install 1 formula:\njq 1 -> 2\n",
	} {
		if _, err := ParseDryRun([]byte(in)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestParseDryRunRefusesAThirdPartyTapPath(t *testing.T) {
	// Real brew prints the full tap path for a formula of a third-party tap.
	// Only plain names are checked, so this has to fail instead of being skipped.
	_, err := ParseDryRun([]byte("==> Would install 1 formula:\ngonzalogtesta/brew-guard/brew-guard 0.2.0\n"))
	if err == nil {
		t.Fatal("want an error")
	}
}

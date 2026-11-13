package brewdata

import (
	"strings"
	"testing"

	"github.com/gonzalogtesta/brew-guard/internal/dates"
)

// The recorded_* files in testdata are real output of Homebrew 7.0.13
// (Linux, homebrew/core at git revision f3756358aef), not written by hand.

func TestRecordedInfo(t *testing.T) {
	pkgs, err := ParseInfo(fixture(t, "recorded_info_jq.json"))
	if err != nil || len(pkgs) != 1 {
		t.Fatalf("%v %v", pkgs, err)
	}
	jq := pkgs[0]
	if jq.Name != "jq" || jq.Kind != dates.Formula || jq.Tap != "homebrew/core" || jq.Version != "1.8.2" ||
		jq.SourcePath != "Formula/j/jq.rb" || !strings.HasPrefix(jq.URL, "https://") || len(jq.Checksum) != 64 {
		t.Fatalf("formula: %+v", jq)
	}
	if jq.PostInstall || jq.HasService || jq.HasCaveats || jq.Deprecated || jq.Disabled {
		t.Fatalf("jq should have no install-time signals: %+v", jq)
	}
}

func TestRecordedOutdatedEmpty(t *testing.T) {
	out, err := ParseOutdated(fixture(t, "recorded_outdated_empty.json"))
	if err != nil || len(out) != 0 {
		t.Fatalf("%v %v", out, err)
	}
}

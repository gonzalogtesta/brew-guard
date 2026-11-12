package decide

import (
	"strings"
	"testing"
	"time"

	"github.com/gonzalogtesta/brew-guard/internal/policy"
)

var now = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

func sub(daysOld float64, tap string) Subject {
	return Subject{Name: "jq", Tap: tap, Version: "1.8", DateSource: "homebrew-core commit",
		Published: now.Add(-time.Duration(daysOld * 24 * float64(time.Hour)))}
}

func hasReason(d Decision, sub string) bool {
	for _, r := range d.Reasons {
		if strings.Contains(r, sub) {
			return true
		}
	}
	return false
}

func TestAge(t *testing.T) {
	p := policy.Default()
	for _, c := range []struct {
		age  float64
		want Action
	}{{11.9, Block}, {12, Allow}, {40, Allow}} {
		if got := Evaluate(p, sub(c.age, "homebrew/core"), now).Action; got != c.want {
			t.Errorf("age %v: got %s want %s", c.age, got, c.want)
		}
	}
}

func TestUnknownDateFailsClosed(t *testing.T) {
	s := sub(30, "homebrew/core")
	s.Published = time.Time{}
	if Evaluate(policy.Default(), s, now).Action != Block {
		t.Fatal("unknown date must block")
	}
}

func TestWarnMode(t *testing.T) {
	p := policy.Default()
	p.Mode = policy.ModeWarn
	if Evaluate(p, sub(1, "homebrew/core"), now).Action != Warn {
		t.Fatal("warn mode must warn")
	}
}

func TestWarnModeUnlistedTap(t *testing.T) {
	p := policy.Default()
	p.Mode = policy.ModeWarn
	d := Evaluate(p, sub(30, "evil/tap"), now)
	if d.Action != Warn {
		t.Fatalf("unlisted tap in warn mode: got %s want warn", d.Action)
	}
	if !hasReason(d, `tap "evil/tap" is not in allowed_taps`) {
		t.Fatalf("missing tap reason: %+v", d.Reasons)
	}
}

func TestWarnModeUnknownDate(t *testing.T) {
	p := policy.Default()
	p.Mode = policy.ModeWarn
	s := sub(30, "homebrew/core")
	s.Published = time.Time{}
	if d := Evaluate(p, s, now); d.Action != Warn {
		t.Fatalf("unknown date in warn mode: got %s want warn", d.Action)
	}
}

func TestTaps(t *testing.T) {
	p := policy.Default()
	if Evaluate(p, sub(30, "evil/tap"), now).Action != Block {
		t.Fatal("unlisted tap must block")
	}
	p.AllowedTaps = append(p.AllowedTaps, "me/tools")
	d := Evaluate(p, sub(30, "me/tools"), now)
	if d.Action != Allow || !d.NeedsReview {
		t.Fatalf("allowed third-party tap needs review: %+v", d)
	}
	if !hasReason(d, "review code and diff") {
		t.Fatalf("third-party tap reason missing: %+v", d.Reasons)
	}
}

func TestOfficialTapNoReview(t *testing.T) {
	for _, tap := range []string{"homebrew/core", "homebrew/cask"} {
		if d := Evaluate(policy.Default(), sub(30, tap), now); d.NeedsReview || len(d.Reasons) != 0 {
			t.Errorf("%s: %+v", tap, d)
		}
	}
}

func TestException(t *testing.T) {
	p := policy.Default()
	p.Exceptions = []policy.Exception{{Package: "jq", Reason: "fix", Expires: time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)}}
	if d := Evaluate(p, sub(1, "homebrew/core"), now); d.Action != Allow || d.ExceptionUsed == "" {
		t.Fatalf("valid exception (last day) should allow: %+v", d)
	}
	later := now.AddDate(0, 0, 1)
	if Evaluate(p, sub(1, "homebrew/core"), later).Action != Block {
		t.Fatal("expired exception must not apply")
	}
	if Evaluate(p, sub(1, "evil/tap"), now).Action != Block {
		t.Fatal("exception must not bypass tap allowlist")
	}
}

func TestExceptionUnknownDate(t *testing.T) {
	p := policy.Default()
	p.Exceptions = []policy.Exception{{Package: "jq", Reason: "upstream outage", Expires: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)}}
	s := sub(30, "homebrew/core")
	s.Published = time.Time{}
	d := Evaluate(p, s, now)
	if d.Action != Allow || d.ExceptionUsed != "upstream outage" {
		t.Fatalf("exception should cover unknown date: %+v", d)
	}
	if hasReason(d, "publication date unknown") {
		t.Fatalf("unknown-date reason should be suppressed by exception: %+v", d.Reasons)
	}
}

func TestExceptionOtherPackage(t *testing.T) {
	p := policy.Default()
	p.Exceptions = []policy.Exception{{Package: "curl", Reason: "fix", Expires: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)}}
	d := Evaluate(p, sub(1, "homebrew/core"), now)
	if d.Action != Block || d.ExceptionUsed != "" {
		t.Fatalf("exception for another package must not apply: %+v", d)
	}
}

func TestExceptionExpiryBoundary(t *testing.T) {
	exp := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	p := policy.Default()
	p.Exceptions = []policy.Exception{{Package: "jq", Reason: "fix", Expires: exp}}
	for _, c := range []struct {
		name string
		at   time.Time
		want Action
	}{
		{"last second of expiry day", time.Date(2026, 11, 12, 23, 59, 59, 999999999, time.UTC), Allow},
		{"exact next midnight", time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC), Block},
		{"after expiry", time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC), Block},
	} {
		if got := Evaluate(p, sub(1, "homebrew/core"), c.at).Action; got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}

func TestExpiredExceptionReason(t *testing.T) {
	p := policy.Default()
	p.Exceptions = []policy.Exception{{Package: "jq", Reason: "fix", Expires: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}}
	d := Evaluate(p, sub(1, "homebrew/core"), now)
	if !hasReason(d, `exception for "jq" expired on 2026-11-01`) {
		t.Fatalf("expired reason missing: %+v", d.Reasons)
	}
}

func TestExceptionAllowsYoungVersion(t *testing.T) {
	p := policy.Default()
	p.Exceptions = []policy.Exception{{Package: "jq", Reason: "security fix", Expires: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)}}
	d := Evaluate(p, sub(2, "homebrew/core"), now)
	if d.Action != Allow || d.ExceptionUsed != "security fix" {
		t.Fatalf("got %+v", d)
	}
	if !hasReason(d, "allowed by exception") || !hasReason(d, "2.0 days old") {
		t.Fatalf("exception reason missing: %+v", d.Reasons)
	}
}

func TestReasonsContent(t *testing.T) {
	p := policy.Default()
	d := Evaluate(p, sub(3, "evil/tap"), now)
	if d.Action != Block {
		t.Fatalf("got %s", d.Action)
	}
	for _, want := range []string{
		`tap "evil/tap" is not in allowed_taps`,
		"version 1.8 is 3.0 days old (minimum 12)",
	} {
		if !hasReason(d, want) {
			t.Errorf("missing reason %q in %+v", want, d.Reasons)
		}
	}
}

func TestDateSourceShown(t *testing.T) {
	s := sub(3, "homebrew/core")
	s.DateSource = "GitHub API commit 1234abcd"
	d := Evaluate(policy.Default(), s, now)
	if !hasReason(d, "date from GitHub API commit 1234abcd") {
		t.Fatalf("date source not shown: %+v", d.Reasons)
	}
}

func TestUnknownDateReason(t *testing.T) {
	s := sub(30, "homebrew/core")
	s.Published = time.Time{}
	d := Evaluate(policy.Default(), s, now)
	if !hasReason(d, "publication date unknown") {
		t.Fatalf("missing reason: %+v", d.Reasons)
	}
}

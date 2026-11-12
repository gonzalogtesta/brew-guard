package policy

import (
	"strings"
	"testing"
)

const tomlOK = `
min_age_days = 20   # comment with "quotes"
mode = "warn"
allowed_taps = [
  "homebrew/core",
  'me/tools', # trailing
]
block_telemetry = false

[[exception]]
package = "jq"
reason = "needed for fix # not a comment"
expires = 2026-12-01
`

func TestTOML(t *testing.T) {
	p, err := Parse(".toml", []byte(tomlOK))
	if err != nil {
		t.Fatal(err)
	}
	if p.MinAgeDays != 20 || p.Mode != "warn" || p.BlockTelemetry || len(p.AllowedTaps) != 2 || p.AllowedTaps[1] != "me/tools" {
		t.Fatalf("bad policy: %+v", p)
	}
	if len(p.Exceptions) != 1 || p.Exceptions[0].Reason != "needed for fix # not a comment" || p.Exceptions[0].Expires.Format("2006-01-02") != "2026-12-01" {
		t.Fatalf("bad exception: %+v", p.Exceptions)
	}
}

func TestFormatsAgree(t *testing.T) {
	js := `{"min_age_days":20,"mode":"warn","allowed_taps":["homebrew/core","me/tools"],"block_telemetry":false,
	"exceptions":[{"package":"jq","reason":"needed for fix # not a comment","expires":"2026-12-01"}]}`
	tx := "min_age_days 20\nmode warn\nallow_tap homebrew/core\nallow_tap me/tools\nblock_telemetry false\nexcept jq 2026-12-01 needed for fix # not a comment\n"
	a, _ := Parse(".toml", []byte(tomlOK))
	for ext, in := range map[string]string{".json": js, ".txt": tx} {
		b, err := Parse(ext, []byte(in))
		if err != nil {
			t.Fatalf("%s: %v", ext, err)
		}
		if a.MinAgeDays != b.MinAgeDays || a.Mode != b.Mode || strings.Join(a.AllowedTaps, ",") != strings.Join(b.AllowedTaps, ",") ||
			a.Exceptions[0] != b.Exceptions[0] {
			t.Fatalf("%s differs: %+v vs %+v", ext, a, b)
		}
	}
}

func TestRejects(t *testing.T) {
	bad := map[string]string{
		".toml": "min_age_dayz = 3",
	}
	cases := []struct{ ext, in string }{
		{".toml", "min_age_dayz = 3"},
		{".toml", "min_age_days = 3\nmin_age_days = 4"},
		{".toml", "mode = \"allow\""},
		{".toml", "[tbl]\nx = 1"},
		{".toml", "min_age_days = -1"},
		{".toml", "allowed_taps = [\"a\" \"b\"]"},
		{".toml", "[[exception]]\npackage = \"jq\"\nexpires = 2026-12-01"},
		{".toml", "[[exception]]\nmin_age_days = 1"},
		{".json", `{"min_age_dayz":3}`},
		{".json", `{} {}`},
		{".txt", "min_age_dayz 3"},
		{".txt", "except jq 2026-12-01"},
		{".yaml", ""},
	}
	_ = bad
	for _, c := range cases {
		if _, err := Parse(c.ext, []byte(c.in)); err == nil {
			t.Errorf("%s %q: expected error", c.ext, c.in)
		}
	}
}

func TestDefaults(t *testing.T) {
	p, err := Parse(".toml", nil)
	if err != nil || p.MinAgeDays != 12 || p.Mode != ModeBlock || !p.BlockTelemetry {
		t.Fatalf("defaults wrong: %+v %v", p, err)
	}
}

func TestDeepNestingCannotCrash(t *testing.T) {
	in := "x = " + strings.Repeat("[", 20000) + strings.Repeat("]", 20000)
	if _, err := Parse(".toml", []byte(in)); err == nil {
		t.Fatal("expected error")
	}
}

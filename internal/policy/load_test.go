package policy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFind(t *testing.T) {
	for _, c := range []struct {
		name  string
		files []string
		want  string // base name of the expected result
		err   bool
		noent bool
	}{
		{name: "none", noent: true},
		{name: "unrelated files ignored", files: []string{"policy.yaml", "other.toml"}, noent: true},
		{name: "toml", files: []string{"policy.toml"}, want: "policy.toml"},
		{name: "json", files: []string{"policy.json"}, want: "policy.json"},
		{name: "txt", files: []string{"policy.txt"}, want: "policy.txt"},
		{name: "two files", files: []string{"policy.toml", "policy.json"}, err: true},
		{name: "all three", files: []string{"policy.toml", "policy.json", "policy.txt"}, err: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range c.files {
				if err := os.WriteFile(filepath.Join(dir, f), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := Find(dir)
			switch {
			case c.noent:
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("want ErrNotExist, got %v", err)
				}
			case c.err:
				if err == nil || errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "multiple policy files") {
					t.Fatalf("want ambiguity error, got %q (%v)", got, err)
				}
			default:
				if err != nil || got != filepath.Join(dir, c.want) {
					t.Fatalf("got %q (%v) want %q", got, err, c.want)
				}
			}
		})
	}
}

// Known bug, documented rather than fixed: Find only counts candidates whose
// os.Stat returns nil and ignores every other error. A policy.toml that cannot
// be stat'ed (here a self-referencing symlink, ELOOP) is treated as absent, so
// a sibling policy.json is used silently and the ambiguity check is bypassed.
// Expected: Find returns an error for any stat failure other than ErrNotExist.
func TestFindBrokenCandidate(t *testing.T) {
	dir := t.TempDir()
	toml := filepath.Join(dir, "policy.toml")
	if err := os.Symlink(toml, toml); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "policy.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Find(dir); err == nil {
		t.Fatalf("broken policy.toml silently ignored; Find returned %q", got)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	if _, err := Load(filepath.Join(dir, "missing.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file: want ErrNotExist, got %v", err)
	}

	big := write("big.toml", "# "+strings.Repeat("x", MaxFileSize))
	if _, err := Load(big); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("oversized file: got %v", err)
	}

	head := "min_age_days = 3\n"
	exact := write("exact.toml", head+strings.Repeat("#", MaxFileSize-len(head)))
	if p, err := Load(exact); err != nil || p.MinAgeDays != 3 {
		t.Fatalf("file of exactly MaxFileSize: %+v %v", p, err)
	}

	for _, c := range []struct {
		name, body string
		minAge     int
	}{
		{"valid.toml", "min_age_days = 5\n", 5},
		{"valid.json", `{"min_age_days": 6}`, 6},
		{"valid.txt", "min_age_days 7\n", 7},
		{"VALID.TOML", "min_age_days = 8\n", 8},
	} {
		p, err := Load(write(c.name, c.body))
		if err != nil || p.MinAgeDays != c.minAge {
			t.Errorf("%s: got %+v %v", c.name, p, err)
		}
	}

	if _, err := Load(write("policy.yaml", "min_age_days: 3\n")); err == nil {
		t.Fatal("unsupported extension must fail")
	}
}

func TestParseValidation(t *testing.T) {
	cases := []struct {
		name, ext, in string
		wantErr       bool
	}{
		{"min_age_days 0 ok", ".toml", "min_age_days = 0", false},
		{"min_age_days 3650 ok", ".toml", "min_age_days = 3650", false},
		{"min_age_days 3651 rejected", ".toml", "min_age_days = 3651", true},
		{"min_age_days 3651 json", ".json", `{"min_age_days": 3651}`, true},
		{"min_age_days 3651 text", ".txt", "min_age_days 3651", true},
		{"tap with space", ".toml", `allowed_taps = ["home brew/core"]`, true},
		{"tap leading dash", ".toml", `allowed_taps = ["-evil/tap"]`, true},
		{"tap empty", ".toml", `allowed_taps = [""]`, true},
		{"tap too long", ".toml", `allowed_taps = ["a` + strings.Repeat("b", 128) + `"]`, true},
		{"tap with @ and +", ".toml", `allowed_taps = ["me/tool@2+x"]`, false},
		{"json bad tap", ".json", `{"allowed_taps": ["bad tap"]}`, true},
		{"text bad tap", ".txt", "allow_tap bad;tap", true},
		{"exception bad package", ".toml", "[[exception]]\npackage = \"bad pkg\"\nreason = \"r\"\nexpires = 2026-12-01", true},
		{"exception empty reason", ".toml", "[[exception]]\npackage = \"jq\"\nreason = \"   \"\nexpires = 2026-12-01", true},
		{"exception empty reason json", ".json", `{"exceptions":[{"package":"jq","reason":"","expires":"2026-12-01"}]}`, true},
		{"exception bad date feb 30", ".toml", "[[exception]]\npackage = \"jq\"\nreason = \"r\"\nexpires = 2026-02-30", true},
		{"exception bad date words", ".toml", "[[exception]]\npackage = \"jq\"\nreason = \"r\"\nexpires = \"tomorrow\"", true},
		{"exception bad date text", ".txt", "except jq 12/01/2026 r", true},
		{"exception good date", ".toml", "[[exception]]\npackage = \"jq\"\nreason = \"r\"\nexpires = 2026-12-01", false},
		{"mode invalid", ".toml", `mode = "Warn"`, true},
		{"mode invalid json", ".json", `{"mode": "allow"}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.ext, []byte(c.in))
			if (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v got %v", c.wantErr, err)
			}
		})
	}
}

func TestTextDirectiveErrors(t *testing.T) {
	for _, in := range []string{
		"min_age_days",
		"min_age_days 1 2",
		"min_age_days twelve",
		"mode",
		"mode warn block",
		"allow_tap",
		"allow_tap a/b c/d",
		"block_telemetry",
		"block_telemetry yes please",
		"except",
		"except jq",
		"except jq 2026-12-01",
		"frobnicate 1",
	} {
		if _, err := Parse(".txt", []byte(in+"\n")); err == nil {
			t.Errorf("%q: expected error", in)
		}
	}
}

func TestTextErrorReportsLine(t *testing.T) {
	_, err := Parse(".txt", []byte("min_age_days 3\n\n# note\nbogus 1\n"))
	if err == nil || !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("want line 4 error, got %v", err)
	}
}

func TestTextDirectivesParse(t *testing.T) {
	p, err := Parse(".txt", []byte("mode warn\nblock_telemetry false\nallow_tap me/tools\nexcept jq 2026-12-01 needs   the fix\n"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != ModeWarn || p.BlockTelemetry || len(p.Exceptions) != 1 || p.Exceptions[0].Reason != "needs the fix" {
		t.Fatalf("got %+v", p)
	}
}

func TestJSONRejects(t *testing.T) {
	for _, in := range []string{
		`{"min_age_days": 3} x`,
		`{"min_age_days": 3} {"mode": "warn"}`,
		`{"min_age_days": "3"}`,
		`{"allowed_taps": "homebrew/core"}`,
		`{"exceptions": [{"packge": "jq", "reason": "r", "expires": "2026-12-01"}]}`,
		`{"exceptions": [{"package": "jq", "reason": "r", "expires": "2026-12-01", "extra": 1}]}`,
		`[]`,
		`{"min_age_days": 3`,
	} {
		if _, err := Parse(".json", []byte(in)); err == nil {
			t.Errorf("%s: expected error", in)
		}
	}
}

func TestJSONNullKeepsDefaults(t *testing.T) {
	p, err := Parse(".json", []byte(`{"min_age_days": null, "mode": null, "allowed_taps": null}`))
	if err != nil || p.MinAgeDays != 12 || p.Mode != ModeBlock || len(p.AllowedTaps) != 2 {
		t.Fatalf("got %+v %v", p, err)
	}
}

func TestUnsupportedExtension(t *testing.T) {
	for _, ext := range []string{"", ".yaml", ".TOMLX", "toml"} {
		if _, err := Parse(ext, []byte("mode = \"warn\"")); err == nil {
			t.Errorf("ext %q: expected error", ext)
		}
	}
}

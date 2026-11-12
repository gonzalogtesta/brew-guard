package policy

import (
	"strings"
	"testing"
)

// exceptionTOML builds a single [[exception]] block around the given reason
// line, which must already be valid TOML for the reason key.
func exceptionTOML(reasonLine string) string {
	return "[[exception]]\npackage = \"jq\"\n" + reasonLine + "\nexpires = 2026-12-01\n"
}

func TestTOMLCommentsInStrings(t *testing.T) {
	p, err := Parse(".toml", []byte(`mode = 'warn' # trailing comment`))
	if err != nil || p.Mode != ModeWarn {
		t.Fatalf("got %+v %v", p, err)
	}
	p, err = Parse(".toml", []byte(exceptionTOML(`reason = 'C:\path # not a comment'`)))
	if err != nil || p.Exceptions[0].Reason != `C:\path # not a comment` {
		t.Fatalf("got %+v %v", p.Exceptions, err)
	}
}

func TestTOMLEscapes(t *testing.T) {
	in := exceptionTOML(`reason = "say \"hi\"\tnow\n\\ done"`)
	p, err := Parse(".toml", []byte(in))
	if err != nil {
		t.Fatal(err)
	}
	want := "say \"hi\"\tnow\n\\ done"
	if p.Exceptions[0].Reason != want {
		t.Fatalf("reason %q want %q", p.Exceptions[0].Reason, want)
	}
}

func TestTOMLStringErrors(t *testing.T) {
	for _, in := range []string{
		exceptionTOML(`reason = "a\x"`),    // unsupported escape
		exceptionTOML(`reason = "abc\"`),   // dangling backslash (escaped closing quote)
		exceptionTOML(`reason = "a"b"`),    // unescaped quote
		exceptionTOML(`reason = unquoted`), // not a string
		`mode = "warn`,                     // unterminated string
		`mode = warn`,                      // bare word
	} {
		if _, err := Parse(".toml", []byte(in)); err == nil {
			t.Errorf("%q: expected error", in)
		}
	}
}

func TestTOMLSingleQuotedStrings(t *testing.T) {
	p, err := Parse(".toml", []byte(`mode = 'block'`))
	if err != nil || p.Mode != ModeBlock {
		t.Fatalf("got %+v %v", p, err)
	}
	p, err = Parse(".toml", []byte(exceptionTOML(`reason = 'no \escapes here'`)))
	if err != nil || p.Exceptions[0].Reason != `no \escapes here` {
		t.Fatalf("literal string should keep backslash: %+v %v", p.Exceptions, err)
	}
}

func TestTOMLSingleQuotedEmbeddedQuote(t *testing.T) {
	if _, err := Parse(".toml", []byte("mode = 'a'b'")); err == nil {
		t.Fatal("expected error for a quote inside a literal string")
	}
}

func TestTOMLMultiLineArray(t *testing.T) {
	in := `allowed_taps = [
  "homebrew/core", # core
  # full-line comment inside the array
  'me/tools',
  "other/tool"
]
mode = "warn"
`
	p, err := Parse(".toml", []byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.AllowedTaps, ",") != "homebrew/core,me/tools,other/tool" {
		t.Fatalf("taps %q", p.AllowedTaps)
	}
	if p.Mode != ModeWarn {
		t.Fatalf("key after array lost: %+v", p)
	}
}

func TestTOMLArrayShapes(t *testing.T) {
	for _, c := range []struct {
		name, in string
		n        int
		wantErr  bool
	}{
		{"empty", `allowed_taps = []`, 0, false},
		{"empty with spaces", `allowed_taps = [   ]`, 0, false},
		{"trailing comma", `allowed_taps = ["homebrew/core",]`, 1, false},
		{"missing comma", `allowed_taps = ["homebrew/core" "me/tools"]`, 0, true},
		{"unquoted item", `allowed_taps = [homebrew/core]`, 0, true},
		{"leading comma", `allowed_taps = [,"homebrew/core"]`, 0, true},
		{"unterminated string item", `allowed_taps = ["homebrew/core]`, 0, true},
		{"unterminated array", `allowed_taps = [ "a",`, 0, true},
		{"unterminated array no close bracket", "allowed_taps = [\n  \"a\"\n", 0, true},
		{"not an array", `allowed_taps = "homebrew/core"`, 0, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, err := Parse(".toml", []byte(c.in))
			if (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v got %v", c.wantErr, err)
			}
			if err == nil && len(p.AllowedTaps) != c.n {
				t.Fatalf("got %d taps want %d", len(p.AllowedTaps), c.n)
			}
		})
	}
}

func TestTOMLEmptyArrayAllowsNoTaps(t *testing.T) {
	p, err := Parse(".toml", []byte(`allowed_taps = []`))
	if err != nil {
		t.Fatal(err)
	}
	if p.AllowedTaps == nil || len(p.AllowedTaps) != 0 {
		t.Fatalf("empty array must replace the default taps, got %#v", p.AllowedTaps)
	}
}

func TestTOMLExpiresBareVsQuoted(t *testing.T) {
	bare, err1 := Parse(".toml", []byte(exceptionTOML(`reason = "r"`)))
	quoted, err2 := Parse(".toml", []byte("[[exception]]\npackage = \"jq\"\nreason = \"r\"\nexpires = \"2026-12-01\"\n"))
	if err1 != nil || err2 != nil {
		t.Fatalf("bare: %v quoted: %v", err1, err2)
	}
	if bare.Exceptions[0] != quoted.Exceptions[0] {
		t.Fatalf("bare %+v quoted %+v", bare.Exceptions[0], quoted.Exceptions[0])
	}
	if got := bare.Exceptions[0].Expires.Format("2006-01-02"); got != "2026-12-01" {
		t.Fatalf("expires %s", got)
	}
}

func TestTOMLBadExpires(t *testing.T) {
	for _, v := range []string{`2026-1-1`, `2026/12/01`, `20261201`, `"2026-02-30"`, `"soon"`} {
		in := "[[exception]]\npackage = \"jq\"\nreason = \"r\"\nexpires = " + v + "\n"
		if _, err := Parse(".toml", []byte(in)); err == nil {
			t.Errorf("expires = %s: expected error", v)
		}
	}
}

func TestTOMLUnterminatedAndDuplicateKeys(t *testing.T) {
	for _, in := range []string{
		"[[exception]]\npackage = \"jq\"\npackage = \"curl\"\nreason = \"r\"\nexpires = 2026-12-01\n",
		"min_age_days = 3\nmin_age_days = 3\n",
	} {
		if _, err := Parse(".toml", []byte(in)); err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Errorf("%q: want duplicate error, got %v", in, err)
		}
	}
}

func TestTOMLRepeatedKeyAcrossExceptionBlocks(t *testing.T) {
	in := "[[exception]]\npackage = \"jq\"\nreason = \"one\"\nexpires = 2026-12-01\n" +
		"[[exception]]\npackage = \"jq\"\nreason = \"two\"\nexpires = 2027-01-01\n"
	p, err := Parse(".toml", []byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Exceptions) != 2 || p.Exceptions[0].Reason != "one" || p.Exceptions[1].Reason != "two" {
		t.Fatalf("got %+v", p.Exceptions)
	}
}

func TestTOMLTopLevelKeyAfterExceptionRejected(t *testing.T) {
	in := "[[exception]]\npackage = \"jq\"\nreason = \"r\"\nexpires = 2026-12-01\nmode = \"warn\"\n"
	_, err := Parse(".toml", []byte(in))
	if err == nil || !strings.Contains(err.Error(), `unknown key "mode"`) {
		t.Fatalf("want unknown key error, got %v", err)
	}
}

func TestTOMLExceptionKeyAtTopLevelRejected(t *testing.T) {
	_, err := Parse(".toml", []byte("package = \"jq\"\n"))
	if err == nil || !strings.Contains(err.Error(), `unknown key "package"`) {
		t.Fatalf("want unknown key error, got %v", err)
	}
}

func TestTOMLStructuralErrors(t *testing.T) {
	for _, in := range []string{
		"[policy]\nmode = \"warn\"\n",
		"[[exception]\npackage = \"jq\"\n",
		"mode warn\n",
		" = 3\n",
		"min_age_days = 3.5\n",
		"block_telemetry = maybe\n",
	} {
		if _, err := Parse(".toml", []byte(in)); err == nil {
			t.Errorf("%q: expected error", in)
		}
	}
}

func TestTOMLErrorReportsLine(t *testing.T) {
	_, err := Parse(".toml", []byte("min_age_days = 3\n\nmode = allow\n"))
	if err == nil || !strings.Contains(err.Error(), "toml line 3") {
		t.Fatalf("want line 3 error, got %v", err)
	}
}

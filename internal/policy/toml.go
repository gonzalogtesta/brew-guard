package policy

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// parseTOML reads the small TOML subset the policy needs: top-level
// "key = value" lines and [[exception]] tables. Values are strings, integers,
// booleans, arrays of strings and bare dates. Anything else, unknown keys and
// duplicate keys are errors, so a typo can never silently drop a protection.
func parseTOML(data []byte) (raw, error) {
	var (
		r    raw
		cur  *rawException
		seen = map[string]bool{}
	)
	lines := strings.Split(string(data), "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(stripComment(lines[i]))
		if line == "" {
			continue
		}
		errf := func(f string, a ...any) error {
			return fmt.Errorf("toml line %d: %s", i+1, fmt.Sprintf(f, a...))
		}
		if line == "[[exception]]" {
			r.Exceptions = append(r.Exceptions, rawException{})
			cur = &r.Exceptions[len(r.Exceptions)-1]
			seen = map[string]bool{}
			continue
		}
		if strings.HasPrefix(line, "[") {
			return raw{}, errf("unsupported table %q", line)
		}
		key, val, ok := strings.Cut(line, "=")
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if !ok || key == "" {
			return raw{}, errf("expected key = value")
		}
		scope := "top"
		if cur != nil {
			scope = "exception"
		}
		if seen[key] {
			return raw{}, errf("duplicate key %q", key)
		}
		seen[key] = true
		// Arrays may span lines: join until brackets balance.
		for strings.HasPrefix(val, "[") && !balanced(val) {
			i++
			if i >= len(lines) {
				return raw{}, errf("unterminated array")
			}
			val += " " + strings.TrimSpace(stripComment(lines[i]))
		}
		var err error
		switch {
		case scope == "top" && key == "min_age_days":
			var n int
			if n, err = strconv.Atoi(val); err != nil {
				return raw{}, errf("min_age_days must be an integer")
			}
			r.MinAgeDays = &n
		case scope == "top" && key == "mode":
			var s string
			if s, err = tomlString(val); err == nil {
				r.Mode = &s
			}
		case scope == "top" && key == "block_telemetry":
			var b bool
			if b, err = strconv.ParseBool(val); err == nil {
				r.BlockTelemetry = &b
			}
		case scope == "top" && key == "allowed_taps":
			r.AllowedTaps, err = tomlStrings(val)
			if r.AllowedTaps == nil && err == nil {
				r.AllowedTaps = []string{}
			}
		case scope == "exception" && key == "package":
			cur.Package, err = tomlString(val)
		case scope == "exception" && key == "reason":
			cur.Reason, err = tomlString(val)
		case scope == "exception" && key == "expires":
			if dateRe.MatchString(val) {
				cur.Expires = val
			} else {
				cur.Expires, err = tomlString(val)
			}
		default:
			return raw{}, errf("unknown key %q", key)
		}
		if err != nil {
			return raw{}, errf("key %q: %v", key, err)
		}
	}
	return r, nil
}

// stripComment removes a trailing # comment that is outside any string.
func stripComment(s string) string {
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == '\\' && q == '"' {
				i++
			} else if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			q = c
		case c == '#':
			return s[:i]
		}
	}
	return s
}

func balanced(s string) bool {
	depth := 0
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == '\\' && q == '"' {
				i++
			} else if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			q = c
		case c == '[':
			depth++
		case c == ']':
			depth--
		}
	}
	return depth == 0
}

func tomlString(v string) (string, error) {
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		if strings.Contains(v[1:len(v)-1], "'") {
			return "", fmt.Errorf("single quote inside a literal string")
		}
		return v[1 : len(v)-1], nil
	}
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		var b strings.Builder
		body := v[1 : len(v)-1]
		for i := 0; i < len(body); i++ {
			c := body[i]
			if c == '"' {
				return "", fmt.Errorf("unescaped quote in string")
			}
			if c != '\\' {
				b.WriteByte(c)
				continue
			}
			i++
			if i >= len(body) {
				return "", fmt.Errorf("dangling backslash")
			}
			switch body[i] {
			case '"', '\\':
				b.WriteByte(body[i])
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				return "", fmt.Errorf("unsupported escape \\%c", body[i])
			}
		}
		return b.String(), nil
	}
	return "", fmt.Errorf("expected a quoted string")
}

func tomlStrings(v string) ([]string, error) {
	if len(v) < 2 || v[0] != '[' || v[len(v)-1] != ']' {
		return nil, fmt.Errorf("expected an array of strings")
	}
	body := strings.TrimSpace(v[1 : len(v)-1])
	var out []string
	for body != "" {
		if body[0] != '"' && body[0] != '\'' {
			return nil, fmt.Errorf("array items must be quoted strings")
		}
		q := body[0]
		end := -1
		for j := 1; j < len(body); j++ {
			if body[j] == '\\' && q == '"' {
				j++
			} else if body[j] == q {
				end = j
				break
			}
		}
		if end < 0 {
			return nil, fmt.Errorf("unterminated string in array")
		}
		s, err := tomlString(body[:end+1])
		if err != nil {
			return nil, err
		}
		out = append(out, s)
		body = strings.TrimSpace(body[end+1:])
		if strings.HasPrefix(body, ",") {
			body = strings.TrimSpace(body[1:])
		} else if body != "" {
			return nil, fmt.Errorf("expected comma between array items")
		}
	}
	return out, nil
}

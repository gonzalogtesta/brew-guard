package audit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeWC is an in-memory io.WriteCloser that can be told to fail.
type fakeWC struct {
	buf      bytes.Buffer
	writeErr error
	closeErr error
	closed   int
}

func (f *fakeWC) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return f.buf.Write(p)
}

func (f *fakeWC) Close() error {
	f.closed++
	return f.closeErr
}

func TestAppendMarshalError(t *testing.T) {
	// time.Time refuses to marshal years outside [0, 9999].
	p := filepath.Join(t.TempDir(), "audit.jsonl")
	e := Entry{Time: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), Action: "allow"}
	if err := Append(p, e); err == nil {
		t.Fatal("expected marshal error")
	}
}

func TestWriteEntry(t *testing.T) {
	sentinel := errors.New("boom")
	badTime := Entry{Time: time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC), Action: "allow"}

	tests := []struct {
		name     string
		entry    Entry
		writeErr error
		closeErr error
		wantErr  bool
		wantLine bool
	}{
		{name: "ok", entry: Entry{Action: "allow"}, wantLine: true},
		{name: "marshal error", entry: badTime, wantErr: true},
		{name: "write error", entry: Entry{Action: "allow"}, writeErr: sentinel, wantErr: true},
		{name: "close error", entry: Entry{Action: "allow"}, closeErr: sentinel, wantErr: true, wantLine: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := &fakeWC{writeErr: tc.writeErr, closeErr: tc.closeErr}
			err := writeEntry(w, tc.entry)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.closeErr != nil && err != tc.closeErr {
				t.Errorf("err = %v, want close error %v", err, tc.closeErr)
			}
			if tc.writeErr != nil && err != tc.writeErr {
				t.Errorf("err = %v, want write error %v", err, tc.writeErr)
			}
			if w.closed != 1 {
				t.Errorf("Close called %d times, want 1", w.closed)
			}
			if got := w.buf.Len() > 0; got != tc.wantLine {
				t.Errorf("wrote line = %v, want %v (%q)", got, tc.wantLine, w.buf.String())
			}
			if tc.wantLine && !strings.HasSuffix(w.buf.String(), "\n") {
				t.Errorf("line not newline-terminated: %q", w.buf.String())
			}
		})
	}
}

func TestAppend(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a", "audit.jsonl")
	for i := 0; i < 2; i++ {
		if err := Append(p, Entry{Time: time.Unix(0, 0).UTC(), Command: "install", Package: "jq", Action: "block"}); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(p)
	if strings.Count(string(b), "\n") != 2 {
		t.Fatalf("want 2 lines: %q", b)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
}

func TestAppendUnwritable(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	asDir := filepath.Join(dir, "is-a-dir")
	if err := os.Mkdir(asDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(blocker, "audit.jsonl"), // parent is a regular file
		asDir,                                 // target is a directory
	} {
		if err := Append(p, Entry{Action: "allow"}); err == nil {
			t.Errorf("Append(%q): expected error", p)
		}
	}
}

func TestAppendDirPerms(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := Append(filepath.Join(dir, "audit.jsonl"), Entry{Action: "allow"}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v want 0700", st.Mode().Perm())
	}
}

func TestAppendJSONRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "audit.jsonl")
	when := time.Date(2026, 11, 12, 9, 30, 0, 0, time.UTC)
	in := []Entry{
		{Time: when, Command: "install", Package: "jq", Tap: "homebrew/core", Version: "1.8", Action: "block",
			Reasons: []string{"version 1.8 is 1.0 days old", "tap \"evil\""}},
		{Time: when.Add(time.Hour), Command: "upgrade", Package: "curl", Tap: "me/tools", Version: "8.0", Action: "allow"},
	}
	for _, e := range in {
		if err := Append(p, e); err != nil {
			t.Fatal(err)
		}
	}

	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got []Entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		got = append(got, e)
	}
	if len(got) != len(in) {
		t.Fatalf("got %d entries want %d", len(got), len(in))
	}
	for i := range in {
		g, w := got[i], in[i]
		if !g.Time.Equal(w.Time) || g.Command != w.Command || g.Package != w.Package ||
			g.Tap != w.Tap || g.Version != w.Version || g.Action != w.Action ||
			strings.Join(g.Reasons, "|") != strings.Join(w.Reasons, "|") {
			t.Errorf("entry %d: got %+v want %+v", i, g, w)
		}
	}
}

func TestAppendJSONFieldNames(t *testing.T) {
	p := filepath.Join(t.TempDir(), "audit.jsonl")
	if err := Append(p, Entry{Time: time.Unix(0, 0).UTC(), Command: "install", Package: "jq", Tap: "homebrew/core", Version: "1", Action: "allow"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"time", "command", "package", "tap", "version", "action"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing field %q in %s", k, b)
		}
	}
	if _, ok := m["reasons"]; ok {
		t.Errorf("empty reasons should be omitted: %s", b)
	}
}

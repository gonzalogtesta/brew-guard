package dates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func at(day int) time.Time { return time.Date(2026, 8, day, 10, 0, 0, 0, time.UTC) }

// History shaped like homebrew-core's jq.rb: a version bump, then two bottle
// commits that must not reset the clock, then a later bump.
var jqHistory = []Commit{
	{"c5", at(25), "jq: update 1.8.2 bottle."},
	{"c4", at(20), "jq: update 1.8.2 bottle."},
	{"c3", at(18), "jq 1.8.2"},
	{"c2", at(10), "jq: revision bump"},
	{"c1", at(1), "jq 1.8.1"},
}

func TestFirstCommitIgnoresBottles(t *testing.T) {
	c, ok := FirstCommit(Formula, "jq", "1.8.2", jqHistory)
	if !ok || c.SHA != "c3" {
		t.Fatalf("got %+v ok=%v", c, ok)
	}
}

func TestFirstCommitOrderIndependent(t *testing.T) {
	rev := make([]Commit, len(jqHistory))
	for i, c := range jqHistory {
		rev[len(rev)-1-i] = c
	}
	if c, _ := FirstCommit(Formula, "jq", "1.8.2", rev); c.SHA != "c3" {
		t.Fatalf("got %+v", c)
	}
}

func TestFirstCommitPicksOldestMatch(t *testing.T) {
	h := []Commit{{"new", at(20), "jq 1.8.2"}, {"old", at(5), "jq 1.8.2"}}
	if c, _ := FirstCommit(Formula, "jq", "1.8.2", h); c.SHA != "old" {
		t.Fatalf("got %+v", c)
	}
}

func TestFirstCommitNoMatch(t *testing.T) {
	for _, v := range []string{"1.8", "1.8.20", "9.9.9", "1.8.2 "} {
		if _, ok := FirstCommit(Formula, "jq", v, jqHistory); ok {
			t.Errorf("version %q should not match", v)
		}
	}
	// A different formula's message must not match, and names are not regexes.
	if _, ok := FirstCommit(Formula, "j.", "1.8.2", jqHistory); ok {
		t.Error("name must be matched literally")
	}
}

func TestFormulaNewAndVersioned(t *testing.T) {
	h := []Commit{{"a", at(1), "python@3.13 3.13.9"}, {"b", at(2), "foo 1.0 (new formula)"}}
	if _, ok := FirstCommit(Formula, "python@3.13", "3.13.9", h); !ok {
		t.Error("versioned formula")
	}
	if _, ok := FirstCommit(Formula, "foo", "1.0", h); !ok {
		t.Error("new formula")
	}
}

func TestCaskPatterns(t *testing.T) {
	h := []Commit{{"a", at(3), "Update iterm2 from 3.5.0 to 3.6.0"}, {"b", at(4), "Update foo to 2.0"}}
	if c, ok := FirstCommit(Cask, "iterm2", "3.6.0", h); !ok || c.SHA != "a" {
		t.Errorf("cask update: %+v %v", c, ok)
	}
	if _, ok := FirstCommit(Cask, "foo", "2.0", h); !ok {
		t.Error("cask without from")
	}
	if _, ok := FirstCommit(Cask, "iterm2", "3.5.0", h); ok {
		t.Error("old version must not match the 'to' side")
	}
}

type fakeHistory struct {
	calls int
	h     []Commit
	err   error
}

func (f *fakeHistory) History(_ context.Context, repo, path string) ([]Commit, error) {
	f.calls++
	return f.h, f.err
}

func TestLookup(t *testing.T) {
	f := &fakeHistory{h: jqHistory}
	c := OpenCache(filepath.Join(t.TempDir(), "d.json"))
	r, err := Lookup(context.Background(), f, c, Formula, "homebrew/core", "jq", "1.8.2", "Formula/j/jq.rb", false)
	if err != nil || !r.Time.Equal(at(18)) || !strings.Contains(r.Source, "Homebrew/homebrew-core commit c3") {
		t.Fatalf("got %+v %v", r, err)
	}
	// Second call is served from the cache.
	if _, err := Lookup(context.Background(), f, c, Formula, "homebrew/core", "jq", "1.8.2", "Formula/j/jq.rb", false); err != nil || f.calls != 1 {
		t.Fatalf("cache not used: calls=%d err=%v", f.calls, err)
	}
}

func TestLookupFailuresAreErrors(t *testing.T) {
	ctx := context.Background()
	f := &fakeHistory{h: jqHistory}
	cases := []struct {
		name               string
		tap, version, path string
		offline            bool
		h                  *fakeHistory
	}{
		{"unknown tap", "evil/tap", "1.8.2", "Formula/j/jq.rb", false, f},
		{"no path", "homebrew/core", "1.8.2", "", false, f},
		{"version not in history", "homebrew/core", "9.9", "Formula/j/jq.rb", false, f},
		{"offline without cache", "homebrew/core", "1.8.2", "Formula/j/jq.rb", true, f},
		{"api error", "homebrew/core", "1.8.2", "Formula/j/jq.rb", false, &fakeHistory{err: fmt.Errorf("boom")}},
	}
	for _, c := range cases {
		r, err := Lookup(ctx, c.h, nil, Formula, c.tap, "jq", c.version, c.path, c.offline)
		if err == nil || r.Known() {
			t.Errorf("%s: expected an error and no date, got %+v %v", c.name, r, err)
		}
	}
}

func TestCacheSaveAndPermissions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "d.json")
	c := OpenCache(p)
	c.Put("k", Result{Time: at(1), Source: "s"})
	c.Put("unknown", Result{})
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	c2 := OpenCache(p)
	if r, ok := c2.Get("k"); !ok || !r.Time.Equal(at(1)) {
		t.Fatalf("round trip: %+v %v", r, ok)
	}
	if _, ok := c2.Get("unknown"); ok {
		t.Fatal("unknown results must not be cached")
	}
}

func TestCacheCorruptFileIgnored(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.json")
	os.WriteFile(p, []byte("{not json"), 0o600)
	if _, ok := OpenCache(p).Get("k"); ok {
		t.Fatal("corrupt cache must be treated as empty")
	}
}

func page(n int, prefix string) []map[string]any {
	var out []map[string]any
	for i := 0; i < n; i++ {
		out = append(out, map[string]any{"sha": fmt.Sprintf("%s%d", prefix, i), "commit": map[string]any{
			"message": "msg\n\nbody", "committer": map[string]any{"date": "2026-08-01T10:00:00Z"}}})
	}
	return out
}

func TestGitHubHistoryPaginationAndHeaders(t *testing.T) {
	var gotAuth, gotUA, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotUA, gotPath = r.Header.Get("Authorization"), r.Header.Get("User-Agent"), r.URL.Query().Get("path")
		n := 100
		if r.URL.Query().Get("page") == "2" {
			n = 3
		}
		json.NewEncoder(w).Encode(page(n, "p"+r.URL.Query().Get("page")+"-"))
	}))
	defer srv.Close()
	g := &GitHub{BaseURL: srv.URL, Token: "tok", UserAgent: "brew-guard/test"}
	h, err := g.History(context.Background(), "Homebrew/homebrew-core", "Formula/j/jq.rb")
	if err != nil || len(h) != 103 {
		t.Fatalf("len=%d err=%v", len(h), err)
	}
	if h[0].Message != "msg" {
		t.Fatalf("only the first message line must be kept: %q", h[0].Message)
	}
	if gotAuth != "Bearer tok" || gotUA != "brew-guard/test" || gotPath != "Formula/j/jq.rb" {
		t.Fatalf("headers: %q %q %q", gotAuth, gotUA, gotPath)
	}
}

func TestGitHubHistoryKeepsEarlierPagesWhenALaterOneFails(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Query().Get("page"))
		if r.URL.Query().Get("page") != "1" {
			w.WriteHeader(422) // "The request is taking too long to process"
			return
		}
		json.NewEncoder(w).Encode(page(100, "p1-"))
	}))
	defer srv.Close()
	g := &GitHub{BaseURL: srv.URL}
	h, err := g.History(context.Background(), "Homebrew/homebrew-core", "Formula/f/ffmpeg.rb")
	if err != nil || len(h) != 100 {
		t.Fatalf("the first page is good data: len=%d err=%v", len(h), err)
	}
	if strings.Join(asked, ",") != "1,2" {
		t.Fatalf("it must stop after the failed page: %v", asked)
	}
}

func TestGitHubHistoryFirstPageFailureIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(422) }))
	defer srv.Close()
	if _, err := (&GitHub{BaseURL: srv.URL}).History(context.Background(), "o/r", "p"); err == nil {
		t.Fatal("no data at all must stay an error")
	}
}

func TestGitHubRateLimitCarriesTheResetTime(t *testing.T) {
	reset := time.Date(2026, 11, 13, 20, 30, 0, 0, time.UTC)
	for name, status := range map[string]int{"403": 403, "429": 429} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", fmt.Sprint(reset.Unix()))
			w.WriteHeader(status)
		}))
		_, err := (&GitHub{BaseURL: srv.URL}).History(context.Background(), "o/r", "p")
		srv.Close()
		var rl *RateLimitError
		if !errors.As(err, &rl) || !rl.Reset.Equal(reset) {
			t.Errorf("%s: got %v", name, err)
		} else if !strings.Contains(err.Error(), "resets at") {
			t.Errorf("%s: message lacks the time: %v", name, err)
		}
	}
	if msg := (&RateLimitError{}).Error(); strings.Contains(msg, "resets at") || !strings.Contains(msg, "rate limit") {
		t.Fatalf("no reset time given: %q", msg)
	}
}

func TestGitHubErrors(t *testing.T) {
	cases := []struct {
		name   string
		handle http.HandlerFunc
		want   string
	}{
		{"rate limit", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(403)
		}, "rate limit"},
		{"server error", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }, "500"},
		{"bad json", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("nope")) }, "invalid"},
		{"oversize", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(strings.Repeat("a", maxBody+10)))
		}, "too large"},
	}
	for _, c := range cases {
		srv := httptest.NewServer(c.handle)
		g := &GitHub{BaseURL: srv.URL}
		_, err := g.History(context.Background(), "o/r", "p")
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v want %q", c.name, err, c.want)
		}
	}
}

func TestGitHubRefusesOffHostRedirect(t *testing.T) {
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("Authorization") != ""
		w.Write([]byte("[]"))
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer srv.Close()
	g := &GitHub{BaseURL: srv.URL, Token: "secret"}
	if _, err := g.History(context.Background(), "o/r", "p"); err == nil {
		t.Fatal("expected redirect to be refused")
	}
	if leaked {
		t.Fatal("token must never reach another host")
	}
}

func TestGitHubContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := (&GitHub{BaseURL: srv.URL}).History(ctx, "o/r", "p"); err == nil {
		t.Fatal("expected context error")
	}
}

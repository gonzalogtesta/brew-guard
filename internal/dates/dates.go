// Package dates finds when a package version was first published to
// Homebrew, using the commit history of its formula or cask file.
//
// The commit that introduced the version is used, not bottle or revision
// commits, because those would reset the clock on an old version. If the
// introducing commit cannot be identified the answer is "unknown", which the
// policy treats as a failure.
package dates

import (
	"fmt"
	"regexp"
	"time"
)

// Commit is one entry of a file's history.
type Commit struct {
	SHA     string
	Time    time.Time
	Message string // first line only
}

// Result is a publication date and where it came from.
type Result struct {
	Time   time.Time `json:"time"`
	Source string    `json:"source"`
}

// Known reports whether a date was found.
func (r Result) Known() bool { return !r.Time.IsZero() }

// Kind selects the commit-message convention of the tap.
type Kind string

const (
	Formula Kind = "formula"
	Cask    Kind = "cask"
)

// FirstCommit returns the oldest commit in history that introduced version of
// name. history may be in any order. ok is false when none matches.
func FirstCommit(kind Kind, name, version string, history []Commit) (Commit, bool) {
	re := matcher(kind, name, version)
	var best Commit
	var found bool
	for _, c := range history {
		if !re.MatchString(c.Message) {
			continue
		}
		if !found || c.Time.Before(best.Time) {
			best, found = c, true
		}
	}
	return best, found
}

// matcher builds the message pattern for a version bump:
//
//	formula: "jq 1.8.2" or "jq 1.8.2 (new formula)"
//	cask:    "Update iterm2 from 3.5.0 to 3.6.0" or "iterm2 3.6.0"
//
// Bottle commits ("jq: update 1.8.2 bottle.") and revision bumps do not match.
func matcher(kind Kind, name, version string) *regexp.Regexp {
	n, v := regexp.QuoteMeta(name), regexp.QuoteMeta(version)
	if kind == Cask {
		return regexp.MustCompile(`^(Update ` + n + ` (from \S+ )?to ` + v + `|` + n + ` ` + v + `)$`)
	}
	return regexp.MustCompile(`^` + n + ` ` + v + `( \(new formula\))?$`)
}

// Describe formats the origin of a date for display and the audit log.
func Describe(repo string, c Commit) string {
	sha := c.SHA
	if len(sha) > 10 {
		sha = sha[:10]
	}
	return fmt.Sprintf("%s commit %s", repo, sha)
}

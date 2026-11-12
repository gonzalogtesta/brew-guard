package dates

import (
	"context"
	"fmt"
)

// History provides the commit history of one file.
type History interface {
	History(ctx context.Context, repo, path string) ([]Commit, error)
}

// Repo returns the GitHub repository that holds the file for a tap.
// Only the two official taps are supported; anything else is unknown.
func Repo(tap string) (string, bool) {
	switch tap {
	case "homebrew/core":
		return "Homebrew/homebrew-core", true
	case "homebrew/cask":
		return "Homebrew/homebrew-cask", true
	}
	return "", false
}

// Lookup finds the publication date of name at version. path is the file path
// inside the tap repository (ruby_source_path from brew's JSON).
// Cache may be nil. With offline set, only the cache is consulted.
func Lookup(ctx context.Context, h History, c *Cache, kind Kind, tap, name, version, path string, offline bool) (Result, error) {
	key := fmt.Sprintf("%s/%s/%s@%s", tap, kind, name, version)
	if c != nil {
		if r, ok := c.Get(key); ok {
			return r, nil
		}
	}
	if offline {
		return Result{}, fmt.Errorf("no cached date for %s and offline mode is on", key)
	}
	repo, ok := Repo(tap)
	if !ok {
		return Result{}, fmt.Errorf("no date source for tap %q", tap)
	}
	if path == "" {
		return Result{}, fmt.Errorf("no source path for %s", name)
	}
	history, err := h.History(ctx, repo, path)
	if err != nil {
		return Result{}, err
	}
	commit, found := FirstCommit(kind, name, version, history)
	if !found {
		return Result{}, fmt.Errorf("no commit introducing %s %s found in the first %d commits of %s", name, version, len(history), path)
	}
	r := Result{Time: commit.Time, Source: Describe(repo, commit)}
	if c != nil {
		c.Put(key, r)
	}
	return r, nil
}

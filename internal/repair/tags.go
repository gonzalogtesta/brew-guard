package repair

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Release is a Homebrew release tag and when its commit was made.
type Release struct {
	Name string // as the tag is called, for example 5.0.3
	Time time.Time
}

// semver parses names like 5.0.3 (no prefix, no suffix). ok is false for
// anything else, such as pre-release or development tags.
func semver(name string) (v [3]int, ok bool) {
	parts := strings.Split(name, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p != strconv.Itoa(n) {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// SortedTags returns the plain x.y.z tags, newest first.
func SortedTags(names []string) []string {
	var out []string
	for _, n := range names {
		if _, ok := semver(n); ok {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := semver(out[i])
		b, _ := semver(out[j])
		for k := range a {
			if a[k] != b[k] {
				return a[k] > b[k]
			}
		}
		return false
	})
	return out
}

// Pick chooses the newest release that is at least minAgeDays old. The age
// rule applies to Homebrew itself, like it does to every package.
func Pick(releases []Release, now time.Time, minAgeDays int) (Release, error) {
	for _, r := range releases { // newest first
		if r.Time.IsZero() {
			continue
		}
		if now.Sub(r.Time) >= time.Duration(minAgeDays)*24*time.Hour {
			return r, nil
		}
	}
	return Release{}, errors.New("no Homebrew release is old enough")
}

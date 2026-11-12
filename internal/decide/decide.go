// Package decide turns a policy and facts about a package version into a
// decision. It is pure: no network, no clock, no filesystem.
package decide

import (
	"fmt"
	"time"

	"github.com/gonzalogtesta/brew-guard/internal/policy"
)

type Action string

const (
	Allow Action = "allow"
	Warn  Action = "warn"
	Block Action = "block"
)

// Subject is what is known about the version about to be installed.
type Subject struct {
	Name       string
	Tap        string
	Version    string
	Published  time.Time // zero when unknown
	DateSource string    // where Published came from, shown to the user
}

type Decision struct {
	Action        Action
	Reasons       []string
	NeedsReview   bool // third-party tap: show code and diff, require confirmation
	ExceptionUsed string
}

// Official taps need no per-install code review.
var official = map[string]bool{"homebrew/core": true, "homebrew/cask": true}

func Evaluate(p policy.Policy, s Subject, now time.Time) Decision {
	d := Decision{Action: Allow}
	fail := Block
	if p.Mode == policy.ModeWarn {
		fail = Warn
	}
	violate := func(msg string) {
		d.Reasons = append(d.Reasons, msg)
		if fail == Block || d.Action == Allow {
			d.Action = fail
		}
	}

	if !contains(p.AllowedTaps, s.Tap) {
		violate(fmt.Sprintf("tap %q is not in allowed_taps", s.Tap))
	} else if !official[s.Tap] {
		d.NeedsReview = true
		d.Reasons = append(d.Reasons, fmt.Sprintf("third-party tap %q: review code and diff before install", s.Tap))
	}

	var excepted bool
	for _, e := range p.Exceptions {
		if e.Package != s.Name {
			continue
		}
		if now.Before(e.Expires.AddDate(0, 0, 1)) {
			excepted = true
			d.ExceptionUsed = e.Reason
		} else {
			d.Reasons = append(d.Reasons, fmt.Sprintf("exception for %q expired on %s", s.Name, e.Expires.Format("2006-01-02")))
		}
	}

	switch {
	case s.Published.IsZero():
		if !excepted {
			violate("publication date unknown")
		}
	case now.Sub(s.Published) < time.Duration(p.MinAgeDays)*24*time.Hour:
		age := now.Sub(s.Published).Hours() / 24
		msg := fmt.Sprintf("version %s is %.1f days old (minimum %d), date from %s",
			s.Version, age, p.MinAgeDays, s.DateSource)
		if excepted {
			d.Reasons = append(d.Reasons, msg+"; allowed by exception")
		} else {
			violate(msg)
		}
	}
	return d
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

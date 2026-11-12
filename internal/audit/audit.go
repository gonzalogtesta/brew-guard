// Package audit appends one JSON line per policy decision.
package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type Entry struct {
	Time    time.Time `json:"time"`
	Command string    `json:"command"`
	Package string    `json:"package"`
	Tap     string    `json:"tap"`
	Version string    `json:"version"`
	Action  string    `json:"action"`
	Reasons []string  `json:"reasons,omitempty"`
}

// Append writes e to path, creating it with owner-only permissions.
func Append(path string, e Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

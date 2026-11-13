// Package audit appends one JSON line per policy decision.
package audit

import (
	"encoding/json"
	"io"
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
	return writeEntry(f, e)
}

// writeEntry writes e as one JSON line to w and always closes w.
func writeEntry(w io.WriteCloser, e Entry) error {
	line, err := json.Marshal(e)
	if err != nil {
		w.Close()
		return err
	}
	if _, err := w.Write(append(line, '\n')); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

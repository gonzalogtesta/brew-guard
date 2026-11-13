// Package repair finds out why a Homebrew checkout is broken and puts it back
// to an official release, without running anything the checkout itself could
// have been tampered to run.
package repair

import "strings"

// UnsafeConfig returns the entries of a git config file that make git run a
// program or talk to somewhere else than the plain defaults would: hooks,
// filters, aliases, pagers, credential helpers, URL rewrites and so on. A
// Homebrew checkout has none of them, so any hit is reported and blocks the
// repair.
func UnsafeConfig(b []byte) []string {
	var out []string
	section := ""
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			end := strings.IndexByte(line, ']')
			if end < 0 {
				continue
			}
			section = strings.ToLower(strings.TrimSpace(line[1:end]))
			rest := strings.TrimSpace(line[end+1:])
			if rest == "" {
				continue
			}
			line = rest // a key on the same line as the section header
		}
		key, value, _ := strings.Cut(line, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		name, _, _ := strings.Cut(section, " ")
		if risky(name, key, strings.ToLower(strings.TrimSpace(value))) {
			out = append(out, strings.TrimSpace(section+" "+key))
		}
	}
	return out
}

func risky(section, key, value string) bool {
	switch section {
	case "alias", "include", "includeif", "filter", "url", "merge":
		return true
	case "core":
		switch key {
		case "fsmonitor":
			// Homebrew's installer sets it to false. Anything else may name a program.
			return value != "false" && value != "no" && value != "off" && value != "0"
		case "hookspath", "sshcommand", "pager", "editor", "askpass", "gitproxy", "attributesfile":
			return true
		}
	case "credential":
		return key == "helper"
	case "diff":
		return key == "textconv" || key == "command"
	case "http":
		return key == "proxy" || key == "sslverify" || key == "sslcainfo" || key == "extraheader"
	case "remote":
		return key == "uploadpack" || key == "receivepack" || key == "vcs"
	case "protocol":
		return true
	}
	return false
}

package policy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func parseJSON(data []byte) (raw, error) {
	var r raw
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return raw{}, err
	}
	if dec.More() {
		return raw{}, fmt.Errorf("unexpected data after JSON policy")
	}
	return r, nil
}

// parseText reads one directive per line; '#' starts a comment.
//
//	min_age_days 12
//	mode block
//	allow_tap owner/name
//	block_telemetry true
//	except <package> <YYYY-MM-DD> <reason...>
func parseText(data []byte) (raw, error) {
	var r raw
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		bad := func(msg string) error { return fmt.Errorf("line %d: %s", i+1, msg) }
		switch f[0] {
		case "min_age_days":
			if len(f) != 2 {
				return raw{}, bad("min_age_days takes one number")
			}
			n, err := strconv.Atoi(f[1])
			if err != nil {
				return raw{}, bad("min_age_days is not a number")
			}
			r.MinAgeDays = &n
		case "mode":
			if len(f) != 2 {
				return raw{}, bad("mode takes one value")
			}
			r.Mode = &f[1]
		case "allow_tap":
			if len(f) != 2 {
				return raw{}, bad("allow_tap takes one tap")
			}
			r.AllowedTaps = append(r.AllowedTaps, f[1])
		case "block_telemetry":
			if len(f) != 2 {
				return raw{}, bad("block_telemetry takes true or false")
			}
			b, err := strconv.ParseBool(f[1])
			if err != nil {
				return raw{}, bad("block_telemetry must be true or false")
			}
			r.BlockTelemetry = &b
		case "except":
			if len(f) < 4 {
				return raw{}, bad("except needs: package YYYY-MM-DD reason")
			}
			r.Exceptions = append(r.Exceptions, rawException{f[1], strings.Join(f[3:], " "), f[2]})
		default:
			return raw{}, bad("unknown directive " + strconv.Quote(f[0]))
		}
	}
	return r, nil
}

# brew-guard

Homebrew, but with a security policy and without the telemetry.

I like Homebrew and I don't want to replace it. What bothers me is that it
installs and upgrades whatever is out there, minutes after release, and nobody
checks if it is any good. brew-guard sits next to brew and applies rules you
set. It never patches Homebrew's files.

## Status

Early development. Don't count on it to protect you yet.

The pieces exist and are tested, but nothing blocks a `brew install` right now.
The part that connects them (a `check` command and a shim for `brew`) comes
next.

## What is in the repo today

- Policy file in TOML, JSON or plain text. The format comes from the file
  extension. Unknown keys are an error, so a typo can't quietly turn a rule off.
- Decision engine: minimum age of a release (default 12 days), tap allowlist,
  and exceptions that need a reason and an expiry date. If the release date is
  unknown it counts as a failure.
- Release date lookup: finds the homebrew-core / homebrew-cask commit that
  introduced a version. Bottle and revision commits are ignored, otherwise an
  old version would look new again.
- Runner for the real `brew`: absolute path, cleaned environment, parses
  `brew info --json=v2` and `brew outdated --json=v2`.
- Audit log in JSON lines.
- `brew-guard doctor`: checks that analytics are off and that a few privacy
  variables are set.
- `brew-guard policy validate [file]`

## What is not there yet

- `check`, the shim and the upgrade flow
- showing a third party tap's code and diff before installing
- attestation checks and change alerts
- SBOM and vulnerability audit (OSV)

## Policy

`~/.config/brew-guard/policy.toml`

```toml
min_age_days = 12
mode = "block"            # or "warn"
allowed_taps = ["homebrew/core", "homebrew/cask"]
block_telemetry = true

[[exception]]
package = "jq"
reason = "needed for a security fix"
expires = 2026-12-01
```

Same settings work as `policy.json` or `policy.txt`. Keep only one of them in
the directory, with two it refuses to start.

## Build and test

You need the Go version pinned in `go.mod` (see
[docs/PINNED-VERSIONS.md](docs/PINNED-VERSIONS.md)).

```
go test ./...
go run ./cmd/brew-guard doctor
```

## Network

No telemetry. The only host brew-guard talks to is:

- `api.github.com`: read-only, to get the commit history of a formula or cask.
  An optional token raises the rate limit and is only sent to that host.

Nothing in the CLI calls it yet, the code is in `internal/dates`. `doctor` only
runs local commands. Any new endpoint will be added to this list.

## Limits

It lowers risk, it doesn't prove a package is safe. It won't help if
Homebrew's own infrastructure is compromised, if the malicious code is old, or
if malware already runs as your user.

## Dependencies

None, standard library only. Versions are pinned, see
[docs/PINNED-VERSIONS.md](docs/PINNED-VERSIONS.md).

## Licence

MIT

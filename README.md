# brew-guard

Homebrew, but with a security policy and without the telemetry.

I like Homebrew and I don't want to replace it. What bothers me is that it
installs and upgrades whatever is out there, minutes after release, and nobody
checks if it is any good. brew-guard sits next to brew and applies rules you
set. It never patches Homebrew's files.

## Status

Early development. Don't count on it to protect you yet.

There is a `check` command that tells you if a package passes your policy, but
nothing stops a `brew install` by itself yet. The shim for `brew` comes next.

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
- `brew-guard check`: looks at packages and tells you if the policy allows them.
  See below.

## What is not there yet

- the shim, so `brew install` runs `check` first
- the upgrade flow
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

## check

```
brew-guard check jq wget
brew-guard check --outdated
brew-guard check --offline jq
brew-guard check --no-deps jq
brew-guard check --policy ./policy.toml jq
```

For each package it asks brew for the tap and version, finds the release date,
applies the policy and prints the result with the reasons. Every decision goes
to the audit log. It only reports, it doesn't install or block anything.

`brew install jq` can bring other packages with it. So `check jq` first asks
brew what it would install (`brew install --dry-run`, which installs nothing),
prints how many packages come with it and which ones, and then checks every one
of them with the same policy. If one dependency is too young, the whole result
is a block and the output says which package it is. `--no-deps` checks only the
names you gave.

`--outdated` does the same for upgrades, but differently: Homebrew's upgrade
dry-run prints several sections that I can't rely on, so it reads the formula
metadata instead. It follows the runtime dependencies that aren't installed
yet and checks those too. That is an estimate, not Homebrew's own plan: build
only dependencies are left out and casks aren't expanded.

If the release date can't be found (rate limit, no matching commit, no network)
the date is unknown and that counts as a failure. `--offline` only uses dates
already in the cache. It restricts brew-guard's own GitHub lookups, not brew's
dry-run.

If `block_telemetry` is on and Homebrew analytics are not disabled, the check
fails too.

Exit codes:

| code | meaning |
|---|---|
| 0 | all packages allowed |
| 1 | something was blocked |
| 2 | usage error, bad policy, brew not found or brew failed |
| 3 | nothing blocked, but a third party tap needs review |

Files:

| what | where |
|---|---|
| policy | `~/.config/brew-guard/policy.{toml,json,txt}` |
| release date cache | `~/.cache/brew-guard/dates.json` |
| audit log | `~/.local/state/brew-guard/audit.jsonl` |

`XDG_CONFIG_HOME`, `XDG_CACHE_HOME` and `XDG_STATE_HOME` are respected. With no
policy file the defaults are used (12 days, block mode, homebrew/core and
homebrew/cask).

Third party taps have no date source yet, so they come out as unknown.

## Releases

A tag like `v0.1.0` starts the release workflow. It runs the tests, builds
the binaries for macOS and Linux (amd64 and arm64) with
`scripts/build-release.sh`, and publishes them with a `SHA256SUMS` file.

To check a download:

```
shasum -a 256 -c SHA256SUMS --ignore-missing
```

That only proves the file is the one that was published. There is no signature
or build provenance yet, so it doesn't prove who built it. You can also build
it yourself from the tag with the same script and compare the checksums, the
build is reproducible. The binaries aren't notarized by Apple, so macOS may ask
you to confirm the first run.

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
  `check` is the only command that calls it. An optional token
  (`GITHUB_TOKEN` or `GH_TOKEN`) raises the rate limit and is only sent to that
  host.

`brew` itself is started with a cleaned environment: only a few variables are
passed on, all other `HOMEBREW_*` and `DYLD_*` ones are dropped, and auto update
and analytics are off. `doctor` only runs local commands. Any new endpoint will
be added to this list.

## Limits

It lowers risk, it doesn't prove a package is safe. It won't help if
Homebrew's own infrastructure is compromised, if the malicious code is old, or
if malware already runs as your user.

## Dependencies

None, standard library only. Versions are pinned, see
[docs/PINNED-VERSIONS.md](docs/PINNED-VERSIONS.md).

## Licence

MIT

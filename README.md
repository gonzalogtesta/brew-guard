# brew-guard

Use Homebrew securely and privately. brew-guard adds a security policy on top
of Homebrew without replacing it or patching any of its files.

**Status: early development.** The policy engine exists. The integration that
actually gates `brew install` and `brew upgrade` is not built yet, so do not
rely on this tool to protect you today.

## What exists today

- A policy file in TOML, JSON or plain text (chosen by file extension) that
  rejects unknown keys, so a typo cannot silently turn a protection off.
- A decision engine: minimum release age (default 12 days), tap allowlist,
  and per-package exceptions that need a reason and an expiry date.
  An unknown publication date is treated as a failure.
- A JSON-lines audit log of decisions.
- `brew-guard doctor`: checks that Homebrew analytics are off and that
  privacy-related `HOMEBREW_*` variables are set.
- `brew-guard policy validate [file]`.

## What is planned

Shim or wrapper for `brew install` and `brew upgrade`, publication-date lookup,
tap code review and diff, bottle attestation checks, change alerts, an SBOM,
and vulnerability audit. Each phase is reviewed before the next starts.

## Policy example

`~/.config/brew-guard/policy.toml`:

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

The same settings work as `policy.json` or `policy.txt`. Keep exactly one
policy file in the directory.

## Build and test

Requires the pinned Go toolchain (see `docs/PINNED-VERSIONS.md`).

```
go test ./...
go run ./cmd/brew-guard doctor
```

## Network and privacy

brew-guard makes no network calls today and has no telemetry. When lookups are
added, every endpoint will be listed here.

## What it does not do

It reduces risk and cannot prove a package is safe. It does not protect
against a compromised Homebrew infrastructure, malicious code that is already
old, or malware running with your user privileges on your machine.

## Dependencies

None. See [docs/PINNED-VERSIONS.md](docs/PINNED-VERSIONS.md).

## Licence

MIT. See [LICENSE](LICENSE).

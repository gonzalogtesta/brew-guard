# Pinned versions

brew-guard pins every version it builds and runs with. Nothing floats.
This file lists what is pinned today and how to check it.

## Rules

- Every version we adopt was published at least 12 days before we adopted it.
- Versions are exact. No ranges, no `latest`, no floating tags.
- A new dependency is added only after its name, publisher, repository,
  licence, install scripts and known vulnerabilities have been checked and
  it appears in the table below.
- Installs in CI use the frozen lockfile and run with scripts disabled.
- Checks reduce risk but cannot prove that any package is free of malware.

## What is pinned

| Item | Pinned to | Published | Where it is pinned |
|---|---|---|---|
| Go toolchain | go1.27.3 | 2026-10-19 | `go` line in `go.mod` |
| Third-party Go modules | none | n/a | `go.mod` has no `require` lines |
| GitHub Actions from other publishers | none | n/a | `.github/workflows/` only runs `git`, `go`, `gh` and shell |

The toolchain is fetched through the Go module proxy and verified against
the public checksum database (`sum.golang.org`). Go has no package-manager
age setting, so the 12-day age is checked by hand from the proxy publish time.

## Check it yourself

```
go version
go list -m all
go mod verify
```

`go list -m all` prints only `brew-guard` while there are no dependencies.

## CI and release

The workflows in `.github/workflows/` use no actions from other publishers.
They fetch the code with `git`, build with the Go toolchain pinned in
`go.mod`, and publish with `gh`, the GitHub tool that is already on the
runner. Permissions start at none and each job asks only for what it needs
(`contents: read` for tests, `contents: write` for a release).

What is not pinned: the runner image is `ubuntu-24.04`, a moving image that
GitHub updates, and `git`, `go` (the starter toolchain that then fetches
go1.27.3) and `gh` come from that image. Those are GitHub's, not a package in
this repository, but they are part of what builds the release.

Releases are built by `scripts/build-release.sh`. The build has no cgo, no VCS
stamp and no local paths, so building the same commit twice gives identical
files. The checksums are in `SHA256SUMS` next to the files.

## Planned pins (not in the repository yet)

- Build provenance (an attestation signed through Sigstore), which needs
  `actions/attest`. Not added until it is approved as a dependency.

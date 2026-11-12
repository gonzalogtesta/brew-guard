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

## Planned pins (not in the repository yet)

- GitHub Actions referenced by full commit SHA, with least-privilege
  permissions.
- Release binaries built reproducibly, with published checksums and Sigstore
  signatures.

# Security policy

brew-guard is a security tool, so reports about it are taken seriously.

## Reporting a vulnerability

Please don't open a public issue for a security problem.

Use GitHub's private reporting instead: go to the Security tab of this
repository and click "Report a vulnerability". If that is not available, open a
public issue that only says you have a security report, without details, and
I will contact you.

Please include:

- what you found and where (file, command, version)
- how to reproduce it
- what you think the impact is

I will answer as soon as I can. This is a one person open source project, so
I can't promise a fixed response time, but I will keep you updated and credit
you in the fix if you want.

## What is in scope

- a way to get around the policy (age gate, tap allowlist, exceptions)
- a way to make `check` report "allowed" when it should not
- brew-guard running or leaking something it should not (environment
  variables, tokens, files)
- weaknesses in the release process (builds, checksums, signatures, the tap)

## What is not in scope

- problems in Homebrew itself: report those to Homebrew
- an attacker who already runs code as your user or as root on your machine
  (see "Limits" in the README)
- packages that are malicious but old enough to pass your policy

## Supported versions

There is no stable release yet. Only the latest commit on `main` is looked at.

## Dependencies

brew-guard has no third party dependencies. Versions of the toolchain are
pinned, see [docs/PINNED-VERSIONS.md](docs/PINNED-VERSIONS.md).

# brew-guard

Homebrew, but with a security policy and without the telemetry.

I like Homebrew and I don't want to replace it. What bothers me is that it
installs and upgrades whatever is out there, minutes after release, and nobody
checks if it is any good. brew-guard sits next to brew and applies rules you
set. It never patches Homebrew's files.

## Status

Early development. The first release is v0.1.0, the binaries are unsigned and
unnotarized. Don't count on it to protect you yet.

Once the `brew` link is in your `PATH`, `brew install`, `reinstall` and
`upgrade` go through your policy first, and so does everything they would pull
in. I have tested it with fake brew output and against a Linux Homebrew in a
container. A real install on macOS, and casks, are not tested yet.

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
- `brew-guard check`: tells you if the policy allows a package and everything
  it would bring, and prints notes about what runs at install time.
- `brew-guard brew ...` (or a link called `brew`): runs `install`, `reinstall`
  and `upgrade` through the check, passes read-only commands on, refuses the
  rest.
- `brew-guard shim install|uninstall|status`: the `brew` link.
- `brew-guard repair`: checks if Homebrew's own folder was altered and can
  reset it to an official release.
- `brew-guard doctor` and `brew-guard harden`: check privacy settings, including
  that Homebrew's tap trust is not switched off, and set them for you.
- `brew-guard policy validate [file]`
- A reproducible build script and CI and release workflows that use no
  third party actions.

## What is not there yet

- showing a third party tap's code and diff before installing, and `brew trust`
- pinning the real brew against replacement, and taking over the `brew` symlink
- telling a half finished Homebrew update apart from tampering (`repair` reports what differs, it can't say why)
- attestation checks and change alerts
- SBOM and vulnerability audit (OSV)
- signed releases and build provenance
- a tap, so that brew-guard can be installed with brew
- cask support is untested, and dependencies of casks aren't expanded

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

Without a token GitHub allows 60 requests an hour. When that runs out,
brew-guard stops asking, checks the rest from the cache, blocks what it can't
check and tells you when to run it again. The dates it found are kept.

If the release date can't be found (rate limit, no matching commit, no network)
the date is unknown and that counts as a failure.

GitHub lets you make 60 requests an hour without a token, and each package
needs about one. Dates never change, so every date found is saved in the cache
and only asked once. When the limit is reached, brew-guard stops asking, checks
the rest against the cache only, and tells you how many packages are left and
when the limit resets (the packages it couldn't check are blocked, never
guessed). Run it again after that time and it continues where it stopped. A
`GITHUB_TOKEN` raises the limit to 5000 an hour, but you don't need one for
installing a few packages. `--offline` only uses dates
already in the cache. It restricts brew-guard's own GitHub lookups, not brew's
dry-run.

If `block_telemetry` is on and Homebrew analytics are not disabled, the check
fails too.

## Using it as brew

If the program is called `brew` (a link or a copy with that name), or you run
`brew-guard brew ...`, it works as a wrapper around the real brew:

```
brew-guard brew install jq
brew-guard brew upgrade
```

- `install`, `reinstall` and `upgrade` run the same check as `check`, on the
  packages and on everything they would bring. Only if all of it passes does
  the real brew run, with exactly the names that were checked. Before it runs,
  the set is resolved a second time. If it changed, nothing runs.
- Options are limited to a short list (`--cask`, `--force`, `--verbose` and a
  few more). Things like `--HEAD` or `--no-quarantine` are refused, and so are
  paths, URLs and `user/tap/name`, because the policy can't see what they do.
- Read-only commands (`info`, `list`, `outdated`, `search`, `update`, ...) go
  straight to the real brew. `update` still works, so you can download all
  formulae and decide later what to install.
- Anything else (`bundle`, `tap`, `cask`, ...) is refused. It could install
  something that wasn't checked. Use the real brew directly if you mean it.
- brew runs with a clean environment: only a few variables of yours go through,
  and every `HOMEBREW_*` you set is dropped, so a variable that redirects
  downloads can't reach it. Analytics and auto update are off.

The real brew must be a different file than brew-guard. If the `brew` it finds
is brew-guard itself it stops instead of calling itself.

### The brew link

```
brew-guard shim install
brew-guard shim status
brew-guard shim uninstall
```

`shim install` creates a link named `brew` in `~/.local/bin` (change it with
`--dir`) that points to brew-guard. It doesn't edit your shell profile, it
only tells you the line to add so that directory comes before Homebrew's in
`PATH`. It won't overwrite a `brew` that is not its own link. Nothing inside
Homebrew's folders is renamed or changed. `shim status` tells you which
`brew` your shell really runs.

This protects the normal case, you typing `brew install`. It doesn't stop
something that calls Homebrew's brew by its full path, and it doesn't stop
malware running as you from editing your `PATH`. Run `shim status` now and then.

## harden

```
brew-guard harden
brew-guard harden --apply
brew-guard harden --undo
```

Without a flag it only prints the settings (the three `HOMEBREW_*` exports and
`brew analytics off`) so you can add them yourself.

`--apply` does it for you. It shows what it will do and asks for a yes first
(`--yes` skips that):

- adds a block between `# >>> brew-guard harden >>>` and
  `# <<< brew-guard harden <<<` to your shell profile (`~/.zprofile` for zsh,
  `~/.bash_profile` for bash, or the file you give with `--profile`). Exports
  that the profile already has are left as they are. The old profile is saved
  in `~/.local/state/brew-guard/backups` first. If the profile is a symlink,
  the file it points to is written.
- turns analytics off in the git config of the Homebrew checkout, the same
  setting `brew analytics off` writes. It uses `git config --file` on that one
  file, so git reads no other config.

It writes nothing else. It refuses to touch a profile whose block is broken
(not closed, or twice) instead of guessing where to cut.

Open a new terminal afterwards, the running one keeps its old environment.
`--undo` removes the block and puts analytics back as they were before, unless
you changed that setting since. It only knows what `--apply` did, which it
records in `~/.local/state/brew-guard/harden.json`.

## repair

```
brew-guard repair
brew-guard repair --apply
```

Checks if the Homebrew folder itself was damaged or altered. Without `--apply`
it only reads. It tells you:

- if the git config of the checkout has settings that can run programs (hooks,
  filters, aliases, credential helpers, URL rewrites, a file system monitor
  that is not `false`). If it does, it doesn't run git there at all.
- if git hooks are installed (a normal Homebrew checkout has none)
- which tracked files are missing or changed, and which extra files sit under
  `Library/Homebrew`, with the first ones listed
- if the commit is an official Homebrew release, by asking GitHub for the
  release tags (`--offline` skips this)

`--apply` backs up the whole folder to `~/.local/state/brew-guard/backups`,
fetches the release tags from `github.com/Homebrew/brew` (never from the
remote in the checkout's config), and resets to the newest release that is at
least `min_age_days` old. It asks for a yes first, `--yes` skips that. Extra
files are removed only under `Library/Homebrew`. Installed packages and your
taps are not touched. If you didn't expect the changes it found, don't treat
the repair as a fix for whatever caused them: look at the backup first.

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
build is reproducible.

The binaries aren't notarized by Apple. A file downloaded with a browser or
curl gets a quarantine flag, and macOS kills it on the first run (or asks you
to confirm it in System Settings, Privacy & Security). Once the checksum is
verified, you can remove the flag yourself:

```
xattr -d com.apple.quarantine ~/.local/bin/brew-guard
```

Only do this for a file whose checksum you checked.

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

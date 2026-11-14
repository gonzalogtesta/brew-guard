# Protect your Homebrew

You can do most of this without brew-guard. These are the settings and habits
that make Homebrew safer and more private today. Where I'm not sure about
something I say so.

`brew-guard harden` prints the environment settings below, ready to paste.
`brew-guard harden --apply` writes them for you, with a backup, and
`brew-guard harden --undo` puts everything back. `brew-guard doctor` checks them.

One thing I learned the hard way: a terminal that doesn't read your shell
profile starts brew without these variables, and brew auto-updates every tap,
third party ones included. Homebrew also reads `~/.homebrew/brew.env`, which
works in any terminal. `harden --apply` writes that file too.

## 1. Turn off analytics

Homebrew sends anonymous usage data (package names, install options, OS and
brew version, command names) to a hosted database. The server still sees your
IP address. It is on by default.

Turn it off in two places:

```
brew analytics off
```

and in your shell profile:

```
export HOMEBREW_NO_ANALYTICS=1
```

Check it with `brew analytics state`.

## 2. No automatic updates

By default brew updates itself before many commands. That means new code and new
versions arrive without you asking.

```
export HOMEBREW_NO_AUTO_UPDATE=1
```

Run `brew update` yourself when you decide to. Then look at what is outdated
before you upgrade (`brew outdated`, or `brew-guard check --outdated`).

## 3. Don't follow downloads from https to http

```
export HOMEBREW_NO_INSECURE_REDIRECT=1
```

## 4. Never run brew as root

Homebrew refuses to run as root, and you shouldn't try to get around that. If a
cask asks for an admin password, macOS shows its own prompt. Read what it is
asking for before you type anything.

## 5. Be careful with taps

A third party tap is Ruby code from someone's repository. It runs on your
machine when you install from it, and nobody reviews it for you.

- Only add taps you really need, from authors you can check.
- Read the formula or cask file before the first install.
- Read what changed before you upgrade something from a tap.
- Remove taps you don't use: `brew untap name/tap`.

Since Homebrew 6.0 there is tap trust: a tap that isn't an official Homebrew one
must be trusted before Homebrew will load its Ruby code. If it isn't trusted,
brew refuses with an error that tells you to run `brew trust`. Installing by
the full name (`user/tap/formula`) trusts only that one item. Trust is saved in
a file called `trust.json` in your Homebrew config directory.

- `brew trust` and `brew untrust` list, grant and remove trust.
- `brew tap-info --json=v1` shows a `trusted` flag for each tap.
- Don't set `HOMEBREW_NO_REQUIRE_TAP_TRUST`. It switches the check off. Homebrew
  has marked it as deprecated, and `brew-guard doctor` flags it if it's set.

I read this from the Homebrew docs and source, I haven't tried it myself on a
working install. `HOMEBREW_ALLOWED_TAPS` is an older separate setting that is also
marked as deprecated but still works. Don't build on it.

## 6. Don't take brand new releases

A malicious release is most dangerous in the first hours or days, before anyone
noticed. Waiting a few days costs you little. Homebrew has no setting for this,
which is one of the reasons brew-guard exists: `min_age_days` in the policy.

If you can't use the tool, upgrade by hand and wait a while before you upgrade
things that matter.

## 7. Casks

- Keep macOS quarantine on. It's what makes Gatekeeper check an app when you
  first open it. Don't remove the quarantine flag from apps by hand.
- Be extra careful with casks that install a `.pkg`, a launch daemon or a
  kernel extension. They run installers with admin rights.
- A cask whose checksum is `:no_check` can't be verified at all.

## 8. Verify bottles (optional)

Homebrew can verify build provenance for core bottles. It is off by default and
needs the GitHub CLI (`gh`):

```
export HOMEBREW_VERIFY_ATTESTATIONS=1
```

Last time I checked, this covered core bottles, and not casks. Check the Homebrew
docs for the current state.

## 9. Pin what you don't want to move

```
brew pin name
```

A pinned formula isn't upgraded until you unpin it. Homebrew doesn't support
every mix of versions, so an upgrade of something else can still pull related
formulae.

## 10. Your PATH

Whatever comes first in `PATH` is what runs when you type `brew`. Check
where it points:

```
type -a brew
```

You should see one entry, the real Homebrew one. If another program is
earlier in your PATH, or `brew` is an alias or a shell function you didn't
write, find out why.

## 11. Other things worth knowing

- Downloads still come from `ghcr.io`, `github.com` and the upstream project's
  own server. Those servers see your IP address and what you fetch. Homebrew has
  no way to hide that. A mirror set with `HOMEBREW_ARTIFACT_DOMAIN` can help, but
  read the docs for the fallback behaviour first.
- Use `HOMEBREW_FORBIDDEN_FORMULAE` and `HOMEBREW_FORBIDDEN_CASKS` to block
  names you never want installed.

## What this doesn't cover

If malware already runs as your user, it can change your shell profile, your
PATH and Homebrew itself. Nothing here stops that. Keep your system updated and
don't run things you don't trust.

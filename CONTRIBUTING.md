# Contributing

Thanks for looking at brew-guard. It is a one person project and it is a
security tool, so some rules are strict. Please read this before you open a pull
request.

## Before you start

- For anything bigger than a small fix, open an issue first and say what you
  want to change. It saves you work if I'd say no.
- A security problem is not an issue. See [SECURITY.md](SECURITY.md).

## Rules that are not negotiable

- **No dependencies.** brew-guard uses only the Go standard library. A pull
  request that adds a module, a vendored copy or a new GitHub Action will be
  closed. If you think something is needed, open an issue and make the case.
  Versions of everything we do use are pinned in
  [docs/PINNED-VERSIONS.md](docs/PINNED-VERSIONS.md).
- **Fail closed.** When something is unknown (a release date, a tap, a line of
  brew output we can't read) the answer is "block", not "allow".
- **No guessing about brew.** Parsers read real brew output. If you add one, add
  a fixture recorded from a real brew run and say which version and system it
  came from.
- **Nothing runs without being checked.** The wrapper must not let brew install,
  upgrade, clean up or remove something that the policy didn't see.
- **No secrets in the repo.** Tokens come from the environment only.
- **No telemetry.** Any new network endpoint has to be listed in the README.

## Build and test

You need the Go version pinned in `go.mod`.

```
gofmt -l .
go vet ./...
go test ./...
```

`gofmt -l .` must print nothing. Tests must pass, and new behaviour needs a test.
Don't test against your own Homebrew: use the fakes in the existing tests, or
a throwaway container.

## Pull requests

- One change per pull request, as small as it can be.
- Describe what changed and why. If it changes what brew-guard allows or
  blocks, say so clearly.
- Update the README if the behaviour or a command changes.
- Commit messages look like the existing ones: `feat(wrap): ...`,
  `fix(check): ...`, `docs: ...`. First line in the present tense, short.

## Releases

Only I tag releases. The build is reproducible, see the "Releases" section of
the README.

By contributing you agree that your work is under the [MIT licence](LICENSE).
Everyone taking part follows the [code of conduct](CODE_OF_CONDUCT.md).

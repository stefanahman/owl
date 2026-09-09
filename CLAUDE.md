# owl — agent notes

owl is the launcher for review and feature workspaces (README.md). It
ships together with four sibling repos, each with a CLAUDE.md like this
one: [mux](https://github.com/stefanahman/mux), the multiplexer drivers
owl imports; [spaces](https://github.com/stefanahman/spaces), the
desktop spaces and hotkeys owl's popup lives in;
[claude-status](https://github.com/stefanahman/claude-status), the
Claude Code hooks that write the agent's state owl reads back;
[mcp-defer](https://github.com/stefanahman/mcp-defer).

A change is not done when the code compiles. It is done when it is
committed in coherent pieces, pushed with CI green, installed where the
shell finds it, documented, and — at a milestone — tagged so the cask
other machines install catches up. Do all of it, in this order, and
say which steps you did and which you left.

## 1. Build and test

```sh
make test      # go test . and the e2e suite (needs tmux; all hermetic)
make lint      # gofmt, go vet
go run honnef.co/go/tools/cmd/staticcheck@2026.2.1 ./...   # CI's analysis job
```

Check exit codes, not output: `go test ./... | tail` hides a failure
behind tail's exit 0. After a deliberate UI change, `make
update-snapshots` and look at the PNGs in e2e/testdata. Behaviour
comes with a test; the three layers are in README.md, Hacking.

## 2. Commit

Conventional commits, lower-case subject, a body that says why in the
README's voice — `feat(issue): a row shows every open PR whose branch
carries its key`. Scopes in use: `pr`, `issue`, `plugin`, `config`,
`cmux`, `herdr`, `tmux`, `e2e`. Smallest coherent commits, dependencies
before consumers, refactor before feature; every commit compiles with
its tests — `git rebase --exec 'go vet ./...' <base>` proves it, run on
a clean tree and finished before anything else: `git tag` marks HEAD,
and a stopped rebase leaves HEAD on the wrong commit. No Co-Authored-By
trailers.

## 3. Push, and install the checkout build

```sh
git push origin main
make install BIN=~/.eden/bin     # the checkout build, versioned by git describe
```

CI (`.github/workflows/ci.yml`): `go` (lint, test), `macos` (the e2e
suite on a real tmux), `analysis` (staticcheck, govulncheck), `plugin`
(`claude plugin validate`). `gh run list --workflow ci --branch main
--limit 1` shows it. The macOS e2e waits thirty seconds for the child
on a cold runner; one failure followed by a pass on the next commit is
the runner, not the code.

On Stefan's machine a login shell puts `/opt/homebrew/bin` before
`~/.eden/bin`, so `owl` in a terminal is the cask until the next
release, while hotkeys and spaces prefer the checkout. `zsh -lc
'command -v owl; owl --version'` says which answers. `field X not
found in type main.Config` after a config change is the cask being
older than the config.

## 4. Document

README.md is the front page — facts as tables, one voice; docs/ holds
the depth (multiplexers.md); owl/README.md is the plugin. The config
template lives in config.go (`owl config init`) and, abridged, in the
README's Configuration block: keys and defaults in step
(TestTemplateMatchesDefaults checks the code side). A new or changed
skill bumps `owl/.claude-plugin/plugin.json` and its line in
`.claude-plugin/marketplace.json`; installed copies follow with
`claude plugin marketplace update owl`.

## 5. Ship at a milestone

Not after every change: tag when a feature is complete, at the end of
a chunk of work, or when asked, batching what landed since the last
tag. Before tagging: tree clean, no rebase in progress, HEAD pushed,
CI green at HEAD.

```sh
git tag -a v0.8.4 -m "owl 0.8.4: <the batch, one line>"
git push origin v0.8.4
```

The tag runs `release.yml`: goreleaser builds macOS and Linux binaries,
publishes the GitHub release and rewrites the cask in
stefanahman/homebrew-tap. Then, in the background so the next step is
not blocked:

```sh
gh run list --workflow release --branch v0.8.4 --json status,conclusion   # until completed success
brew update && brew upgrade --cask owl
/opt/homebrew/bin/owl --version           # the cask is what other machines get
```

Patch for fixes and additions, minor for a new command or config key
(a removed key gets a migration error naming the new place — see
parseConfig), major not yet.

A change in mux comes first: tag mux, then here `GOPROXY=direct go get
github.com/stefanahman/mux@vX.Y.Z && go mod tidy`, one commit `build:
mux vX.Y.Z — <why>`, and ship owl.

## Gotchas that cost a day

- The cask shadows the checkout in login shells (section 3).
- A `go test … | tail -1` printed FAIL and the commit went through:
  the pipeline's status is tail's.
- A tag made while a rebase was stopped pointed at the wrong commit;
  the release had to be cancelled, the tag moved, the run repeated.
- A prompt typed into a Claude waiting at a dialog answers the dialog.
  That is why `f` is refused while the state is blocked, and why the
  state comes from Claude Code's hooks (claude-status), never from a
  multiplexer's guess — cmux counts the idle reminder as waiting.

# Install

```sh
brew install --cask stefanahman/tap/owl        # macOS
go install github.com/stefanahman/owl@latest   # anywhere with Go 1.25
```

Prebuilt binaries for macOS and Linux (amd64, arm64) are on the
[releases page](https://github.com/stefanahman/owl/releases); from a
checkout, `make install BIN=~/.local/bin`.

Then the plugin, inside a `claude` session — it is the default first
prompt of a review and of a feature:

```
/plugin marketplace add stefanahman/owl
/plugin install owl@owl
```

| needs | why |
|---|---|
| git, and [`gh`](https://cli.github.com) authenticated for the repo's host | every read and fetch goes through `gh`; GitHub.com is what it is used with, a GitHub Enterprise host should work but is untested |
| [Claude Code](https://docs.claude.com/en/docs/claude-code) on a POSIX-ish shell your multiplexer runs (sh, bash, zsh, fish ≥ 3.4) | the start line is typed into that shell, and reads the prompt back with `"$(cat …)"` — the prompt itself is written to `$XDG_STATE_HOME/owl/prompts/<workspace>.md` (mode 0600), so one of any size arrives whole. A prompt sent to an agent *already running* (`--prompt` on an open workspace) is still keystrokes, so keep that one short |
| one of tmux (≥ 3.2 for the popup), herdr ≥ 0.9, cmux ≥ 0.64 (macOS) | where the workspaces live |
| a Linear API key, for `owl issue` only | see [Features](features.md) |

macOS and Linux. On Linux `o` opens with `xdg-open`; `y` copies through
OSC 52, the terminal escape for the clipboard, which most terminals
support.

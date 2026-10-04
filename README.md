# owl

TUI that opens any PR, issue or project as a git worktree with Claude Code.

[![ci](https://github.com/stefanahman/owl/actions/workflows/ci.yml/badge.svg)](https://github.com/stefanahman/owl/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/stefanahman/owl)](https://github.com/stefanahman/owl/releases)
[![license](https://img.shields.io/github/license/stefanahman/owl)](LICENSE)

owl lists the pull requests waiting for your review, the Linear issues
waiting for your hands, and the projects they belong to. Press Enter on
a row and it becomes a workspace: a git worktree, a window in your
multiplexer (tmux, herdr or cmux), and Claude Code inside it, on the
review, the feature or the project.

```
owl · acme/app                                              updated just now
5 open · 1 todo · 1 you · 2 author · 1 approved · 1 merged (1d)
────────────────────────────────────────────────────────────────────────────
Todo
▸ #3543  ⎇ ©*      2h  add billing migration (alice)
Waiting for you
  #3491  ⎇ ©  ·    1d  split ingestion worker (carol)
Waiting for author
  #3510       · ⚠  8h  retry on 429 (erin)
  #3550       ·    5h  fix retry ordering (bob)
Approved
  #3502       ✓    3d  bump node to 22 (dave)
Merged (last 3d)
  #3488            6h  remove legacy flag (erin)
```

owl stores nothing of its own. The branch carries the ticket, the pull
request carries the review, the window carries the agent; the lists
read all of it back from GitHub, Linear and the multiplexer, so nothing
is written twice and nothing goes stale in a second place.

- **Three lists, one shape.** `owl pr` groups pull requests by where
  you sit on them: todo, waiting for you, waiting for the author,
  approved, merged. `owl issue` groups your Linear issues by state.
  `owl project` holds the projects they belong to. Same keys, same
  badges.
- **Workspaces on demand.** Enter fetches the branch into a worktree,
  opens a window and starts Claude on a first prompt. `c` removes all
  three; the conversation stays on disk, so the next Enter resumes it.
- **The agent's state in the list.** `©` shows whether Claude is
  working, blocked on you, or done and unread. `n` jumps to the next
  row that needs you.
- **Prompts on a key.** `f` sends the feedback prompt: on a review,
  check what changed since your last review; on an issue, go through
  the review you were given. `bindings` add your own prompts and URLs.
- **Skills with manners.** The bundled [plugin](owl/README.md) gives
  the agent its process: `review` never posts without you, `feature`
  never pushes without you. Any first prompt works without it.
- **Three multiplexers, one config.** tmux, [herdr](https://github.com/herdrdev/herdr)
  and [cmux](https://github.com/manaflow-ai/cmux); owl finds the one
  it runs in.

## Install

```sh
brew install --cask stefanahman/tap/owl
go install github.com/stefanahman/owl@latest   # with Go 1.25
```

Then the plugin, inside a `claude` session:

```
/plugin marketplace add stefanahman/owl
/plugin install owl@owl
```

owl needs git, an authenticated [`gh`](https://cli.github.com), Claude
Code, and one of tmux, herdr (0.9 or later) or cmux (0.64 or later).
`owl issue` also needs a Linear API key. [docs/install.md](docs/install.md) has the
details and the prebuilt binaries.

## Quick start

```sh
gh auth login           # once
owl config init         # optional: the config with every key explained
cd ~/src/app            # a clone whose remote is on GitHub
owl pr                  # in a terminal inside tmux, herdr or cmux
```

Enter on someone else's PR fetches it into `.worktrees.local/pr-<N>-<slug>`,
opens a window for it — under tmux the `reviews` session is created on
first use — starts Claude there with `/owl:review <N>`, and takes you
to it. Back in the list, the `©` badge follows the agent; `f` sends the
feedback prompt once the author has pushed; `c` removes worktree,
branch and window when you are done.

Under tmux, bind the list to a popup so it is a keystroke away:

```tmux
bind r display-popup -E -w 88% -h 84% owl pr
```

Set `default_repo` in the config to start owl from anywhere. Outside a
multiplexer, `open` prints `attach with: …` instead of taking you
there.

## Docs

- [Reviews](docs/reviews.md): `owl pr`, its two panes, your own PRs,
  and `owl pr --check` for news while the list is closed
- [Features](docs/features.md): `owl issue`, and how an issue finds its
  branch
- [Projects](docs/projects.md): `owl project` and the project
  conversation
- [Keys](docs/keys.md) and [Commands](docs/commands.md)
- [Configuration](docs/configuration.md): every key, with defaults
- [How it works](docs/how-it-works.md): worktree, window,
  conversation
- [Multiplexers](docs/multiplexers.md): how tmux, herdr and cmux differ
- [The plugin](owl/README.md): the review, feature and dependabot
  skills

## Related

[gh-dash](https://github.com/dlvhdr/gh-dash) is the dashboard: every
PR and issue you care about, in one TUI. owl is the launcher: the rows
that are yours to act on, each one keystroke from a worktree with an
agent in it. [lazygit](https://github.com/jesseduffield/lazygit) is
where the worktree's git work happens once you are there. Claude
Code's own `--worktree` makes a worktree for a session; owl's are
found by name, so the two meet.

owl began as pr-owl, a watcher of pull requests. When the issues came,
the bird kept the name and the noun moved into the command.

See also: [spaces](https://github.com/stefanahman/spaces) ·
[mux](https://github.com/stefanahman/mux) ·
[mcp-defer](https://github.com/stefanahman/mcp-defer) ·
[claude-status](https://github.com/stefanahman/claude-status) ·
[mindoro](https://github.com/stefanahman/mindoro) ·
[eden](https://github.com/stefanahman/eden)

## License

MIT

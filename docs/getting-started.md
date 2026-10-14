# Getting started with herdr or cmux

From nothing to a first review, for someone whose terminal runs
[herdr](https://github.com/herdrdev/herdr) or
[cmux](https://github.com/manaflow-ai/cmux). Under tmux the README's
quick start is shorter.

## 1. Install

```sh
brew install --cask stefanahman/tap/owl         # macOS; Linux: the releases page, or go install
gh auth login                                   # once: owl reads GitHub through gh
gh auth setup-git                               # once: owl fetches with git, and this lends it gh's credentials
claude plugin marketplace add stefanahman/owl   # the plugin that gives the agent its review and feature procedures
claude plugin install owl@owl --scope user      # for every repository
```

| needs | for |
|---|---|
| herdr 0.9 or later, or cmux 0.64 or later (macOS); cmux 0.65.0 or later to see an idle agent as idle | where the workspaces live |
| git, `gh` signed in with access to your repositories | every list and every fetch |
| [Claude Code](https://docs.claude.com/en/docs/claude-code) | the agent in each workspace |
| a Linear personal API key (Linear: Settings → Account → Security & access) | `owl issue` and `owl project` only; reviews need none |

[claude-status](https://github.com/stefanahman/claude-status) is for
tmux. herdr and cmux report the agent's state themselves.

## 2. Configure

```sh
owl config init        # writes ~/.config/owl/config.yaml, every key explained
```

Most of it can stay as it is. Edit these keys where `config init` put
them: they are already in the file, and a second copy appended below
breaks it.

```yaml
default_repo: ~/src/app                    # the repository owl uses when started outside one
linear:
  token: $LINEAR_API_KEY                   # or op://<vault>/<item>/<field>, or file://~/.config/owl/linear.token (mode 600)
  team: PROJ                               # the key in PROJ-123
bindings:
  pr:
    - key: l                               # l on a review opens the issue it names
      name: Linear
      pattern: 'PROJ-\d+'
      url: https://linear.app/<workspace>/issue/{id}
```

A binding with `{id}` in its URL is written in block form, as above, or
with the URL quoted: unquoted on one line between `{ }`, the braces
break the YAML. `<workspace>` is your Linear workspace's URL key, the
part after `linear.app/` in any issue link. `agent.cmd` is where your
own `claude` flags go. [Configuration](configuration.md) has every key.

## 3. Start the multiplexer from a plain shell

Start herdr or cmux from a terminal or a launcher, never from a shell
inside Claude Code. A multiplexer started there keeps Claude Code's
marker in its environment, and every agent owl starts in it runs as a
child session that saves no transcript. Start cmux from outside tmux
too: launched with `TMUX` in its environment, its shell integration
talks to tmux instead. owl refuses either and says how to fix it.

cmux: run owl in a cmux terminal. cmux admits only processes started
inside it, unless `"automation.socketControlMode": "allowAll"` is in
`~/.config/cmux/cmux.json` or the app was started with
`CMUX_SOCKET_MODE=allowAll`.

## 4. A workspace for each list

Make one workspace per list, in your repository, and leave the list
running in it:

| workspace | runs |
|---|---|
| `prs` | `owl pr`: what waits for your review, and your own PRs below |
| `issues` | `owl issue`: your Linear issues |
| `projects` | `owl project`: the Linear projects you work in |

```sh
herdr workspace create --label prs --cwd ~/src/app    # herdr; then run owl pr in it
cmux workspace create --name prs --cwd ~/src/app      # cmux
```

Under herdr and cmux a list stays open after `↵`, so this workspace is
the launcher: come back to it, pick the next row.

## 5. A first review

In `prs`, `↵` on someone else's PR fetches it into
`.worktrees.local/pr-<N>-<slug>`, opens a workspace for it and starts
Claude there on `/owl:review <N>`. The review posts nothing until you
approve the exact draft file it writes; when the PR changes the shape
of the system it also stops earlier, at a design brief.

Back in the list, `©` follows the agent: yellow working, amber waiting
for you, green done, `*` until you look. `f` asks the agent what changed
since your last review, once the author has pushed. `c` removes the
worktree, its branch and its workspace; the conversation stays, so the
next `↵` resumes it. `?` lists every key.

**If every idle agent shows amber under cmux**, that cmux counts
Claude's reminder a minute after each turn as "needs input". cmux 0.65.0
fixed it: update cmux.

## 6. Your repository's settings

The review skill reads `.claude/owl.md` in the repository: the extra
checks to load (Linear tickets, a pnpm and turbo build, MongoDB,
service boundaries), the ticket pattern, and what to look for there.
When your team has committed one, there is nothing to do. Your own
additions go in `.claude/owl.local.md`; add `.claude/*.local.md` to the
repository's `.gitignore` if it is not there. The [plugin's
README](../owl/README.md) has the format.

The `linear` check, when the file's `modules` lists it, reads tickets
through a Linear MCP server in Claude Code, and skips itself silently
without one:

```sh
claude mcp add --scope user --transport http linear https://mcp.linear.app/mcp   # for every repository
```

Then authenticate it once: `/mcp` in a `claude` session, and pick
`linear`. Until then the check skips, and a review shows no tickets and
no error.

## Where to read on

[Reviews](reviews.md) for the lists, the badges, your own PRs and stacks;
[Keys](keys.md); [Multiplexers](multiplexers.md) for how each one is
driven; [Configuration](configuration.md).

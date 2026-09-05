# pr-owl

A terminal UI of the PRs waiting for your review — and one key to turn
any of them into a review workspace: a git worktree, a tmux window, and
Claude Code reviewing inside it. Run it in a shell, or bind it to a
tmux popup so it's one keystroke away from any session.

```
 acme/app · 3 todo · 1 waiting for you · 2 approved      updated 40s ago

 Todo
▸ #3543  ⎇  ©*  ✓  ⚠   add billing migration            alice     2h
  #3550                 fix retry ordering               bob       5h
 Waiting for you
  #3491  ⎇  ©      ·   split ingestion worker           carol     1d
 Approved
  #3502      ✓         bump node to 22                  dave      3d
 Merged (last 24h)
  #3488                 remove legacy flag               erin      merged 6h
```

- Rows are grouped by **where you sit on the PR** — todo, waiting for
  you (the author pushed after your last review), waiting for the
  author, approved — plus what merged in the last day so a PR that
  merged without your review doesn't vanish unseen.
- `⎇` a worktree exists for it, `©` a Claude session is running in its
  tmux window — coloured by what Claude is doing (working / blocked on
  you / done, with `*` until you look) — `✓` you approved, `·` you
  engaged, `⚠` someone requested changes.
- **Enter** opens the review: `pr-owl open` fetches the PR into
  `<repo>/.worktrees.local/pr-<N>-<slug>`, creates a window in the
  `pr-reviews` tmux session and starts Claude there with the
  [pr-review skill](pr-review/README.md). **f** sends a "check the
  feedback since your last review" prompt to that session — resuming
  the conversation first if you had closed the workspace. **c** closes
  the workspace: worktree, branch and window go; the conversation on
  disk stays, so the next Enter resumes it.

GitHub only — everything goes through `gh`.

## Install

```sh
go install github.com/stefanahman/pr-owl@latest
```

or from a checkout, `make install BIN=~/.local/bin`. Needs git, an
authenticated `gh`, and tmux ≥ 3.2 (only for the popup; `open` itself
works with any tmux). Go 1.25 to build. Linux: `xdg-open` for `o`,
xclip/xsel/wl-clipboard for `y`.

`pr-owl` opens the TUI for the repo of the current directory; set
`default_repo` in the config to launch it from anywhere. To have it a
keystroke away inside tmux, bind a popup in `tmux.conf`:

```tmux
bind r display-popup -E -w 88% -h 84% pr-owl
```

(tmux runs that with the server's PATH — give the absolute path if
`pr-owl` isn't on it.) From a plain terminal it works the same; `open`
then tells you how to attach to the review session.

Two optional companions:

- [tmux-claude-status](https://github.com/stefanahman/tmux-claude-status)
  writes the `©` state pr-owl shows (and puts the same chips in your
  status bar). Without it the badge only says "a session exists".
- [pr-review](pr-review/README.md), the Claude Code plugin with the
  review skill the workspace starts with:
  `/plugin marketplace add stefanahman/pr-owl`, then
  `/plugin install pr-review@pr-owl`.

## Keys

| key | action |
|---|---|
| `↓`/`j` `↑`/`k` `g` `G` `pgup` `pgdn` | move; section headers are skipped |
| `n` | jump to the next PR that needs you (Claude blocked or done) |
| `↵` | open (or focus) the review workspace, close the popup |
| `f` | send the check-feedback prompt to the PR's Claude session, close the popup |
| `o` | open the PR in the browser |
| `y` | copy the PR URL |
| `c` | close the workspace — worktree, branch and tmux window; uncommitted changes in the worktree are discarded |
| `/` | filter by PR number; `esc` clears |
| `r` | refresh |
| `?` | help, with the full badge legend |
| `q` | quit |

Every key is rebindable, and `links` add your own (below).

## Commands

```
pr-owl                          the TUI
pr-owl open <N> [--prompt TEXT] open (or focus) PR N's workspace; with --prompt, hand the prompt to the agent
pr-owl close [<N>]              remove the worktree, branch and window; N is inferred from inside a workspace
pr-owl config init | path | get <key>
```

`open` is idempotent: it creates what is missing and selects the
window. A workspace is named once, from the PR title at first open, and
found by number afterwards — the path stays stable, and with it
Claude's per-directory conversation, which is what lets `close` be
cheap and `open` resume. The first `open` in a clone also adds the
worktrees directory to `.git/info/exclude`, so `git status` stays clean
without touching the project's `.gitignore`.

`close` exits 2 when there was nothing to remove.

Fork-based workflow (`origin` is your fork, `upstream` the repo the PRs
are on)? Set `remote: upstream`: PRs are listed for, and fetched from,
that remote.

## Configuration

`~/.config/pr-owl/config.yaml` (`$XDG_CONFIG_HOME` and `$PR_OWL_CONFIG`
respected). `pr-owl config init` writes the annotated template; every
key is optional.

```yaml
tmux:
  session: pr-reviews            # one window per review lives here
  keepalive_window: scratch      # keeps the session alive with no reviews open
  state_option: "@claude-state"  # window option written by tmux-claude-status

remote: origin                   # the GitHub remote: PRs are listed for it and fetched from it
worktrees_dir: .worktrees.local  # relative to the repo root
default_repo: ""                 # used when pr-owl starts outside a git repo

agent:
  cmd: claude --permission-mode auto   # -c is appended when the worktree has a prior conversation
  prompt: "/pr-review:pr-review {pr}"  # first prompt of a fresh review
  feedback_prompt: "Please carefully check the feedback since your last review …"  # what f sends
  link_local:                          # symlinked from the repo into each new worktree (keep them gitignored there)
    - .claude/settings.local.json
    - .claude/*.local.md
    - .claude/skills/*.local

open_cmd: ""                     # opens URLs; default: open (macOS) or xdg-open

hooks:
  after_open: ""                 # runs after every open with PR_OWL_PR, _SESSION, _WINDOW, _WORKTREE, _REPO set

theme:                           # the three Claude-state colours (ANSI 0-255 or #rrggbb)
  working: "#dbbc7f"
  blocked: "214"
  done: "42"

keys:                            # rebind any action: a key name or a list
  open: enter
  feedback: f
  quit: [q, ctrl+c]

links:                           # your own keys, each opening a URL built from the PR
  - key: l
    name: Linear
    pattern: 'PROJ-\d+'          # {id} is the first match in title, body and branch
    url: https://linear.app/my-org/issue/{id}
  - key: b
    name: CI
    url: https://ci.example.com/{repo}/pr/{pr}
```

Link placeholders: `{pr}`, `{repo}` (owner/name), `{branch}`, `{url}`
(the PR page) and `{id}`.

`hooks.after_open` is where window-manager glue goes; see
[contrib/macos](contrib/macos/README.md) for the yabai + Ghostty setup
that keeps every review in one terminal window on its own space, with
a hotkey that reaches the popup from anywhere.

## The workspace model

| layer | what | owner |
|---|---|---|
| git worktree | `<repo>/<worktrees_dir>/pr-<N>-<slug>` on branch `pr-<N>-<slug>`, fetched from `pull/N/head` | `open` creates, `close` removes |
| tmux window | same name, in `tmux.session`, cwd = the worktree, running `agent.cmd` | `open` creates, `close` kills |
| conversation | Claude's transcript for that directory | survives `close`; `open` resumes it with `-c` |

Re-running `open` never re-fetches: the worktree is yours once it
exists. When the author pushes, `git pull origin pull/<N>/head` inside
it.

## Hacking

```sh
make test     # go test ./... — the open/close tests need tmux
make lint     # gofmt, go vet, shellcheck
PR_OWL_SMOKE=1 go test -run TestFetchSmoke ./...   # against your real gh, from a repo checkout
```

The tests run against a throwaway git origin, a fake `gh`, a private
tmux server and an empty `CLAUDE_CONFIG_DIR`; nothing touches your
sessions or config.

## License

MIT

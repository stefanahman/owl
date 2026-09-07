# pr-owl

A terminal UI of the PRs waiting for your review — and one key to turn
any of them into a review workspace: a git worktree, a tmux window, and
Claude Code reviewing inside it. Run it in a shell, or bind it to a
tmux popup so it's one keystroke away from any session.

```
pr-owl · acme/app                                           updated just now
5 open · 1 todo · 1 you · 2 author · 1 approved · 1 merged (1d)
────────────────────────────────────────────────────────────────────────────
Todo
▸ #3543  ⎇ ©*     add billing migration (alice) — 2h
Waiting for you
  #3491  ⎇ ©  ·   split ingestion worker (carol) — 1d
Waiting for author
  #3510       · ⚠ retry on 429 (erin) — 8h
  #3550       ·   fix retry ordering (bob) — 5h
Approved
  #3502       ✓   bump node to 22 (dave) — 3d
Merged (last 1d)
  #3488           remove legacy flag (erin) — merged 6h
```

- Rows are grouped by **where you sit on the PR** — todo, waiting for
  you (the author pushed after your last review), waiting for the
  author, approved — plus what merged in the last day so a PR that
  merged without your review doesn't vanish unseen.
- `⎇` a worktree exists for it, `©` a Claude session is running in its
  tmux window — coloured by what Claude is doing (working / blocked on
  you / done, with `*` until you look) — `✓` you approved, `·` you
  engaged (both amber when the author pushed after that review), `⚠`
  someone requested changes.
- The list comes back as you left it: the last fetch from a per-repo
  cache until the live one lands, with the cursor on the row it was
  on when you quit (the row, not the PR).
- **Enter** opens the review: `pr-owl open` fetches the PR into
  `<repo>/.worktrees.local/pr-<N>-<slug>`, creates a window in the
  `pr-reviews` tmux session and starts Claude there with the
  [pr-review skill](pr-review/README.md). **f** sends a "check the
  feedback since your last review" prompt to that session — resuming
  the conversation first if you had closed the workspace. **c** closes
  the workspace: worktree, branch and window go; the conversation on
  disk stays, so the next Enter resumes it.

GitHub.com only — everything goes through `gh`; GitHub Enterprise
hosts are not supported.

## Install

```sh
brew install --cask stefanahman/tap/pr-owl        # macOS
go install github.com/stefanahman/pr-owl@latest   # anywhere with Go 1.25
```

Prebuilt binaries for macOS and Linux (amd64, arm64) are on the
[releases page](https://github.com/stefanahman/pr-owl/releases); from a
checkout, `make install BIN=~/.local/bin`. Needs git, an authenticated
`gh`, tmux (any version for `open` and `close`, ≥ 3.2 for the popup) and
[Claude Code](https://docs.claude.com/en/docs/claude-code), the agent
`open` starts and resumes. Windows is not supported (no tmux). Linux:
`xdg-open` for `o`; `y` copies through OSC 52, which most terminals
support.

`pr-owl` opens the TUI for the repo of the current directory; set
`default_repo` in the config to launch it from anywhere. To have it a
keystroke away inside tmux, bind a popup in `tmux.conf`:

```tmux
bind r display-popup -E -w 88% -h 84% pr-owl
```

(tmux runs that with the server's PATH — give the absolute path if
`pr-owl` isn't on it.) Without a popup, set `on_open` so the TUI doesn't
quit after opening a review: `switch` in a tmux window (it moves your
client to the review session), `stay` in a plain terminal.

Three companions, each optional:

- [tmux-claude-status](https://github.com/stefanahman/tmux-claude-status)
  writes the `©` state pr-owl shows (and puts the same chips in your
  status bar). Optional: without it the badge only says "a session
  exists".
- [pr-review](pr-review/README.md), the Claude Code plugin whose review
  skill is the default `agent.prompt` of a fresh workspace:
  `/plugin marketplace add stefanahman/pr-owl`, then
  `/plugin install pr-review@pr-owl`. Without it, set `agent.prompt` to
  the first prompt a review should start with.
- [tmux-spaces](https://github.com/stefanahman/tmux-spaces) keeps the
  review session in one terminal window on its own desktop space and
  opens the popup from a hotkey anywhere (macOS, yabai, Ghostty);
  `hooks.after_open: tmux-spaces focus pr-reviews` brings that window
  to the front after every open.

## Keys

| key | action |
|---|---|
| `↓`/`j` `↑`/`k` `g` `G` `pgup` `pgdn` | move; section headers are skipped |
| `n` | jump to the next PR that needs you (Todo, or Claude blocked or done) |
| `↵` | open (or focus) the review workspace; then `on_open` |
| `f` | send the check-feedback prompt to the PR's Claude session (refused while Claude is blocked on a question or a permission there); then `on_open` |
| `o` | open the PR in the browser |
| `y` | copy the PR URL |
| `c` | close the workspace — worktree, branch and tmux window; refused while tracked files have uncommitted changes (`pr-owl close --force <N>` discards them) |
| `/` | filter by PR number; `esc` clears |
| `r` | refresh |
| `?` | help, with the full badge legend |
| `q` | quit |

Every key is rebindable, and `links` add your own (below).

## Commands

```
pr-owl                          the TUI
pr-owl open <N> [--prompt TEXT] open (or focus) PR N's workspace; with --prompt, hand the prompt to the agent
pr-owl close [--force] [<N>]    remove the worktree, branch and window of the current repo; N is inferred from inside a workspace; --force discards uncommitted changes
pr-owl config init | path
```

`open` is idempotent: it creates what is missing and selects the
window. A workspace is named once, from the PR title at first open, and
found by number afterwards — after `close`, by the name Claude's
conversation is stored under — so the path stays stable even if the PR
is retitled, and with it Claude's per-directory conversation, which is
what lets `close` be cheap and `open` resume. The first `open` in a clone also adds the
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

remote: origin                   # the GitHub remote: PRs are listed for it and fetched from it
worktrees_dir: .worktrees.local  # relative to the repo root
default_repo: ""                 # used when pr-owl starts outside a git repo

agent:
  cmd: claude --permission-mode auto   # Claude Code, with your flags (e.g. --model claude-opus-5); -c is appended when the worktree has a prior conversation
  prompt: "/pr-review:pr-review {pr}"  # first prompt of a fresh review
  feedback_prompt: "Please carefully check the feedback since your last review …"  # what f sends
  link_local:                          # symlinked from the repo into each new worktree (keep them gitignored there)
    - .claude/settings.local.json
    - .claude/*.local.md
    - .claude/skills/*.local

open_cmd: ""                     # opens URLs; default: open (macOS) or xdg-open
on_open: quit                    # the TUI once a review is open: quit (popup), stay, or switch (tmux switch-client to the review session)

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

The default `agent.cmd` runs Claude with `--permission-mode auto` inside
a checkout the PR's author controls, and `link_local` never replaces a
file the PR ships under the same name — so a PR's own `.claude/` is
what the agent starts with. Read that part of the diff first when it
matters.

Two hooks, two layers: `on_open` is what the TUI itself does;
`hooks.after_open` is a shell command run after *every* `open` (TUI or
CLI), where window-manager glue goes — `tmux-spaces focus pr-reviews`,
for one. The review session is a normal tmux session you can attach to
or switch to from anywhere.

## The workspace model

| layer | what | owner |
|---|---|---|
| git worktree | `<repo>/<worktrees_dir>/pr-<N>-<slug>` on branch `pr-<N>-<slug>`, fetched from `pull/N/head` | `open` creates, `close` removes |
| tmux window | same name, in `tmux.session`, cwd = the worktree, running `agent.cmd` | `open` creates, `close` kills |
| conversation | Claude's transcript for that directory | survives `close`; `open` resumes it with `-c` |

A prompt (`f`, `open --prompt`) is typed into the window as one line
of keystrokes. If the agent has exited — the window is back at sh,
bash, zsh, fish, dash, ksh or nu — the agent is started again with `-c`
and the prompt instead; while Claude is blocked on a question or a
permission there, the prompt is refused, since the keystrokes would
answer that dialog.

Re-running `open` never re-fetches: the worktree is yours once it
exists. When the author pushes, `git pull origin pull/<N>/head` inside
it (`origin`, or the `remote` you configured). pr-owl works on one repo
at a time — the working directory's, or `default_repo` — and the review
session is one per machine; window names carry the PR number, not the
repo, so keep one repo per review session.

## Hacking

```sh
make test               # go test ./... — the open/close tests need tmux
make lint               # gofmt, go vet
make update-snapshots   # after a deliberate UI change: the screen snapshots
PR_OWL_SMOKE=1 go test -run TestFetchSmoke .   # against your real gh, from a repo checkout
```

Three test layers, all hermetic (throwaway git origin, fake `gh`, a
private tmux server, empty `CLAUDE_CONFIG_DIR` — nothing touches your
sessions or config):

- `ui_test.go` drives the Bubble Tea model with teatest and asserts on
  what the frames say.
- `workspace_test.go` runs `open`/`close` for real against tmux and git.
- `e2e/` runs the built binary in a virtual terminal (`x/vttest`) and
  snapshots the screen as JSON — plus a PNG next to it, so a UI change
  shows up as a picture in the PR.

## License

MIT

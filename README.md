# pr-owl

A terminal UI of the PRs waiting for your review — and one key to turn
any of them into a review workspace: a git worktree (a second checkout
of the repo, on the PR's branch), a window in your terminal multiplexer
(tmux, herdr or cmux), and Claude Code reviewing inside it. Run it in a
shell, bind it to a tmux popup, or run it in a herdr or cmux workspace,
so it's one keystroke away from any session.

```
pr-owl · acme/app                                           updated just now
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
Merged (last 1d)
  #3488            6h  remove legacy flag (erin)
```

- Rows are grouped by **where you sit on the PR** — todo, waiting for
  you (the author pushed after your last review), waiting for the
  author, approved — plus what merged in the last day so a PR that
  merged without your review doesn't vanish unseen.
- `⎇` a worktree exists for it, `©` a Claude session is running in its
  tmux window — coloured by what Claude is doing (working / blocked on
  you / done, with `*` until you look), followed every two seconds —
  `✓` you approved, `·` you
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

Everything goes through `gh`, which must be authenticated for the
host of the repo's remote. GitHub.com is what it is used with; a
GitHub Enterprise host should work the same way but is untested.

## Install

```sh
brew install --cask stefanahman/tap/pr-owl        # macOS
go install github.com/stefanahman/pr-owl@latest   # anywhere with Go 1.25
```

Prebuilt binaries for macOS and Linux (amd64, arm64) are on the
[releases page](https://github.com/stefanahman/pr-owl/releases); from a
checkout, `make install BIN=~/.local/bin`. Needs git, an authenticated
`gh`, a multiplexer — tmux (any version for `open` and `close`, ≥ 3.2
for the popup), [herdr](https://github.com/herdrdev/herdr) ≥ 0.9 or
[cmux](https://github.com/manaflow-ai/cmux) ≥ 0.64 (macOS) — and
[Claude Code](https://docs.claude.com/en/docs/claude-code), the agent
`open` starts and resumes, on the PATH of the shell your multiplexer
runs (the start line is typed into that shell). Windows is not
supported. Linux: `xdg-open` for `o`; `y` copies through OSC 52 (the
terminal escape for the clipboard), which most terminals support.

## First review

1. `gh auth login`, and `claude` once, so both are set up.
2. Inside a `claude` session: `/plugin marketplace add stefanahman/pr-owl`,
   then `/plugin install pr-review@pr-owl`. The default first prompt of
   a review is that plugin's `/pr-review:pr-review` skill; without the
   plugin, set `agent.prompt` to the prompt a review should start with.
3. `pr-owl config init` writes the config with every key explained.
   Nothing in it is required; `default_repo` lets you start pr-owl
   from anywhere.
4. `cd` into a clone whose remote is on GitHub, in a terminal that runs
   inside tmux, herdr or cmux (or any terminal, with `on_open: stay`).
5. `pr-owl`, then Enter on a row. That fetches the PR into a worktree
   under `.worktrees.local/`, opens a window for it in your
   multiplexer — under tmux the `pr-reviews` session is created on
   first use — types the Claude Code start line into it, and takes you
   there. Outside the multiplexer it prints `attach with: …` instead.
6. Back in the list, the `©` badge follows the agent (legend under
   `?`): `f` sends the feedback prompt once the author has pushed, `c`
   removes worktree, branch and window when you are done.

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
  writes the `©` state pr-owl shows under tmux (and puts the same chips
  in your status bar). Without it the badge only says "a window
  exists". herdr reports the state itself; cmux through its own
  Claude Code hooks.
- [pr-review](pr-review/README.md), the Claude Code plugin whose review
  skill is the default `agent.prompt` of a fresh workspace (installed
  in step 2 above). It is what makes the default prompt do something;
  any other first prompt works without it.
- [spaces](https://github.com/stefanahman/spaces) keeps the review
  session in one terminal window on its own desktop space and opens
  the popup from a hotkey anywhere (macOS, with
  [yabai](https://github.com/koekeishiya/yabai) and
  [Ghostty](https://ghostty.org)); `hooks.after_open: {tmux: spaces
  focus pr-reviews}` brings that window to the front after every open
  under tmux, where it is not already.

## Multiplexers

Reviews live in a terminal multiplexer: one window per PR, the agent
typed into it, its state read back for the `©` badge. `mux: auto` picks
herdr when pr-owl runs inside it (`HERDR_ENV=1`), cmux inside cmux
(`CMUX_WORKSPACE_ID`) and tmux otherwise; `tmux`, `herdr` or `cmux`
forces one.

| | tmux | herdr | cmux |
|---|---|---|---|
| a review | a window of `tmux.session`, cwd the worktree | a workspace labelled `pr-<N>-<slug>`, cwd the worktree | a workspace named `pr-<N>-<slug>`, cwd the worktree |
| the agent's state | tmux-claude-status, from Claude Code's hooks | herdr's own detection, from the screen: a few seconds behind, so a badge can trail by one refresh. `done` means the same everywhere: finished, not yet looked at | cmux's Claude Code hooks, through the wrapper it puts on the shell's PATH: `running`, `needsInput`, `idle`. `done` is idle with cmux's notification about the turn unread; Enter in pr-owl marks it read |
| `f` while Claude waits | refused from the state option | refused by herdr's `agent.prompt` itself; an agent herdr hasn't detected gets the text typed, as under tmux | refused from the hook state; an agent the wrapper never saw shows no state and gets the text typed |
| Enter arrives | `select-window`, then your hook | `workspace focus`; every attached client follows | `workspace select`, and `focus-window` when pr-owl runs outside cmux |
| a failure after the popup closed | tmux's status line | a herdr notification | a cmux notification, on pr-owl's own workspace |
| `hooks.after_open` sees | `PR_OWL_SESSION` = the session, `PR_OWL_WINDOW` = the window | `PR_OWL_SESSION` = the herdr session, `PR_OWL_WINDOW` = the label | `PR_OWL_WINDOW` = the name; cmux has no session |

`PR_OWL_MUX` names the one in use, for hooks that only make sense with
one of them. Under herdr, run `pr-owl` in a pane of the session your
reviews should join; from outside, `herdr.socket` says which server,
and `PR_OWL_SESSION` is the session's name as read from that path.

One config serves all three: `mux: auto` picks the multiplexer from
the environment, `pr-owl --mux cmux` picks one for a run, and
`hooks.after_open` takes a mapping when a hook only makes sense under
one of them (`{tmux: spaces focus pr-reviews}`). `--config FILE`
reads another config; `PR_OWL_CONFIG` does the same for hooks that
can't pass flags. The multiplexers themselves are
[mux](https://github.com/stefanahman/mux)'s drivers; pr-owl is the
review side of them.

Under cmux, run `pr-owl` in a cmux terminal: cmux's socket admits only
processes started inside it, unless cmux itself was started with
`CMUX_SOCKET_MODE=allowAll`. The state needs cmux's Claude Code
integration (`automation.claudeCodeIntegration` in
`~/.config/cmux/cmux.json`). cmux 0.64.22 gives its terminals
`CMUX_WORKSPACE_ID` but not the `CMUX_SURFACE_ID` its wrapper checks
before injecting the hooks, so when pr-owl's own terminal lacks the
variable it types the start line as `CMUX_SURFACE_ID=<id> claude …`;
a cmux that sets it gets the line as it is.

**Not every agent tool is a backend.** The interface asks for a window
to type into, a state to read back, a way to focus that window and a
way to notify. [Paseo](https://paseo.sh) has terminals but exposes
none of the last three for them, and its agents are headless sessions
with no terminal to type into. It would fit as a different kind of
backend, one handed the agent and the prompt rather than a shell line;
that change waits for a backend that needs it.

## Keys

| key | action |
|---|---|
| `↓`/`j` `↑`/`k` `g` `G` `pgup` `pgdn` | move; section headers are skipped |
| `n` | jump to the next PR that needs you (Todo, or Claude blocked or done) |
| `↵` | open (or focus) the review workspace; then `on_open` |
| `s` | start the review workspace and stay in the list — no `on_open`, no `after_open`; press it on one PR after another |
| `f` | send the check-feedback prompt to the PR's Claude session and stay in the list, like `s` (refused while Claude is blocked on a question or a permission there) |
| `o` | open the PR in the browser |
| `y` | copy the PR URL |
| `c` | close the workspace — worktree, branch and window; refused while tracked files have uncommitted changes (`pr-owl close --force <N>` discards them) |
| `/` | filter by PR number; `esc` clears |
| `r` | refresh |
| `?` | help, with the full badge legend |
| `q` | quit |

`↵`, `s`, `f` and `c` run in the background: the list stays usable
while the child works, the row shows a spinner in the worktree slot,
a second press on the same PR is refused until it reports, and a
failure shows in the action row — or, once a popup has closed, on
tmux's status line for eight seconds.

Every key is rebindable, and `links` add your own (below).

## Commands

```
pr-owl [--config FILE] [--mux tmux|herdr|cmux] [command]   the options apply to every command, and to what the TUI runs
pr-owl                          the TUI
pr-owl open <N> [--prompt TEXT] open (or focus) PR N's workspace; with --prompt, hand the prompt to the agent
pr-owl start <N> [--prompt TEXT] the same without going there: no window selection, no after_open
pr-owl close [--force] [<N>]    remove the worktree, branch and window of the current repo; N is inferred from inside a workspace; --force discards uncommitted changes
pr-owl config init | path
pr-owl --version
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
mux: auto                        # tmux, herdr, cmux, or auto: herdr or cmux when pr-owl runs inside one, else tmux

tmux:
  session: pr-reviews            # one window per review lives here
  keepalive_window: scratch      # keeps the session alive with no reviews open

herdr:
  socket: ""                     # default: $HERDR_SOCKET_PATH, else ~/.config/herdr/herdr.sock

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
on_open: quit                    # the TUI once an open starts: quit (popup closes at once), stay, or switch (move your client to the reviews)

hooks:
  after_open: ""                 # runs after every open with PR_OWL_PR, _SESSION, _WINDOW, _WORKTREE, _REPO, _MUX set
                                 # or one per multiplexer: {tmux: spaces focus pr-reviews}

theme:                           # the three Claude-state colours (ANSI 0-255 or #rrggbb)
  working: "#dbbc7f"
  blocked: "214"
  done: "42"

keys:                            # rebind any action: a key name or a list
  open: enter
  start: s
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
CLI), where window-manager glue goes — `spaces focus pr-reviews`,
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
make test               # go test . and the e2e suite — the open/close tests need tmux
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

# owl

**The pull requests waiting for your review, the issues waiting for
your hands, and the projects they belong to, one keystroke from any
terminal.** Press Enter on a row and it becomes a workspace: a git
worktree, a window in your multiplexer — tmux, herdr or cmux — and
Claude Code inside it, on the review, the feature or the project.

[![ci](https://github.com/stefanahman/owl/actions/workflows/ci.yml/badge.svg)](https://github.com/stefanahman/owl/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/stefanahman/owl)](https://github.com/stefanahman/owl/releases)
[![license](https://img.shields.io/github/license/stefanahman/owl)](LICENSE)

[Install](#install) · [Quick start](#quick-start) · [Reviews](#reviews-owl-pr) · [Features](#features-owl-issue) · [Projects](#projects-owl-project) · [Keys](#keys) · [Configuration](#configuration) · [Multiplexers](docs/multiplexers.md) · [The plugin](owl/README.md)

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
Merged (last 1d)
  #3488            6h  remove legacy flag (erin)
```

owl stores nothing of its own. The branch carries the ticket, the pull
request carries the review, the window carries the agent; the lists
read all of it back from GitHub, Linear and the multiplexer, so nothing
is written twice and nothing goes stale in a second place.

- **Three lists, one shape.** `owl pr` groups pull requests by where
  you sit on them — todo, waiting for you, waiting for the author,
  approved, merged today. `owl issue` groups the Linear issues assigned
  to you by state. `owl project` holds the projects those issues belong
  to, with the conversation that plans and dispatches them. Same keys,
  same badges.
- **Workspaces on demand.** Enter fetches the branch into a worktree,
  opens a window, starts Claude on a first prompt. `c` removes all
  three; the conversation stays on disk, so the next Enter resumes it.
- **The agent's state, in the list.** `©` follows Claude every two
  seconds: working, blocked on you, done and unread. `n` jumps to the
  next row that needs you.
- **Prompts on a key.** `f` is feedback on both lists, from the two
  sides of it: on a review, "check the feedback since your last
  review"; on an issue, go through the review *you* were given and
  check each comment before applying it, because agreeing with all of
  it is the failure mode. `bindings` add your own, a prompt or a URL,
  per list. A prompt is refused while Claude is waiting on a question
  or a permission, since the keystrokes would answer it.
- **Skills with manners.** The bundled [plugin](owl/README.md) gives
  the agent its process: `review` never posts without you, `feature`
  never pushes without you, `dependabot` fixes on the bot's branch and
  drafts the review as a file. Any first prompt works without it.
- **Three multiplexers, one config.** tmux, [herdr](https://github.com/herdrdev/herdr)
  and [cmux](https://github.com/manaflow-ai/cmux); owl finds the one
  it runs in.

## Install

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
| [Claude Code](https://docs.claude.com/en/docs/claude-code) on the PATH of the shell your multiplexer runs | the start line is typed into that shell |
| one of tmux (≥ 3.2 for the popup), herdr ≥ 0.9, cmux ≥ 0.64 (macOS) | where the workspaces live |
| a Linear API key, for `owl issue` only | see [Features](#features-owl-issue) |

macOS and Linux. On Linux `o` opens with `xdg-open`; `y` copies through
OSC 52, the terminal escape for the clipboard, which most terminals
support.

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

## Telling you work arrived (`owl pr --check`)

The list is only awake while you are watching it — under tmux the popup
closes the moment you open a review — so a PR that lands in your court
while it is shut is exactly the one worth hearing about. `owl pr
--check` is that, without a TUI: it fetches, says what has **arrived**
since owl last looked, and leaves the cache as the new baseline.

```sh
$ owl pr --check
#4291 todo             add billing migration
#4242 waiting for you  ci: cache the monorepo
```

Run it from launchd, cron, or tmux's `status-interval` — owl stays a
command and never becomes a daemon.

A transition, not a state: a PR that has sat in your court since the
last look is not news, and announcing the standing set every five
minutes is how a notification becomes something you ignore. The
baseline is the same cache the list reads, so **opening owl counts as
having seen it**, and a first run on a cold cache is silent rather than
a morning's worth at once.

What comes out is yours to decide:

```yaml
hooks:
  attention: 'terminal-notifier -title owl -message "$OWL_SUMMARY"'
```

with `OWL_ARRIVED` (how many just arrived), `OWL_ARRIVED_PRS`
(`4291,4242`), `OWL_WAITING` (how many are in your court in total),
`OWL_SUMMARY`, `OWL_REPO` and `OWL_MUX` set — and a mapping for one per
multiplexer, as `after_open` takes.

**The hook runs on every check, arrival or not**, because it is asked
about state rather than told about an event: a badge that can go up has
to be able to come down, and a hook that only hears about arrivals
raises a count it can never clear. `OWL_ARRIVED` is `0` on a quiet run
and `OWL_WAITING` is still the truth. With no hook configured owl falls
back to the multiplexer's own notification, and *that* is event-shaped
— it fires when something arrives and stays quiet otherwise, since one
every five minutes saying the same thing is not a notification.

Which makes a sidebar badge a hook, not a feature. Under cmux:

```yaml
hooks:
  attention: |
    if [ "$OWL_WAITING" -gt 0 ]; then
      cmux set-status owl "$OWL_WAITING waiting" --workspace prs \
        --icon eye.fill --color "#00afff" --priority 80
    else
      cmux clear-status owl --workspace prs
    fi
```

Two things that decide whether that works. The `--workspace` is not
optional: a scheduled check has no cmux context of its own, so without
it the badge lands on whichever workspace happens to be selected. And
reaching cmux at all from a scheduler needs the app started with
`CMUX_SOCKET_MODE=allowAll` — the same condition
[docs/multiplexers.md](docs/multiplexers.md) names for running owl
outside a cmux terminal.

It deliberately says nothing about agents. An agent blocked or finished
is attention too, and `n` jumps to it — but Claude Code already posts
those to the multiplexer's notifications, and owl repeating them would
tell you twice. This is the half nobody else watches.

Two companions, each optional:

- [claude-status](https://github.com/stefanahman/claude-status) writes
  the `©` state under tmux (and puts the same chips in your status
  bar) and under cmux (a pill in the sidebar). Without it the badge
  under tmux only says "a window exists", and under cmux it comes from
  cmux's own hooks, which also count Claude's 60-second idle reminder
  as waiting for you. herdr reports the state itself.
- [spaces](https://github.com/stefanahman/spaces) keeps the reviews
  session in one terminal window on its own desktop space and opens
  the popup from a hotkey anywhere (macOS, with yabai and Ghostty);
  `hooks.after_open: {tmux: spaces focus reviews}` brings that window
  to the front after every open.

## Reviews (`owl pr`)

Rows are grouped by **where you sit on the PR**: todo, waiting for you
(the author pushed after your last review), waiting for the author,
approved — plus what merged in the last day, so a PR that merged
without your review doesn't vanish unseen. Newest change first within
a section. The list comes back as you left it: the last fetch from a
per-repo cache until the live one lands, the cursor on the row it was
on.

| badge | meaning |
|---|---|
| `⎇` | a worktree exists for the PR |
| `©` | Claude runs in its window — yellow working, amber blocked on you, green done, `*` until you look |
| `✓` | you approved |
| `·` | you engaged: commented or requested changes |
| amber `✓` / `·` | the author pushed after that review; it no longer covers the head |
| `⚠` | someone requested changes |
| `[draft]` | a draft PR |

## Two panes (`tab`)

The PR list has two, because "what needs my review" and "what is
stopping mine from landing" are different questions with different
answers and different keys. `tab` moves between them; the pane you are
not driving goes dim, so which one has the keys is never in doubt. Each
keeps its own cursor, so coming back lands where you left.

**The panes do not move.** The review queue is always above, yours
always below, and `tab` changes only the highlight — a pane that
swapped places would put the rows you were reading somewhere else every
time you switched, which is the eye movement two panes exist to save.

```
To review (2)
▸ #4302  ⎇ ©  ·   1h  refactor(backstage-desktop): rename the bundled…
Merged (last 1d)
  #4247       ✓  16h  fix: stop the Overview and Issues tabs reading…
Mine (17)
Blocked on you
  #4273  ○ ✗ ⚠  19h  feat(capture): mark enrichment stale on edit… [draft]
  #4003  ✓ ✓ ⚠   2w  feat(sven): safety-filter resilience…
Ready to merge
  #4007  ✓ ◐     7m  feat(capture): per-tenant captureEngine override…
Waiting on reviewers
  #4299  · ✓     1h  feat(ingest): stamp periodStart/periodEnd…
Not out for review
  #4290  ○ ✓     3h  refactor(tenancy): drop the legacy tenant shim
  #4306  ○ ✓    17h  fix(compile-db): keep captureStatus out… [draft]
```

The mine pane groups by **what is in the way**, not by review status:
blocked on you (a red check, changes requested, a conflict), ready to
merge, waiting on reviewers who have been asked, and not out for review
— never offered to anyone, which is every draft plus anything you
marked ready and forgot to request a review on.

Those two wear the same face and are not the same thing: a draft nobody
was asked about is what a draft is, while a PR marked ready and never
sent out is the one real omission this pane can catch. So the section
sorts by which — **drafts sink**, and the omission sits on top of them.
Nothing else re-orders: recency is the rule in every section, and
inside this one once the drafts have sunk.

| badge | meaning |
|---|---|
| `✓` `⚠` `·` `○` | GitHub's own `reviewDecision`: approved, changes requested, reviewers asked, nobody asked |
| `✓` `✗` `◐` | the head commit's checks: green, failing, still running |
| `⚠` (third slot) | GitHub reports the branch as conflicting |

Two things worth knowing. `mergeable` is computed lazily, so a PR
GitHub has not been asked about reads as no-conflict until something
asks — which means the list can change shape on a refresh with nothing
having happened. And a PR approved with checks still running sits under
"Ready to merge" with a `◐`: the badge is the truth, the heading is the
intent.

Keys follow the pane. `bindings.mine` is its own list, shipping `f` —
the same key as the issue list's, and the same job: go through the
review you were **given**, checking each comment rather than complying
with it.

## Your own PR opens the workspace you already have

A review of someone else's work gets a copy: `pull/<N>/head` fetched
into a branch of owl's own, which `close` deletes without asking
because a copy is all it ever was. On your own PR both halves are
wrong — the copy reviews the pushed head while you edit the real tree
in the worktree next door, and the delete takes your commits with it.

So **a PR of yours resolves to its branch**, and the branch decides
everything else:

| the PR | the workspace |
|---|---|
| yours, branch carries an issue key | the feature's — `bar-4157-<slug>`, in `features` |
| yours, no issue key | `pr-<N>-<slug>` in `reviews`, holding the real branch |
| someone else's, or yours from a fork | `pr-<N>-<slug>`, a fetched copy |

When the feature is already open — you started it from `owl issue` —
Enter in the mine pane goes **there**: same worktree, same window, same
conversation, whatever the directory ended up called. The match is on
the branch and is exact, so a PR on `bar-4157-part-2` never lands in
the worktree holding `bar-4157-part-1`. Opening the other way round
finds it too, because the branch inside still carries the key.

The link between a PR and its issue is the key in the branch name —
what Linear's own GitHub integration puts there — filtered by
`linear.team`, so `deps/sharp-0.35.4` is a dependency bump and not
SHARP-0. A branch closing several issues opens into the newest one's
workspace: one branch is one piece of work and gets one workspace.

Two consequences worth knowing. The window's container follows the
**workspace name**, not the command that opened it, so one worktree can
never end up with a window among the reviews and another among the
features, each with its own agent. And `owl pr close <N>` on a PR that
resolved to a feature refuses and says so: closing a feature deletes a
branch whose commits exist nowhere else, and `owl issue close` is the
one that checks for them first.

`open` is idempotent: it creates what is missing and selects the
window. A workspace is named once, from the PR title at first open,
and found by number afterwards — after `close`, by the name Claude's
conversation is stored under — so the path stays stable if the PR is
retitled, and with it Claude's per-directory conversation, which is
what lets `close` be cheap and `open` resume. Re-running `open` never
re-fetches: the worktree is yours once it exists; when the author
pushes, `git pull origin pull/<N>/head` inside it. The first `open` in
a clone adds the worktrees directory to `.git/info/exclude`, so `git
status` stays clean without touching the project's `.gitignore`.

Fork-based workflow — `origin` your fork, `upstream` the repo the PRs
are on? Set `remote: upstream`: PRs are listed for, and fetched from,
that remote.

## Features (`owl issue`)

```
owl · issues · acme/app                                     updated just now
4 open · 2 in progress · 1 todo · 1 backlog
────────────────────────────────────────────────────────────────────────────
In progress
▸ BAR-4160  ⎇ ©  !!   2h  Per-tenant captureEngine override  Sequential Capture
  BAR-4159       !!   8h  Company fuzzy match                                    In Review  #3543✓
Todo
  BAR-4578       !!!  1d  Rate-limit the ingest worker       Sequential Capture             #3550 draft
Backlog
  BAR-4404            9d  Shadow output validation           Endpoint Validation
Done · 1d
  BAR-4286             3h  Open update-activity fields       Endpoint Validation
```

Every issue Linear assigns to you — all of them, paged 100 at a time
until Linear runs out — in four sections by state: in progress, todo,
backlog, and what you finished in the last day, newest change first.

A row shows the key, the workspace badges, the priority (`!!!` urgent
to `-` low), the age of the last change (of the closing, in Done), the
title, the project, the state, and every open PR whose head branch
carries the issue's key, newest first: `#N✓` approved by someone, `#N⚠`
changes requested, `#N draft`. The key and not the whole branch name,
since a PR is as often pushed from `fix/bar-4159-particle-guard` as
from the slug Linear names.

The state name appears only where it says something the section does
not: inside Backlog every row would read "Backlog", while inside In
progress the difference between "In Progress" and "In Review" is the
point. The title takes whatever width the window leaves.

Enter opens the feature workspace, `o` opens the issue in Linear, `y`
copies its key, `/` filters by key, title or project. Off a terminal,
`owl issue` prints a table of the open ones.

A feature workspace is a worktree for the issue. `open` looks for the
work that already exists before making any of its own:

1. **A worktree already checked out for the issue** — one of owl's,
   else one made by hand or by Claude Code's worktree tool, used where
   it stands. git will not check a branch out twice, so a worktree
   outside `worktrees_dir` has to be reused rather than duplicated;
   `open` says where it landed, and `close` leaves it alone.
2. **A branch carrying the key**, local or on the remote. Work on an
   issue rarely lives on the slug Linear names: it is pushed from
   `bar-4098-credit-flip-uniform-sets` or `fix/bar-4157-projection`.
   Several is the rare case — a stack, a second attempt — and the
   newest commit wins, with the others named so the choice is visible.
3. **The branch Linear names** (`bar-4159-company-fuzzy-match`), only
   when nothing carries the key, started from the remote's default
   branch with no upstream set, so a `git push` cannot land it on main
   by accident.

Claude starts in the worktree on `/owl:feature <KEY>`, resuming a prior
conversation with `-c`. `close` removes the worktree, the local branch
and the window — but never a worktree outside `worktrees_dir`, nor a
branch another worktree holds, and a branch with commits that exist
nowhere else is refused unless `--force`. The remote branch is never
touched.

`owl hoot "what needs doing"` files an issue in `linear.team`, assigned
to you, and prints its key and URL.

## Projects (`owl project`)

```
owl · projects · acme/app                                   updated just now
14 projects · 6 in progress · 1 planned · 7 backlog · 15 issues yours
────────────────────────────────────────────────────────────────────────────
In progress
▸ Sequential Capture redesign          ▓▓▓▓▓▓░░░░  62%  12/127  11 ms
  Emission Categories — Plumbing       ▓▓▓▓▓▓▓░░░  71%   0/6     4 ms
  Bardo Backstage (BACKEND)            ▓░░░░░░░░░   8%   0/917         Paused
Planned
  Endpoint Validation w. LLM errors    ▓▓▓▓▓░░░░░  50%   2/4
Backlog
  Sven v2 — Deterministic harness      ▓▓▓░░░░░░░  28%   0/18    6 ms
```

The projects you work in, in three sections by status, newest change
first. A row shows the name, Linear's own progress as ten cells, **your
open issues over every issue the project holds**, the milestone count,
and the state name only where the section does not already say it — a
status named Paused but typed `started` sits under In progress and says
so.

A project in one of `project.dim_statuses` renders **dim throughout** —
name, bar and counts — while staying in the section its status type
puts it in. It is present and not moving, which is a different thing
from being elsewhere. The default is `[Paused]`, and it is a config key
rather than a rule in the code because nothing in the data marks such a
status: Linear has no `paused` status *type*, so a workspace's Paused
is typed `started`, identical to In Progress. Naming it here is the
only honest way to tell them apart, and it keeps working when you
rename it or add another.

A fourth section, **Completed · 7d**, holds what you finished in the
last week — a week rather than the issue list's day, because projects
finish on a different clock and one closed on Monday is still news on
Friday.

Which projects are yours is a union: the ones you lead, the ones you
belong to, and the ones you have an open issue in. That last clause is
not decoration — filtering by membership alone drops the project most
of the work is in.

owl works that third clause out itself rather than asking Linear,
because Linear cannot answer it. Its project filter
`issues: { some: { assignee: { isMe }, state: { type: { nin: … } } } }`
does not conjoin per issue: it matches when *some* issue is yours and
*some* issue is open, which in a project of 920 is always true, and
`and:` inside `some` behaves the same. So owl asks Linear only for
lead-or-member, and derives the rest from the issues it has already
fetched — one 90ms lookup per project that is missing.

`/` filters by name, `o` opens the project in Linear, `y` copies its
name. Off a terminal, `owl project` prints a table.

**`→` on a project row drills into it**, and `←` comes back to the row
you left. The drilled list is the project's own issues, everyone's,
grouped by milestone in the project's order with the unmilestoned last.
The project column goes — every row would name the project you are
standing in — and the assignee column fills in for the issues that are
not yours. Enter still opens a feature workspace, because what the key
does follows the row and not the list.

`owl issue --project <id|name>` is the same list off a terminal —
**everyone's, not your slice** — grouped by milestone in the project's
own order, with the unmilestoned last and the assignee column blank
where the issue is yours, so the gaps are your queue. It takes Linear's
slug or a fragment of the name, the same as `owl project open`.

```
$ owl issue --project sequential
Sequential Capture redesign · 62% · 52 open, 12 yours

M3.5 — Downstream compatibility & activation prerequisites (pre-cutover gate)
  BAR-4782  !    1d  In Review            Define resolves issuer-scoped business rules…
  BAR-4785  !!   1d  Backlog              Flag activities whose PII masking failed…
M5 — Output evaluation
  BAR-2718  -    9w  Duplicate   Emilio   Confirm currency/unit/region/issuer parity
No milestone
  BAR-4160  !!   1d  In Review            Per-tenant captureEngine override…
```

Enter opens the project's **conversation** — the one above the issues.
It is a workspace like the others: a worktree under `worktrees_dir`
named `proj-<slug>`, a window in `project.session`, Claude started in
it on `/owl:project {name}`. Two things differ, and both follow from a
project having no branch of its own:

- The worktree is **detached at the remote's default branch**. git
  refuses to check `main` out twice, and a project's agent has nothing
  to commit: it reads, plans, dispatches with `owl issue start`, and
  reviews what comes back. The `/owl:project` skill says so in as many
  words.
- owl **names the conversation**. It generates a session id on first
  open, keeps it in `$XDG_STATE_HOME/owl/projects.json`, and passes
  `--session-id` then `--resume` — so a project is one conversation
  that resumes as itself, rather than whatever `-c` finds last in that
  worktree. Session ids are machine-local, which is why they live
  beside the Linear token and not in the repo.

`owl project open|start|close <id>` does the same from a terminal,
where `<id>` is Linear's slug or a fragment of the name — `owl project
open sequential`. A fragment matching two projects is an error naming
both rather than a guess. `close` removes the worktree and the window
and keeps the session id, so the next open resumes the conversation.

Linear is reached with a personal API key (Settings → Security &
access), which the config holds as a **reference**, never as a value:

```yaml
linear:
  token: op://Work/Linear API key/credential
  account: work.1password.com   # when more than one account is signed in
  team: BAR
```

`team` earns its place twice: it is where `owl hoot` files an issue,
and it is what makes a key in a branch name mean something. Leave it
unset and `bar-4157-<slug>` is just a branch — a PR of yours will open
a workspace of its own rather than the feature's.

An `op://` reference is read from 1Password the first time it is
needed — owl says why before the prompt appears — and kept in
`~/.local/state/owl/linear.token`, mode 600, the way `gh` keeps a
token on a machine without a keyring: one approval per machine, none
per start. A key Linear refuses is forgotten and read again, once;
delete the file to force that by hand. A cache file others can read is
refused. The other forms: `file://~/.config/owl/linear.token` reads a
file of yours (mode 600, or it is refused), `$VAR` reads the
environment, and anything else is taken as the key itself — the one
form that puts a secret in the config.

## Keys

| key | on a PR | on an issue | on a project |
|---|---|---|---|
| `↓`/`j` `↑`/`k` `g` `G` `pgup` `pgdn` | move; section headers are skipped | the same | the same |
| `n` | next PR that needs you: todo, or Claude blocked or done | next issue whose Claude needs you | next project whose Claude needs you |
| `↵` | open (or focus) the review workspace, then `on_open` | open (or focus) the feature workspace | open (or focus) the project's conversation |
| `s` | start the workspace and stay in the list — no `on_open`, no `after_open`; press it on one row after another | the same | the same |
| `f` | send the check-feedback prompt to the PR's Claude and stay; only on a PR with a conversation | — | — |
| `o` | open the PR in the browser | open the issue in Linear | open the project in Linear |
| `y` | copy the PR URL | copy the issue key | copy the project name, what `/` and a search take |
| `c` | close the workspace: worktree, branch and window; refused while tracked files have uncommitted changes (`owl pr close --force <N>` discards them) | the same, for the feature | worktree and window; the session id is kept, so the next `↵` resumes the conversation |
| `/` | filter by number; `esc` clears | filter by key, title or project | filter by name |
| `r` `?` `q` | refresh, help with the full legend, quit | the same | the same |

`↵`, `s`, `f` and `c` run in the background: the list stays usable
while the child works, the row shows a spinner in the worktree slot, a
second press on the same row is refused until it reports, and a
failure shows in the action row — or, once a popup has closed, as a
notification from the multiplexer. Every key is rebindable (`keys`),
and `bindings` add your own to the PR and issue lists; a project row
takes none until it has more than one workspace to aim at.

### Prompts and links on a key

```yaml
bindings:
  pr:
    - key: d
      name: Dependabot
      prompt: "/owl:dependabot {pr}"
    - key: l
      name: Linear
      pattern: 'PROJ-\d+'        # {id} is the first match in title, body and branch
      url: https://linear.app/my-org/issue/{id}
  issue:
    - key: p
      name: Continue
      prompt: "Continue {key}: pick up where you left off"
      when: conversation         # only on a row whose agent has a conversation
```

A binding is a key, a `name` for the help view and exactly one of
`prompt` and `url`. A prompt goes to the row's agent the way `s`
starts one — `owl pr start <N> --prompt …` — so it starts a workspace
where there is none and resumes the conversation where there is one.
A URL opens with `open_cmd`. Placeholders: `{pr}` (or `{key}` on the
issue list), `{repo}`, `{branch}`, `{url}` and `{id}`, the first match
of `pattern`; a binding with a pattern does nothing on a row it does
not match. `when: conversation` keeps the key to rows whose agent
already has one, which is how the shipped `f` behaves; listing `f`
again replaces it. Keys are checked per list, so `l` may mean one thing
on PRs and another on issues. Prefer a skill of your own? Bind it:
`/team:review {pr}`.

## Commands

```
owl [--config FILE] [--mux tmux|herdr|cmux] [<noun> [command]]   the options apply to every command, and to what the TUI runs
owl                                   this introduction and the commands
owl pr                                the PR list
owl pr open <N> [--prompt TEXT]       open (or focus) PR N's workspace; with --prompt, hand the prompt to the agent
owl pr start <N> [--prompt TEXT]      the same without going there: no window selection, no after_open
owl pr close [--force] [<N>]          remove the worktree, branch and window; N is inferred from inside a workspace
owl issue                             the open issues assigned to you; a table when stdout is not a terminal
owl issue open <KEY> [--prompt TEXT]  open (or focus) the feature workspace of issue KEY
owl issue start <KEY> [--prompt TEXT] the same without going there
owl issue close [--force] [<KEY>]     remove the feature's worktree, local branch and window
owl issue new <title…>                file an issue in linear.team, assigned to you
owl project                           the projects you work in; a table when stdout is not a terminal
owl project open <id> [--prompt TEXT] open (or focus) the project's conversation; <id> is Linear's slug or a fragment of the name
owl project start <id> [--prompt TEXT] the same without going there
owl project close [--force] <id>      remove the project's worktree and window; the session id is kept
owl hoot <title…>                     the same, from the owl
owl config init | path
owl --version
```

The noun is the scope — `pr`, `issue` or `project` — so a verb never
has to guess from the shape of an id which kind of thing it acts on.
`close` exits 2 when there was nothing to remove.

## How it works

| layer | what | owner |
|---|---|---|
| git worktree | `<repo>/.worktrees.local/<name>` on a branch of the same name: `pr-<N>-<slug>` fetched from `pull/N/head`, or the issue's branch. A project's `proj-<slug>` is detached at the remote's default branch instead: it has no branch of its own, and its agent reads rather than commits | `open` creates, `close` removes |
| window | same name, in the multiplexer, cwd the worktree, running `agent.cmd` | `open` creates, `close` kills |
| conversation | Claude's transcript for that directory | survives `close`; `open` resumes it with `-c`, or by `--session-id` for a project, whose id owl keeps in `$XDG_STATE_HOME/owl/projects.json` |

A prompt (`f`, a binding, `open --prompt`) is typed into the window as
one line of keystrokes. If the agent has exited — the window is back at
a shell — it is started again with `-c` and the prompt; while Claude is
blocked on a question or a permission, the prompt is refused. owl works
on one repo at a time — the working directory's, or `default_repo` —
and window names carry the PR number, the issue key or the project
slug, not the repo, so keep one repo per session.

The multiplexers differ in how a window is made, how the state is
read and what happens after Enter: [docs/multiplexers.md](docs/multiplexers.md)
has the table, the per-multiplexer notes and the one environment rule
that matters (start the multiplexer from a clean shell, not from
inside Claude).

## Configuration

`~/.config/owl/config.yaml` (`$XDG_CONFIG_HOME` and `$OWL_CONFIG`
respected). `owl config init` writes this, with longer comments; every key is
optional, and these are the defaults.

```yaml
mux: auto                        # tmux, herdr, cmux, or auto: herdr or cmux when owl runs inside one, else tmux

tmux:
  session: reviews               # one window per review lives here
  keepalive_window: scratch      # keeps the session alive with no reviews open

herdr:
  socket: ""                     # default: $HERDR_SOCKET_PATH, else ~/.config/herdr/herdr.sock

remote: origin                   # the GitHub remote: PRs are listed for it and fetched from it
worktrees_dir: .worktrees.local  # relative to the repo root
default_repo: ""                 # used when owl starts outside a git repo

agent:
  cmd: claude --permission-mode auto   # Claude Code, with your flags (e.g. --model claude-opus-5); -c is appended when the worktree has a prior conversation
  prompt: "/owl:review {pr}"           # first prompt of a fresh review
  link_local:                          # symlinked from the repo into each new worktree (keep them gitignored there)
    - .claude/settings.local.json
    - .claude/*.local.md
    - .claude/skills/*.local

issue:
  session: features              # tmux: one window per feature lives here; herdr and cmux need no container
  prompt: "/owl:feature {key}"   # first prompt of a fresh feature; {key} is the issue's key

project:
  session: projects              # tmux: one window per project — the conversation above the issues
  prompt: "/owl:project {name}"  # first prompt of a fresh project; {name} is the project's name in Linear
  dim_statuses: [Paused]         # statuses that mean present but not moving; their rows render dim

groups:                          # cmux workspace groups, one per scope; tmux and herdr ignore them
  enabled: false                 # folds the sidebar, costs its single recency order — see docs/multiplexers.md
  reviews:  { color: "#00afff", icon: eye }
  features: { color: "#00d75f", icon: hammer }
  projects: { color: "#af87ff", icon: square.stack.3d.up }

open_cmd: ""                     # opens URLs; default: open (macOS) or xdg-open
on_open: auto                    # the TUI once an open starts: auto (quit under tmux, stay under herdr and cmux), quit, stay, or switch (move your client to the reviews)

hooks:
  after_open: ""                 # runs after every open with OWL_PR/OWL_ISSUE, _SESSION, _WINDOW, _WORKTREE, _REPO, _MUX set
                                 # or one per multiplexer: {tmux: spaces focus reviews}

theme:                           # the three Claude-state colours (ANSI 0-255 or #rrggbb)
  working: "#dbbc7f"
  blocked: "214"
  done: "42"

keys:                            # rebind any action: a key name or a list
  open: enter
  start: s
  quit: [q, ctrl+c]

linear:                          # the issue tracker behind `owl issue`
  token: ""                      # op://<vault>/<item>/<field>, file://<path>, $VAR, or the key
  account: ""                    # the 1Password account the item is in, when several are signed in
  team: ""                       # the team's key (BAR in BAR-123): where `owl hoot` files issues

bindings:                        # your own keys on a row: a prompt for its agent, or a URL to open
  pr:
    - key: f                     # shipped; listing it again replaces it
      name: check feedback
      prompt: "Please carefully check the feedback since your last review …"
      when: conversation
  issue:
    - key: f                     # shipped; the review you were given, not the one you are giving
      name: check the review
      prompt: "Please go through the review on {key}'s pull request carefully … Agreeing with all of it is the failure mode, not the goal."
      when: conversation
```

The default `agent.cmd` runs Claude with `--permission-mode auto`
inside a checkout the PR's author controls, and `link_local` never
replaces a file the PR ships under the same name — so a PR's own
`.claude/` is what the agent starts with. Read that part of the diff
first when it matters.

Two hooks, two layers: `on_open` is what the TUI itself does once an
open starts; `hooks.after_open` is a shell command run after *every*
`open` (TUI or CLI), where window-manager glue goes.

## Hacking

```sh
make test               # go test . and the e2e suite — the open/close tests need tmux
make lint               # gofmt, go vet
make update-snapshots   # after a deliberate UI change: the screen snapshots
OWL_SMOKE=1 go test -run TestFetchSmoke .   # against your real gh, from a repo checkout
```

Three test layers, all hermetic — throwaway git origin, fake `gh`, a
private tmux server, empty `CLAUDE_CONFIG_DIR`; nothing touches your
sessions or config:

- `ui_test.go` drives the Bubble Tea model with teatest and asserts on
  what the frames say.
- `workspace_test.go` runs `open`/`close` for real against tmux and git.
- `e2e/` runs the built binary in a virtual terminal (`x/vttest`) and
  snapshots the screen as JSON — plus a PNG next to it, so a UI change
  shows up as a picture in the PR.

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

## License

MIT

# Reviews (`owl pr`)

Rows are grouped by **where you sit on the PR**: todo, waiting for you
(the author pushed after your last review), waiting for the author,
approved — plus what merged in the last day, so a PR that merged
without your review doesn't vanish unseen. Newest change first within
a section. The list comes back as you left it: the last fetch from a
cache until the live one lands (one per repo, or one for the list across
`pr.owners`), the cursor on the row it was on.

| badge | meaning |
|---|---|
| `⎇` | a worktree exists for the PR |
| `©` | Claude runs in its window — yellow working, amber blocked on you, green done, `*` until you look |
| `✓` | you approved |
| `·` | you engaged: commented or requested changes |
| amber `✓` / `·` | the author pushed after that review; it no longer covers the head |
| `⚠` | someone requested changes |
| `[draft]` | a draft PR |
| a dim row | your **team** was asked, you were not |

## A push after your review

Any new head puts a PR you reviewed under waiting for you, a rebase
included. owl does not try to tell the two apart, because how the
branch moved says nothing about the code: a fix for review feedback is
a fixup squashed into its commit, so it arrives as a force push exactly
as a rebase does. The shipped `f` settles it first. The PR's Claude
diffs the commit your review covers and the head against the base, with
no context lines, and compares their `git patch-id`: the same id means a
rebase or a squash moved the code and changed none of it, and otherwise
`git range-diff` names the commits that changed. Its answer opens with
that, before the findings.

## Asked of you, or of your team

GitHub's review request is one of two things wearing the same face: a
request to **you**, and a request to a **team you are in**. The second
reaches everybody in the team and asks nobody in particular, which is
why many teams treat it as an FYI and a direct request as a real ask.

owl tells them apart because GitHub does: a requested reviewer is a
`User` or a `Team`, so this is read rather than guessed. A team-only
request **renders dim** — present, with a lower claim on you, the same
thing dim means on a paused project — and **`n` walks past it**. The
row stays where it was and `j`/`k` still reach it; only the key that
means "next thing that needs me" stops treating it as one.

Three shapes have to come apart and only one is an FYI. Asked of the
team *and* of you is a request like any other — the team request does
not dilute your own. Asked of nobody outstanding is a PR you have
already reviewed, which is why it is still listed at all. Only
requests that exist, none of them yours, at least one a team's.

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
Merged (last 3d)
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
Merged (last 3d)
  #4294  ✓ ✓     2h  fix(capture): reject enrichment writes made obsolete…
```

The mine pane groups by **what is in the way**, not by review status:
blocked on you (a red check, a conflict, changes you have not handed
back), ready to merge, waiting on reviewers who have been asked, and
not out for review — never offered to anyone, which is every draft plus
anything you marked ready and forgot to request a review on.

Last comes what landed, inside `merged_window`. Nothing else shows it:
the review list is other people's work by definition (`-author:@me`),
and an open-PR fetch drops a PR the moment it merges — so without this
your own merge disappears the second it happens, which is a strange way
for a day's work to end.

"Changes requested" sits in whichever of the first two the ball is
actually in. Re-request the review and the row moves to **waiting on
reviewers**, because a reviewer's own review consumes their request —
so one standing beside their verdict was made after it, and the move is
theirs. GitHub disagrees: it keeps `reviewDecision` at
`CHANGES_REQUESTED` until they answer, since only a new review or a
dismissal clears it, and `mergeStateStatus` stays `BLOCKED` alongside.
That is the right answer to "may this merge" and the wrong one to
"whose move is it". A red check or a conflict is yours either way. The
row keeps its `⚠` in every case, so the section never hides the
verdict.

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
what Linear's own GitHub integration puts there — filtered by the
teams you file under, so `deps/sharp-0.35.4` is a dependency bump and
not SHARP-0. A branch closing several issues shows all of them, newest
first, and opens into the newest one's workspace: one branch is one
piece of work and gets one workspace.

**The prompt follows the workspace too.** `/owl:review` is a procedure
for someone else's pull request — it ends in a review posted with `gh`,
which you cannot do to yourself — so your own PR opens on `mine.prompt`
instead: read the commits, the checks, the review comments and the
worktree, then say what is done, what is left, and what is stopping it
from landing, and wait.

That fires only where there is nothing to come back to. A workspace
that already holds a conversation resumes it with `-c` and is sent no
prompt at all, so the feature you have been building all morning opens
on its own transcript, not on an instruction. The first prompt is for
arriving somewhere for the first time.

Two more consequences. The window's container follows the **workspace
name**, not the command that opened it, so one worktree can never end
up with a window among the reviews and another among the features, each
with its own agent. And `owl pr close <N>` on a PR that resolved to a
feature refuses and says so: closing a feature deletes a branch whose
commits exist nowhere else, and `owl issue close` is the one that
checks for them first.

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
a morning's worth at once. `owl pr --here` shows one repo and keeps a
cache of its own, so it moves no baseline.

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
[multiplexers.md](multiplexers.md) names for running owl
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

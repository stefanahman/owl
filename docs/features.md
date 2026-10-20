# Features (`owl issue`)

```
owl · issues · acme/app                                     updated just now
4 open · 2 in progress · 1 todo · 1 backlog
────────────────────────────────────────────────────────────────────────────
In progress
▸ ENG-4160  ⎇ ©  !!   2h  Per-tenant cachingEngine override  Background Indexer
  ENG-4159       !!   8h  Address fuzzy match                                    In Review  #3543✓
Todo
  ENG-4578       !!!  1d  Rate-limit the ingest worker       Background Indexer             #3550 draft
Backlog
  ENG-4404            9d  Shadow output validation           Endpoint Validation
Done · 1d
  ENG-4286             3h  Open update-customer fields       Endpoint Validation
Canceled · 3d
  ENG-4287            12h  Drop the shadow validation        Endpoint Validation
```

Every issue Linear assigns to you — all of them, paged 100 at a time
until Linear runs out — in five sections by state: in progress, todo,
backlog, what you finished in the last day, and what was cancelled in
the last three.

**Todo and Backlog lead with priority**; everything else leads with the
last change. Those two are the sections you read to choose what to pick
up, and recency answers a different question — in the workspace this
was built against, the six most recently touched backlog issues were
all unranked while four marked High sat below them. `!!!` to `-` on the
row is the order, so nothing needs a legend. In progress keeps recency
because what you touched last is how you find your way back into work
already begun, and Done and Cancelled keep it because their order is
history.

Linear's own line is [Active versus
Backlog](https://linear.app/docs/default-team-pages) — Todo counts as
active, and means committed-but-not-started rather than important. The
way Linear intends issues to cross that line is
[cycles](https://linear.app/docs/configuring-workflows): "issues here
will be updated to active (To do) status if they're moved to a cycle."
With cycles off, Todo only fills by hand and priority is the signal
that is actually there — which is why the ordering leans on it.

**Cancelling is not finishing**, so it is a section of its own rather
than a row in Done, and it reaches back three days where Done reaches
back one: you were there when you finished something, while a
cancellation is usually someone else's decision about your work and
should survive a weekend. The section renders grey — on the list so you
notice it happened, not so it competes with work that is still alive.
An issue marked a duplicate is listed here too, with Duplicate in its
state column: Linear gives Duplicate a state type of its own, and an
issue marked one has left your work the way a cancelled one has.

Its own request, too, and not by choice. Linear accepts an `or:` of two
windowed branches and then answers by state type alone, applying
neither branch's date bound — verified against the live API, where one
query returned issues cancelled two months back. Two queries, one bound
each.

A row shows the key, the workspace badges, the priority (`!!!` urgent
to `-` low), the age of the last change (of the closing, in Done), the
title, the project, the state, and every open PR whose head branch
carries the issue's key, newest first: `#N✓` approved by someone, `#N⚠`
changes requested, `#N draft`. The key and not the whole branch name,
since a PR is as often pushed from `fix/eng-4159-particle-guard` as
from the slug Linear names.

The state name appears only where it says something the section does
not: inside Backlog every row would read "Backlog", while inside In
progress the difference between "In Progress" and "In Review" is the
point. The title takes whatever width the window leaves.

Enter opens the feature workspace, `o` opens the issue in Linear, `y`
copies its key, `/` filters by key, title or project. With several
Linear workspaces the list shows one at a time and `tab` moves to the
next, `shift+tab` to the previous, as on the project list ([projects.md](projects.md#several-workspaces)).
Off a terminal, `owl issue` prints a table of the open ones.

The workspace opens in the repository owl runs in, unless `repos` names
one for the issue's team. With several workspaces, one list holds teams
whose code lives in different repositories, and

```yaml
repos:
  ENG: ~/src/app
  OPS: ~/src/infrastructure
```

sends ENG-4160 and OPS-12 to their own repositories from whichever list
they were opened in; `start` and `close` follow, and the list shows
their worktrees from there. A team `repos` leaves out, such as one whose
work spans several repositories, keeps opening where owl runs.

A feature workspace is a worktree for the issue, and its conversation is
named for it: `--name "ENG-4160 Per-tenant override"`, the key and the
title as Linear has it at that open, so Claude's resume picker and, with
`issue.cmd` turning on Remote Control, claude.ai and the Claude app list
the work by name. `open` looks for the work that already exists before
making any of its own:

1. **A worktree already checked out for the issue** — one of owl's,
   else one made by hand or by Claude Code's worktree tool, used where
   it stands. git will not check a branch out twice, so a worktree
   outside `worktrees_dir` has to be reused rather than duplicated;
   `open` says where it landed, and `close` leaves it alone.
2. **A branch carrying the key**, local or on the remote. Work on an
   issue rarely lives on the slug Linear names: it is pushed from
   `eng-4098-cursor-flip-uniform-sets` or `fix/eng-4157-projection`.
   Several is the rare case — a stack, a second attempt — and the
   newest commit wins, with the others named so the choice is visible.
3. **The branch Linear names** (`eng-4159-address-fuzzy-match`), only
   when nothing carries the key, started from the remote's default
   branch with no upstream set, so a `git push` cannot land it on main
   by accident — or from `--base <branch>`, when the issue is a layer
   of a [stack](https://docs.github.com/en/pull-requests/get-started/about-stacked-prs).

`--base` is for one change split across issues, where the second needs
what the first adds. The worktree starts from that branch rather than
the trunk, so the layer's diff is its own, and owl records the base
under `branch.<branch>.owlBase` in git's config. That record is the
point: a briefing is one prompt in one conversation, and the session
that picks the branch up next week never read it — while it is exactly
the one that must not rebase onto the trunk or force-push over the
layer below. It can ask git instead:

```sh
git config --get branch.$(git branch --show-current).owlBase
```

The base has to be on the remote already, since the layer is cut from
it; owl says so plainly rather than passing git's complaint along.

Claude starts in the worktree on `/owl:feature <KEY>`, resuming a prior
conversation with `-c`. `close` removes the worktree, the local branch
and the window — but never a worktree outside `worktrees_dir`, nor a
branch another worktree holds, and a branch with commits that exist
nowhere else is refused unless `--force`. The remote branch is never
touched.

Work begun before its issue was filed — an agent's worktree beside the
repository, or under `.claude/worktrees/` — moves into the workspace
`open` would have made with `owl issue adopt <KEY>`, run from inside
it, or `owl issue adopt <KEY> <dir>` from anywhere. The issue has to
exist first: `owl hoot` files one. Then adopt:

| | |
|---|---|
| The worktree | moves to `worktrees_dir/<name>`, named as `open` names one |
| Its branch | takes the name Linear gives the issue, which is what links a PR to it, with no upstream, as `open` starts one. A branch that carries the key keeps its name; so does one on the remote, which a new name would part from its PR (put the key in the PR's title instead) |
| Claude's conversation | moves with it: the folder Claude Code keeps for the worktree's path, whole and unread, so `open` resumes it there |

Then it opens the issue as `open` does. It refuses, and moves nothing,
when the worktree is the main checkout or another repository's, its
branch carries another issue's key, the issue already has a worktree,
Claude keeps a conversation at both ends, or Claude runs in the
worktree — a session whose directory moves out from under it breaks, so
end it first. A conversation that had Remote Control on keeps its
claude.ai session, listed under the folder it started in until that
session is deleted.

Opening an issue also claims it in Linear, so the board says what
you are working on before there is a pull request to say it. The
state and the assignee are decided apart:

| | Before | After |
|---|---|---|
| State | triage, backlog or todo | the team's first started state, In Progress by default |
| State | started, done or cancelled | unchanged |
| Assignee | nobody | you |
| Assignee | someone else | unchanged, and owl names them |

An issue already in progress and yours is read and not written. A key
without Write access, or a Linear that does not answer, costs a line
of output and never the workspace. `issue.claim: false` turns it off.

`owl hoot "what needs doing"` files an issue in `linear.team`, assigned
to you, and prints its key and URL.

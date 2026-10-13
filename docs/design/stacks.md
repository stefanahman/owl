# Stack reviews

A design note, written before the code. A stack is two or more open
pull requests where each one's base is the head branch of the one
below it; the bottom's base is the trunk. owl reviews one PR at a time,
and a stack asks for the picture across the layers as well: what the
bottom sets up and the top relies on, and where a finding on layer five
was really introduced on layer two.

## What the stacks look like

Measured on bardo-system, 2026-10-13: 129 open PRs, 12 stacks among
them, and 3 of those fork, where two or more open PRs share one base.
Two of the forks are in the stack this feature was asked for:

```
#4969 ─┬─ #4970 ─ #4971 ─ #4972 ─ #4973 ─┬─ #4974
       └─ #5124                          ├─ #4984
                                         ├─ #4993 ─ #5013
                                         └─ #5065
```

So a fork is not an error case to refuse. It is the ordinary shape of
parallel work on a shared base.

## A stack review covers one line

**A line is the path from the trunk to one tip**, a PR with nothing
open on top of it. It is exactly the code that tip would merge, which
is what a reviewer reasons about: #5013 runs on top of #4969 through
#4993 and on nothing from #4974.

| `--stack` on | the line |
|---|---|
| #4974 | #4969 → … → #4973 → #4974 |
| #4970 | refused: it has 4 tips above it, and owl names them |
| #5124 | #4969 → #5124 |

The walk down from a PR is unique: one head branch has one open PR
from this repository. A cross-repository PR is never a lower layer,
since its head is in a fork. The walk up follows the one PR on top
until it reaches a tip. Where there are several, it stops and lists
every tip above. A layer stays in the line while it is open, and a
merged one drops out, because GitHub retargets the layer above when
the branch is deleted.

## Discovery: one query

The open PRs of the repository, with `number title url baseRefName
headRefName headRefOid isCrossRepository isDraft author`, in one paged
GraphQL search, and the graph is built locally. At bardo-system's size
that is two pages and well under two seconds. Walking the chain with
one `gh pr view` per layer would take a call per layer.

The PR list asks the same question across its scope, once per fetch
round, so every row can carry its place in a stack.

## The commands

| command | does |
|---|---|
| `owl pr stack <N> [--json]` | prints N's line, or the tips to choose from |
| `owl pr open <N> --stack` | opens, or focuses, the workspace for N's line |
| `owl pr close --stack [<N>]` | closes it; without N, the stack workspace you are in |

`owl pr open <N>` without `--stack` is unchanged. Any layer can be
reviewed on its own while the stack is open, in its own `pr-<N>`
workspace.

## The workspace

| | |
|---|---|
| name | `stack-<slug of the tip's title>`, decided at first open and never renamed |
| worktree | detached at the tip's head, under `worktrees_dir` like the rest |
| container | the reviews', beside `pr-<N>` |
| record | `<git common dir>/owl/stacks/<name>.json`: the tip and the line, bottom first |
| prompt | `agent.stack_prompt`, by default `/owl:review {pr} stack`: `{pr}` is the tip, `{prs}` the line |

The record is how the workspace is found again, since no PR number is
in the name. **A record matches the line that passes through its
tip.** Each open writes the line as it is now, so the record follows
the line as it grows: a layer pushed on top of the tip extends it, and
the next open makes that layer the tip. A fork below the recorded tip
gives a line that does not pass through it, and that line gets a
workspace of its own.

The name comes from the tip and not the bottom, because lines that
share a bottom are told apart by their tips. A tip whose title slugs
to nothing, or to a name another record holds, takes `stack-<N>` with
the tip's number.

`close` removes the worktree and the window and keeps the record, as
a PR's close keeps the conversation: opening the line again finds the
name, and Claude resumes the conversation stored under it.

## Notes between sessions

A stack session and a per-layer session on the same PR each learn
things the other should know. Claude Code sessions can message each
other, but a message is gone once read. The notes are files:

```
$XDG_STATE_HOME/owl/reviews/<owner>/<name>/
  pr-<N>.md         written by the session reviewing PR N on its own
  stack-<…>.md      written by a stack session, one per workspace
```

Every file has one writer, so two sessions never edit the same file.
Every session reads all of them. `owl pr stack --json` names the
directory. The skill decides what goes in a note: findings with their
layer, and what has been posted where.

## The review skill, in stack mode

- **The overview comes first, always:** what each layer does, bottom
  up, and what the upper layers rely on from the lower ones.
- **One brief for the line**, when the line is structural.
- **A finding goes on the layer whose diff holds the line.** GitHub
  rejects a review comment on a line outside the PR's diff, and the
  layer that introduced a defect is where it is fixed.
- **Checks run once, at the tip.** The tip's tree holds every layer.
- **A draft layer is context.** It is read for the overview and gets
  no review of its own unless the user asks.
- **Posting is per layer and optional.** Each layer's review is its
  own draft file and needs its own approval, as a single review does.
  The skill reads the notes first and never posts what another session
  already posted on that layer.

## Staleness

The list keeps its rule: a review on a commit other than the head is
waiting for you. A rebase of a lower layer moves every head above it,
so the whole line goes amber at once. Telling a rebase from a change
needs the commit the review covers, the head and the base, and
`git patch-id` over both diffs, against the base each layer had when
it was reviewed: against the base as it is now, a rewrite below makes
an unchanged layer read as changed. The check stays on demand, in the
skill, and is never run for every row of the list.

## In the list

A stacked row carries `≡3/6`: third layer of a six-layer line. Below
a fork the total is not one number, so the row carries `≡3`. `right`
on a stacked row opens the stack view: the line, or the tree where it
forks, one row per layer with its CI, your review state and the `⎇`
of an open workspace. Enter on a layer opens its own review, and Enter
on a tip opens the stack review of its line.

## Not in this design

- Renaming a workspace when its line changes. The name is fixed at
  first open, like a PR's.
- herdr has no workspace description to show the line in. owl's list
  and the notes show it.
- A stack across repositories. GitHub has no such thing.

# The project layer

A design note, not a description of what owl does. Nothing here is
built. It records what was decided, what it costs, and what is still
open, so the session that builds it starts from something.

owl has two scopes today: reviews (`owl pr`) and features (`owl issue`).
This is the third — projects — and an agent that works at that level,
shaping and dispatching the issue work rather than doing it.

## What breaks first

owl's unit is a workspace: a worktree, the branch checked out in it,
and a window, all named identically, with the key in the name. `close`,
`inferIssue`, `handle()` and the overlay all read that name back. A PR
has a branch. An issue has a branch. **A project has none**, so the
first decision is what a project workspace checks out — not what the
list looks like.

## The shape

**A project workspace is a worktree like any other.** What varies is
where the issue work under it happens, and that is a property of the
project, not of owl:

| Mode | The project's worktree | The issues under it |
| --- | --- | --- |
| `separate` (default) | A reading room on the remote's default branch, refreshed on open. The agent plans, dispatches and reviews; it never commits feature code. | Each gets its own worktree, branch and window, exactly as today. |
| `shared` | A branch of the project's own. | No worktree of their own: a window on the project's worktree, carrying the issue's prompt. |

`shared` has a consequence worth stating plainly rather than
discovering: **one worktree means one branch, which means one PR.**
Twelve shadow-validation issues that are really one change want that.
Four milestones that ship separately do not.

The mode is a default per project, kept in the repo (`.owl/projects.json`,
structured so a session updates it without prose to preserve), and
overridable per invocation. Projects change mode as the work changes.

Workspace names: `proj-<slug>` from Linear's project slug, beside
`pr-<N>` and `<key>-<slug>`. One more branch of `isWorkspaceName`, one
more `scope` value whose windows live in `project.session`.

## One conversation at the top, forked downward

Branches and contexts are separate axes, and conflating them is what
makes this design hard to talk about:

| | one branch | many branches |
| --- | --- | --- |
| **one context** | the single change: many issues, one PR, one conversation | one conversation moving between worktrees, an issue per branch |
| **many contexts** | broken — concurrent writes to one tree | what owl does today: parallel, isolated, cold every time |

The branch axis is the `separate`/`shared` mode above. The context axis
is settled and not per project: **there is exactly one project
conversation, and issue conversations are forked from it.**

Claude Code has the flags for this, so none of it is a hack:

```
--session-id <uuid>   use a specific session ID
--resume <id>         resume a conversation by session ID
--fork-session        when resuming, create a new session ID
--add-dir <dirs...>   additional directories to allow tool access
```

The trick is *where the fork runs*. Claude stores sessions under a slug
of the working directory, so a session recorded in the project worktree
is only resumable from the project worktree. The issue window therefore
keeps the **project worktree as its cwd**, and reaches the issue's
worktree through `--add-dir`:

```
owl project open sven-v2
  claude --session-id <uuid owl generated> -n sven-v2

descend into BAR-2931          # cwd stays the project worktree
  claude --resume <uuid> --fork-session \
         --add-dir .worktrees.local/bar-2931-… \
         "work BAR-2931; its worktree is .worktrees.local/bar-2931-…"
```

The child opens holding the whole project conversation and then
diverges; `--fork-session` is what leaves the parent untouched. owl
generates the project's UUID itself and keeps it in `.owl/projects.json`,
so it never reads Claude's session files or guesses an id.

A second visit to an issue **resumes that issue's own conversation**,
not a fresh fork: what was tried and what a reviewer objected to is
worth more than the project's newer picture, which the state file
carries anyway. So the file holds a session id per issue as well:

```json
{ "sven-v2": {
    "session": "a1b2…",
    "mode": "separate",
    "issues": { "BAR-2931": "c3d4…", "BAR-2932": "e5f6…" } } }
```

### The join is the half that matters

A tree that only forks downward goes stale at the top. Every fork
carries what the project knew *at the moment of the fork*, and nothing
a sibling learns afterwards climbs back — after a week the overview
conversation is the least informed one in the tree while the knowledge
sits in five leaves.

So the child writes and owl nudges. The issue agent appends what it
found to the project's state; owl already detects `agentDone` per
workspace and can type into any window, so when a child finishes the
parent gets a line:

```
BAR-2931 finished (PR #4201). Read the project state for what it found.
```

Written rather than spoken, because compaction eats a conversation and
not a file. This is the same durable plan named below — it is not a
convenience next to the fork model, it is what keeps the fork model
from decaying.

## The layer is the project, the sections are its milestones

Linear's hierarchy is initiative → project → milestone → issue. The
agent sits at the project, and the view groups the project's issues by
milestone — which is where the specs and the progress numbers actually
live in this workspace.

The granularity is not consistent, and the design has to survive that.
Emission Categories is four projects (Plumbing, Backstage integration,
Capture integration, Data foundation) — one body of work split by
domain, with the dependencies written in prose inside the milestones.
An agent scoped to one of the four cannot see the constraint that binds
them. Sven v2 is the opposite: one project whose four milestones are
each a full spec with gates and open questions.

So the project is the right unit to *own a workspace*, and the
initiative is the layer that would eventually carry the dependencies
between them. Not now; noted so the naming does not foreclose it.

Milestone coverage is uneven, and the view cannot assume it. Checked
against the live workspace: Sequential Capture redesign has all but one
of its issues on a milestone, across six of them (M3, M3.5, M5, M5.5,
M8, M9); Sven v2 has one of eight. So a milestone section is a section
like any other — it appears when it has members — and everything else
falls into one at the end for issues with no milestone. Names run long
("M3.5 — Downstream compatibility & activation prerequisites
(pre-cutover gate)"); the leading identifier is the part that carries.

Volume is the other thing the numbers say. Sequential Capture redesign
holds more than thirty issues; twelve are the user's. That gap is
exactly what makes `owl issue --project` worth having, and exactly why
the project view needs the section collapse the issue list does not.

## owl launches and shows; the agent decides

Most of the orchestration already exists. A project agent can run
`owl issue start BAR-4079 --prompt "…"`, `owl issue close`, `owl issue`.
It does not need an engine. It needs context and a way to see what its
children are doing:

1. **The project's issues.** `owl issue` lists what is assigned to the
   user. A project agent needs the project's issues whoever owns them:
   `owl issue --project <name|slug>`. One more filter on the Linear
   side, which already takes its filter as a variable.
2. **Machine-readable state.** owl has no `--json` anywhere. The agent
   state — working, blocked, done — exists only inside the TUI, via
   `fetchLocalIn`. A project agent that dispatches three issue agents
   cannot currently see any of them. This is the smallest piece and the
   one everything else leans on.
3. **A durable plan.** What was dispatched, when, and what came back,
   in a file the next session reads. Linear holds the scope and the
   specs; it does not hold the agent's working state.

**What owl does not do: supervise.** No retries, no dependency
ordering, no restarting a blocked agent. The moment it does, it owns
failure semantics and stops being a launcher. The line is worth
defending because the failure modes on the other side of it are
expensive and invisible.

## Cost, and the one thing that will bite

A project agent plus N issue agents is N+1 concurrent Claude sessions
on one repository. Worktrees keep them from writing over each other;
nothing keeps them from costing what they cost.

A forked child holds the project conversation *and* its own work, so it
starts heavy and compacts sooner than a fresh session would. That is
the price of arriving warm, and the state file is the hedge.

The mechanical hazard is the branch. A child with `--add-dir` onto a
sibling worktree can commit to the wrong one, and `git -C <worktree>`
in a prompt is a convention where an enforcement belongs: a per-worktree
hook refusing a commit whose branch does not carry that worktree's key
would make it impossible rather than discouraged. owl already writes
`.git/info/exclude` per worktree, so it has the hook into that.

## Open

- **The project branch in `shared` mode.** Named after the project
  slug, or after the milestone in play? A milestone-named branch gives
  a PR per milestone, which is closer to how these projects ship.
- **`close` on a shared project** that still has issue windows open on
  its worktree. Refuse, or close them with it?
- **Where the mode lives** if a project spans repositories. Today's
  worktrees are per repo, so `.owl/projects.json` is per repo, and a
  project that touches two repos has two files and no link between
  them.
- **What the child appends, and who writes it.** The agent, told to by
  its prompt, or owl, from what it can observe (the PR, the branch, the
  exit state)? The first is richer and unreliable; the second is thin
  and always happens. Probably both, in different fields.
- **Whether a fork can be refused.** An issue whose work is unrelated
  to the project's current thread would be better off cold than
  carrying twenty thousand tokens of someone else's milestone.

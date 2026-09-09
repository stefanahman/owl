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

## Cost

A project agent plus N issue agents is N+1 concurrent Claude sessions
on one repository. Worktrees keep them from writing over each other;
nothing keeps them from costing what they cost.

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

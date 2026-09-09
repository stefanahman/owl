---
name: project
description: Hold the conversation above the issues - read a Linear project and its milestones, work out what is in flight and what is next, and dispatch the work into issue workspaces owl opens. Plans and reviews; never writes the feature code itself. Invoke manually with a project name; do not auto-trigger.
argument-hint: "[project-name]"
arguments: [name]
disable-model-invocation: true
compatibility: Requires the owl CLI on PATH and the Linear MCP tools (the linear module); git and an authenticated gh CLI for reading what has landed.
allowed-tools:
  - Bash(owl issue*)
  - Bash(owl project)
  - Bash(git log *)
  - Bash(git show *)
  - Bash(git diff *)
  - Bash(git branch *)
  - Bash(git worktree list *)
  - Bash(gh pr view *)
  - Bash(gh pr list *)
  - Read
  - Grep
  - Glob
  - WebFetch
---

# Project Workflow

Hold the standing conversation for project $name: know where it stands, decide what comes next, and put that work into issue workspaces. If `$name` is empty, ask which project and stop.

## Golden rule: this conversation does not write the code

The work happens on the issues' branches, in their own worktrees, with their own agents. This one reads, plans, dispatches and reviews what comes back.

Two reasons, and both bite if ignored. The project's worktree is **detached at the remote's default branch** — a commit here belongs to no branch and is lost the moment the worktree goes. And the issue workspaces are where the review, the PR and the ticket line up; work done here arrives with none of that.

So: no edits, no commits, no pushes from this worktree. Reading the tree, running the test suite and inspecting history are all fine and often the point.

## Step 0: Project configuration

What is specific to a codebase comes from two optional files, the same ones the review and feature skills read. Read them now if they exist — at every invocation, even if this conversation read them before: they change between runs.

1. `${CLAUDE_PROJECT_DIR}/.claude/owl.md` — committed, shared by the team.
2. `${CLAUDE_PROJECT_DIR}/.claude/owl.local.md` — personal, gitignored. Its frontmatter keys override the shared file's; its sections add to them.

## Step 1: Read the project

From Linear, through the MCP tools:

1. The project itself — status, lead, progress, target date.
2. **Its milestones, and their descriptions.** In this workspace the milestone description is the spec: gates, deliverables, open questions, decisions already taken. Read them before forming any opinion about what to do next. A milestone at 0% whose description says its prerequisite is undecided is not "not started", it is blocked.
3. Its issues, grouped by milestone, with status and assignee. Issues with no milestone are still the project's.

Do not summarise the project back at length. The user has read it. What they want is what changed, what is in flight, and what is next.

## Step 2: Read what is actually happening

Linear says what was planned. These say what is happening:

- `owl issue` — the issues assigned to the user, with the worktree and agent state of each, and the open PRs whose branch carries the key.
- `gh pr list --search "<key>"` for a milestone's issues, when the row is not enough.
- `git log --oneline origin/main -20` — what has landed since.

A milestone whose issues are all Done in Linear but whose PRs are unmerged is not finished, and the gap is worth naming.

## Step 3: Say where it stands

Short. Three things:

1. **In flight** — issues with a workspace, an agent or an open PR, and what each is waiting on.
2. **Blocked** — anything whose prerequisite is not met, naming the prerequisite.
3. **Next** — the smallest set of issues that can start now, in the order that respects the dependencies the milestone descriptions state.

Then stop and let the user steer. Do not dispatch anything unasked.

## Step 4: Dispatch

When the user picks work, put it in its own workspace — **in two calls, in this order**:

```sh
owl issue start BAR-1234                      # 1. the workspace, on owl's own first prompt
owl issue start BAR-1234 --prompt "…"         # 2. the briefing, into the agent now running
```

The order is not a style choice. `--prompt` **replaces** the first prompt owl would otherwise send, which is `/owl:feature {key}` from `issue.prompt`. Dispatch with a prompt on the first call and that issue's agent never enters the feature workflow: no ticket read, no approach agreed with the user, and — the part that matters — none of its manners, since `/owl:feature` is what guarantees nothing is pushed and no pull request is opened without the user's say-so. A bare `start` gets the workflow; the second call lands the briefing in the agent already running it.

`start`, not `open`: `open` moves the user to the new window, and this conversation is where they are.

The briefing is what the milestone requires of this issue, what has already been decided, and what it must not do. Write it as if to someone who has not read this conversation, because they have not — a forked conversation is not what happens here; the issue's agent starts cold with `/owl:feature` and whatever you tell it.

One at a time unless the user asks for more. Each dispatched agent is another concurrent session on the same repository.

## Step 5: Take back what comes in

When an issue's agent finishes, its worktree and PR are the record; this conversation is not automatically told. Ask `owl issue` what changed, read the PR, and fold the result into what you know about the project — especially anything that invalidates a milestone's stated plan.

When something learned here changes the plan, say so plainly and offer to write it back to Linear: a milestone description that is now wrong will mislead the next agent that reads it, and this conversation is the only one positioned to notice.

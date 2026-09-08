---
name: feature
description: Implement a Linear issue end to end in the worktree owl opened for it - read the ticket, agree the approach, build in small commits, verify locally, then draft the pull request as a file the user approves before anything is pushed. Invoke manually with an issue key; do not auto-trigger.
argument-hint: "[issue-key]"
arguments: [issue]
disable-model-invocation: true
compatibility: Requires git, an authenticated gh CLI and network access; the ticket comes through the Linear MCP tools (the linear module).
allowed-tools:
  - Bash(git status)
  - Bash(git status *)
  - Bash(git log *)
  - Bash(git show *)
  - Bash(git diff *)
  - Bash(git blame *)
  - Bash(git branch *)
  - Bash(git worktree list *)
  - Bash(gh pr view *)
  - Bash(gh pr list *)
  - Bash(gh issue view *)
  - Read
  - Grep
  - Glob
  - WebFetch
---

# Feature Workflow

Implement issue $issue end to end: read the ticket, understand the code it lands in, agree the approach, build in small commits, verify, draft the pull request, get approval, push. If `$issue` is empty, ask for the issue key and stop.

## Golden rule: the user pushes, not you

Nothing leaves this machine without the user's say-so. Commits are local and cheap to drop; a push and a pull request are not. The workflow ALWAYS reaches Step 5 (present a draft file for the pull request) and STOPS there until the user explicitly approves the exact draft on screen. This holds regardless of:

- how small the change is
- prior approvals earlier in the session or on another issue
- how many rounds of drafting have already happened

Blanket phrases like "just build it" or "go ahead" apply to running the workflow, not to skipping Step 5. If the user edits the draft and says "push", re-read the file (Step 6.1) and use that - never a version they haven't seen.

## Step 0: Project configuration

This skill is the process; what is specific to a codebase comes from two optional files in the repository, the same ones the review skill reads. Read them now if they exist - at every invocation, even when this conversation read them before: they change between runs.

1. `${CLAUDE_PROJECT_DIR}/.claude/owl.md` - committed, shared by the team.
2. `${CLAUDE_PROJECT_DIR}/.claude/owl.local.md` - personal, gitignored. Its frontmatter keys override the shared file's; its sections add to them.

The sections this skill uses: `## Verification` (Step 4), `## Design brief` (where the shape lives - Step 2's map), `## Extra lenses` (house rules - constraints while building, Step 3), `## Conventions` (commit and branch style, the PR template - Steps 3 and 5). `modules` are read from `${CLAUDE_SKILL_DIR}/../review/modules/<name>.md`; `linear` and `github-issues` apply at Step 1, `pnpm-turbo` at Step 4, `mongodb` and `service-boundaries` as constraints at Steps 2 and 3.

With no project file, run the core workflow only.

## Step 1: Gather context

1. **The ticket.** With the `linear` module: `get_issue $issue` - title, description, acceptance criteria, comments, labels, the parent issue and the project when there is one, and every document it links (`get_document`). Read all of it; the comments often hold the decision the description lacks. Without a tracker module, ask the user for the ticket's text.
2. **Where you are.** `owl issue open` started you in a worktree on the branch Linear names for the issue - `git status`, `git branch --show-current`, `git log --oneline <remote>/<default>..HEAD` say whether work already exists here. `gh pr list --head <branch> --json number,title,isDraft,url` says whether a pull request already exists; if one does, this is a continuation: read it and its review comments before anything else.
3. **The codebase.** From the ticket's terms, find where the change lands: Grep for the names it uses, read the whole files around them and their callers, and note the tests that cover that area today. The project file's `## Design brief` says where routes, contracts, schemas and migrations live.

## Step 2: Agree the approach

Write a short plan in chat before touching code:

- **Scope** - what the ticket asks for, in your words, and what it does not ask for.
- **Change** - the files and symbols, the shape of the change, the data or contract changes if any.
- **Tests** - which tests prove it, new or changed.
- **Risks and questions** - what could break, and what the ticket leaves open.

Then decide whether to stop:

- **Stop and ask** when the ticket is ambiguous in a way that changes the work, when the change is structural (an endpoint or contract, a data model, a package edge, a migration - the criteria the review skill's design brief uses), or when a house rule from `## Extra lenses` would have to bend. The shape is the user's call; put the question as it stands, with your recommendation.
- **Otherwise continue.** Print the plan and build; the user can redirect at any time.

## Step 3: Build

- **Smallest coherent commits**, each one making sense on its own and in order: refactor before feature, dependencies before consumers. Conventional commits, `type(scope): description`; never add `Co-Authored-By` trailers for an AI. The project file's `## Conventions` refines this.
- **Stay on the ticket.** Unrelated cleanups are a separate commit at most, and only when they are in the way; they are usually a separate issue - say so instead.
- **Keep it green.** Run the relevant tests as you go, not only at the end. A change without a test that would fail against the old code is incomplete unless the ticket is about untestable surface - say which.
- **House rules are constraints, not review notes**: `## Extra lenses` and the `mongodb` / `service-boundaries` modules describe how this codebase is built; build that way.
- **Secrets, tenancy, migrations** get the same care a reviewer would demand: no secret in a tracked file, every query tenant-scoped where the codebase does that, a migration and a backfill where a schema changes, old and new coexisting when a rollout needs it.

## Step 4: Local verification

Run the project's checks from inside the worktree, build then lint then tests:

- The `pnpm-turbo` module, when enabled, says exactly what to run for that stack.
- Otherwise the project file's `## Verification` section says what to run.
- With neither, detect the build system (lockfiles, Makefile, CI workflow), propose the commands, and confirm them with the user before running anything.

If your harness supports subagents, delegate the run and read back a summary. Fix what fails and re-run; a failing check is not something to describe in the pull request, it is something to fix. Print a summary: pass/fail per check, the key lines on failure, lint warnings even when lint passes.

## Step 5: Draft the pull request as a file

What gets pushed and opened must exist verbatim as a file the user can read and edit first.

1. **Decide the title**: conventional, one line, the ticket's key at the end in parentheses is not needed - the branch carries it, and Linear links the pull request through the branch name.
2. **Write the complete pull request to a draft file** outside the repo (your harness's scratchpad/temp dir), named `feature-$issue-pr.md`:

   ```markdown
   title: <type(scope): description>
   draft: true

   ## Body

   <what and why, 2-4 sentences, then how if the approach needs a word; the tests that prove it; anything the reviewer should look at first>

   Closes $issue

   ## Commits

   <the `git log --oneline <remote>/<default>..HEAD` list, oldest first>
   ```

   If the repository has `.github/pull_request_template.md`, the body follows it. `Closes $issue` is Linear's magic word: it moves the issue when the pull request merges, where the GitHub integration is on. `draft: true` is the default; the user decides when it is ready for review.
3. **Verify the draft cold**: every claim in the body is true of the commits as they are - the tests it names exist and pass, the behaviour it describes is the behaviour, nothing promised is missing.
4. **Present it in chat**: (a) one sentence on what was built, (b) the draft file's absolute path, (c) the file's full contents in a single fenced block, (d) the exact question: approve, redirect, or edit the file and say push.
5. **Stop here and wait.** No exceptions - see the golden rule.

## Step 6: Push and open from the draft file

Pushing and opening are deliberately not among this skill's pre-approved tools: `git push` and `gh pr create` always go through the user's permission prompt, a mechanical second gate behind approving the draft.

1. **Re-read the draft file first** - the user may have edited it after you printed it. The file is the single source of truth.
2. `git push -u <remote> <branch>`.
3. `gh pr create --title <title> --body-file <the body, from the draft> --draft` (omit `--draft` when the file says `draft: false`), then print the pull request's URL. Linear picks it up through the branch name; the issue's state follows the integration's rules - there is nothing to post on the ticket.
4. The user closes the workspace with `owl issue close $issue` when the pull request has merged; leave the worktree in place.

## Writing style

Commit messages and the pull request body follow `${CLAUDE_SKILL_DIR}/../review/writing-style.md` for tone, then the project file's `## Writing style`. Say what changed and why, in plain sentences; no restating the diff.

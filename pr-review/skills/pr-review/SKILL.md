---
name: pr-review
description: Review a pull request end to end - gather context, read the diff with its surrounding code, verify locally, draft the review as a file the user approves, then post it with gh as one atomic review. Invoke manually with a PR number; do not auto-trigger.
argument-hint: "[pr-number]"
arguments: [pr]
disable-model-invocation: true
compatibility: Requires git, an authenticated gh CLI, and network access. Modules may need more (Linear MCP tools, pnpm).
allowed-tools:
  - Bash(gh pr view *)
  - Bash(gh pr checks *)
  - Bash(gh pr diff *)
  - Bash(gh pr checkout *)
  - Bash(gh issue view *)
  - Bash(git worktree add *)
  - Bash(git worktree list *)
  - Bash(git worktree remove *)
  - Bash(git log *)
  - Bash(git show *)
  - Bash(git diff *)
  - Bash(git blame *)
  - Bash(git status)
  - Bash(git status *)
  - Read
  - Grep
  - Glob
  - WebFetch
---

# PR Review Workflow

Review pull request #$pr end to end: gather context, read the code carefully, run local checks, draft the review, get approval, post. If `$pr` is empty, ask for the PR number and stop.

## Golden rule: the user posts, not you

The user is the driver on every review. The workflow ALWAYS reaches Step 4 (present a draft file) and STOPS there until the user explicitly approves the exact draft on screen. This holds regardless of:

- verdict (yes, even a clean `approve` with no inline comments — draft first)
- prior approvals earlier in the session or on a different PR
- how "obvious" the review looks
- how many rounds of drafting have already happened

Blanket phrases like "just do the reviews" or "go ahead" apply to running the workflow, not to skipping Step 4. Each individual review needs its own explicit approval of its own draft file. If the user edits the draft and says "post", re-read the file (Step 5.1) and post that — never a version they haven't seen.

## Step 0: Project configuration

This skill is the review *process*; what is specific to a codebase comes from two optional files in the repository. Read them now if they exist — at every invocation, even when this conversation read them before: they change between runs.

1. `${CLAUDE_PROJECT_DIR}/.claude/pr-review.md` — committed, shared by the team.
2. `${CLAUDE_PROJECT_DIR}/.claude/pr-review.local.md` — personal, gitignored. Its frontmatter keys override the shared file's; its sections add to them.

Both have the same shape: YAML frontmatter, then markdown sections named after the step they extend.

```markdown
---
modules: [linear, pnpm-turbo]      # shipped modules to enable (see below)
ticket_pattern: 'PROJ-\d+'         # read by the tracker modules
brief: file                        # the design brief of Step 2: file (default), or artifact as well
---

## Verification
<how to build, lint and test this repo — used by Step 3>

## Design brief
<where the shape lives in this repository: routes, contracts, schemas, migrations — used by Step 2's brief>

## Extra lenses
<what to look for in this codebase — applied alongside Step 2's lenses>

## Writing style
<additions to writing-style.md for this team's review voice>
```

For each name in `modules`, read `${CLAUDE_SKILL_DIR}/modules/<name>.md` and apply it at the step its first line names (`Applies to: Step N`). Shipped modules: `linear`, `github-issues`, `pnpm-turbo`, `mongodb`, `service-boundaries`. An unknown name is not an error — say so once and continue.

With no project file, run the core workflow only. Nothing here pauses the workflow.

## Step 1: Gather context

Before reviewing code, gather the context that should inform the review:

1. **PR metadata**: run one JSON call and consult the parsed object downstream — one round-trip, richer than the default view:

   ```sh
   gh pr view $pr --json \
     title,body,author,baseRefName,headRefName,\
     isDraft,autoMergeRequest,mergeStateStatus,reviewDecision,latestReviews,\
     labels,changedFiles,additions,deletions,isCrossRepository,closingIssuesReferences
   ```

   Notable fields and how they inform the review:
   - `isDraft: true` — stop and confirm with the user before continuing. Reviewing a draft is usually premature and unwelcome.
   - `autoMergeRequest` (non-null) — auto-merge is armed. Changes the Step 4 verdict rules (see there).
   - `mergeStateStatus` — `BLOCKED` / `DIRTY` / `UNSTABLE` are worth mentioning in the body as context, but don't gate the verdict (the author handles conflicts).
   - `reviewDecision` + `latestReviews` — if prior reviews exist, read them before drafting yours (see Step 4 for how to use them).
   - `labels` — `security` / `hotfix` / `breaking-change` / `wip` / `do-not-merge` shift review tone and depth.
   - `changedFiles` / `additions` / `deletions` — size signal. Very large diffs may warrant confirming scope with the author before deep-reading everything.
   - `isCrossRepository: true` — external contributor from a fork. Warmer tone; more onboarding context in comments.
   - `closingIssuesReferences` — the issues GitHub links to this PR.
2. **CI status**: run `gh pr checks $pr`. Note which checks are failing (or missing) — that shapes both review depth and the eventual verdict, and points at concrete problems worth reading closely.
3. **Tracker context**: the enabled tracker module (`linear`, `github-issues`) says how to find and read the linked issues and documents. Without one, read what `closingIssuesReferences` points at with `gh issue view`.

## Step 2: Review the code carefully

Take your time here — think hard. A confirmed bug beats ten vague suspicions.

Read the diff, but never *from the diff alone* — surrounding context is where bugs hide. With the metadata from Step 1 in hand, dive into the diff: `gh pr diff $pr`, then read broadly (below).

### Read broadly

For every changed file:

- Read the entire file, not just the changed lines.
- For each changed function or exported symbol: search the repository for its callsites (Grep), read at least one.
- For each removed identifier: search for it — catch stale references and broken imports.
- Pay as much attention to what the PR *removes* as what it adds. A dropped guard, retry, or defensive check is a common bug source; a subtly loosened condition (`>` → `>=`, an early return removed) hides in the diff between two similar-looking lines.

If you find yourself skimming, stop and re-read.

### Design brief — when the PR changes the shape of the system

Decide from the file list and what you have now read — not from the description — whether the PR is *structural*: an endpoint or RPC contract added, removed or changed; a data model, schema, collection, index or migration; a service or package added, removed or split; a dependency edge between packages that did not exist before. The project file's `## Design brief` section can say where these live in this repository. If none applies, skip this section.

For a structural PR, write the brief before looking for findings: the shape of a change is the user's call, and they need the map before the details. Write it to `pr-$pr-brief.md` in the directory Step 4 uses for the draft, and print it in full.

The brief is a map drawn from the code — coordinates or nothing:

1. Every row names the file and symbol it comes from. A row that can't is not written.
2. What the repository doesn't show — consumers outside it, traffic, intent — is written as *not determined*, never inferred.
3. The PR description is input for the first section only; nothing else is taken from it.
4. Observations, not verdicts: "`ReportService.facets` reads the collection directly; the other four callers go through the messaging layer" is allowed, "this is the wrong approach" is not.
5. One screen. Tables over prose. No sentence that restates the diff.

Six sections, in this order:

```markdown
# Design brief — #<pr> <title>

## Description versus code
matches / diverges — per divergence: what the description says, what the code does, file:line

## Shape, before → after
| area | before | after | where |
only the rows that change: components and flow, endpoints or contracts, data models, package dependencies

## Compatibility
| change | additive / breaking | consumers in this repository |
breaking: removed or renamed, optional made required, narrowed enum, changed error shape; consumers outside the repository: not determined

## Migration and reversibility
migration or backfill: present (file) / none · old and new coexist during rollout: yes / no · door: two-way (rollback keeps working) / one-way (dropped column, rewritten data)

## Companions
| tests for the new shape | observability | docs | deprecation of what it replaces |
present (file) / missing — no advice

## Questions of approach
the three or four questions the decision hinges on, phrased to be put to the author as they stand
```

Then stop and ask: discuss the approach first, or go on to the findings? Concerns about the shape — the approach, the boundaries, the model — become findings only from what the user decides here; the lenses below cover correctness, contracts, tests, security, performance and maintainability, not the shape.

With `brief: artifact` in the project file and the Artifact tool available, also publish the brief as a page, with a mermaid diagram of the flow where the flow changed; the file stays the source.

### Review with these lenses in mind

Hold all of these simultaneously while reading — don't do separate passes. Roughly by severity:

1. **Correctness & safety** — logic bugs; edge cases (null/undefined, empty collections, boundaries, off-by-one, edge dates); async races and idempotency; error paths (swallowed, re-thrown, logged, cleaned up); resource leaks.
2. **Contracts & compatibility** — for every changed signature, exported type, schema, or API contract: trace the consumers; check migrations, backfills, and backwards compatibility with in-flight requests, older service versions, and persisted state. No stealth breaking changes in shared packages.
3. **Tests** — is there a test that would have caught the current code as broken? Do tests assert on observable behavior, not implementation details? Missing test coverage for a changed behavior is a blocking finding.
4. **Security** — input validation, authN/authZ, secret handling, injection surfaces (SQL/NoSQL, SSRF, path traversal, prototype pollution), dep-audit new packages.
5. **Performance & reliability** — hot paths, N+1, unbounded loops, blocking I/O, queries without a matching index. For infra changes: runtime version bumps, health/readiness checks, environment variable contracts.
6. **Maintainability** — naming, dead code, module boundaries, over-abstraction. Rarely blocking.

Plus the lenses the enabled modules add (`mongodb`, `service-boundaries`) and the project file's `## Extra lenses`.

Also cross-check against Step 1 context: does the PR meet the linked issue's acceptance criteria? Anything over-engineered beyond what was asked?

### Every finding needs a concrete failure

For each candidate, write:

- **File and line** — exact `path:line` (or range).
- **Severity** — `blocking`, `non-blocking`, or `nit` (same taxonomy Step 4 uses).
- **Defect** — one sentence.
- **Failure scenario** — concrete inputs/state → wrong output/behavior. Example: *"when `activity.originalCurrency` is null (existing prod data), `resolveRate()` throws `TypeError` instead of falling back to tenant currency"*.

If you can't articulate the failure concretely, **drop the finding**. Vague suspicions train the reviewer to ignore you.

### Adversarial verify — try to refute each finding

This step earns the findings you keep. Take the time to refute yourself — killing a weak claim is as valuable as finding a strong one.

Before recording a finding, deliberately try to prove yourself wrong:

- Is there a guard clause, upstream check, type narrowing, or framework default that handles this?
- Is there a test that already covers it? If yes and it passes, figure out why the test doesn't fail *before* flagging.
- Is there a comment, ticket, or design decision that makes the behavior intentional?

**Default to refuted.** A finding survives only when you can name the specific input/state where the code is confirmed broken *and* can't state a mechanism by which it's correct. When you confirm a real bug, glance at nearby code — bugs cluster — but no formal re-loop is needed.

### Present the review

Print to the user:

- **What the PR does** — 2-3 sentences in your own words, not the description verbatim.
- **Confirmed findings** — ordered by severity, each with file:line + failure scenario.
- **Notes** — advisory suggestions, questions, nits (optional; skip if nothing worth saying).

Zero surviving findings is a valid outcome. Approve with a body that names what you actually checked — do not invent filler concerns to look thorough.

## Step 3: Local verification in a worktree

If the working directory is already a worktree for this PR — `pr-owl open` starts you in one, a directory named `pr-$pr` or `pr-$pr-…` — skip the setup. Otherwise create one first (`git worktree add <path>` then `gh pr checkout $pr` inside it).

Then, from inside the worktree, run the project's checks:

- The `pnpm-turbo` module, when enabled, says exactly what to run for that stack.
- Otherwise the project file's `## Verification` section says what to run.
- With neither, detect the build system (lockfiles, Makefile, CI workflow), propose the install / build / lint / test commands, and confirm them with the user before running anything.

Run build → lint → tests sequentially (build first — lint and tests usually depend on compiled output). If your harness supports subagents, delegate the run to a single subagent so the output doesn't clutter the main context, and pass it the exact commands and any command constraints verbatim.

Then:

1. **Summary**: for each check (build, lint, tests) print pass/fail; on fail, the key error lines. Include lint warnings even if lint passes. If everything's green, one line saying so.
2. **Cleanup**: if you created the worktree yourself, remove it with `git worktree remove <path>`. If the caller provided the worktree (`pr-owl open`), leave it — the launcher owns its lifecycle.

If any check fails, factor the failure into the review — it likely warrants a blocking comment or shifts the verdict.

## Step 4: Draft the review as a file

The chat analysis from Step 2 is context, not the deliverable. What gets posted must exist verbatim as an artifact the user can read and edit before anything reaches GitHub — never ask for approval of a review the user can only see paraphrased.

1. **Decide the verdict**: `approve`, `request-changes`, or `comment`.
   - Any high-severity issue (correctness bug, security flaw, contract break without a migration plan) → `request-changes`.
   - `approve` only if no high-severity issues, contracts/migrations are safe, and critical paths are tested.
   - `comment` when issues are real but non-blocking, or for questions without gatekeeping.
   - **Auto-merge is armed** (`autoMergeRequest` from Step 1 is non-null) → `approve` requires ZERO non-blocking comments. Any nit, "consider X", or FYI note → downgrade to `comment`. Rationale: on auto-merge, approve merges the PR immediately; comments attached to the approval land in an already-merged PR the author has no reason to revisit.
   - **Prior reviews exist** (`reviewDecision` from Step 1 is `APPROVED` or `CHANGES_REQUESTED`) → read `latestReviews` bodies before finalizing yours:
     - If prior `APPROVED`: fresh eyes are the point. Look for what earlier reviewers missed, and challenge in the body if warranted.
     - If prior `CHANGES_REQUESTED`: don't duplicate the same findings. Also actively check whether any prior finding is wrong (author already fixed it, or the reviewer misread) — if so, say so explicitly in the body so the misdirection doesn't linger.
2. **Write the complete review — exact final text, not a summary of it — to a draft file** outside the repo (your harness's scratchpad/temp dir), named `pr-$pr-review-draft.md`:

   ```markdown
   verdict: comment

   ## Body

   <review body, 2-4 sentences, exactly as it will be posted>

   ## <path>:<line>

   <inline comment text, exactly as it will be posted>

   ## <path>:<line>

   <next inline comment>
   ```

   One `## <path>:<line>` section per finding. Comment text: 1-3 sentences plus an optional suggested fix; if it runs long, split the finding or move context to the body — walls of prose in an inline comment get skimmed. Every `<line>` must be a line present in the PR diff, or GitHub rejects the whole review.
3. **Verify every point, cold, before anyone sees the draft.** What is posted carries the user's name; a finding that is not true is the worst outcome of this workflow. For each finding in the file: re-open the cited file at the cited line and confirm the code says what the finding says; confirm the failure scenario follows from that code and nothing upstream prevents it — a guard, a type, a test, a framework default; confirm the line is in the PR diff (`gh pr diff $pr`). If your harness supports subagents, hand each finding to a fresh one with the file, the line, the claim and the scenario, and the single instruction to refute it; keep a finding only when it comes back confirmed at the same coordinates. Rewrite what survived with a changed scenario, drop the rest, re-decide the verdict from what is left, and print the tally: `verified N, dropped M` with the reason for each drop. A draft with zero findings after this is a good draft.
4. **Present it in chat**: (a) one sentence of verdict rationale, (b) the draft file's absolute path, (c) the file's full contents in a single fenced block with nothing interleaved. That fenced block IS the review — nothing may be posted that isn't in it.
5. **Stop here and wait.** Ask the user to approve, redirect in chat, or edit the file directly and tell you to post. No exceptions — see the golden rule at the top. "Continue", "sounds good", "you decide" on an unrelated earlier turn are NOT approval of this draft. Only an explicit go on this specific draft moves you to Step 5.

## Step 5: Post from the draft file

Posting is deliberately not among this skill's pre-approved tools: the `gh api` call below always goes through the user's permission prompt, a mechanical second gate behind the approval of the draft.

1. **Re-read the draft file first** — the user may have edited it after you printed it. The file is the single source of truth; post its contents verbatim (verdict, body, comments), no rewording, no additions.
2. Post everything in a single `gh api` call to `repos/{owner}/{repo}/pulls/$pr/reviews` so the verdict and all inline comments land as one atomic review. Map verdict → `event`: `approve` = `APPROVE`, `request-changes` = `REQUEST_CHANGES`, `comment` = `COMMENT`.
3. Use `--input -` with a heredoc for the JSON payload — do not inline complex JSON in shell arguments.
4. After posting, print the review URL.

## Writing style

For tone, voice, and formatting of the review body and inline comments, follow `${CLAUDE_SKILL_DIR}/writing-style.md`, then the project file's `## Writing style` section on top of it. Read both before drafting the verdict and comments in Step 4.

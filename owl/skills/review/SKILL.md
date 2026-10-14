---
name: review
description: Review a pull request end to end - gather context, read the diff with its surrounding code, verify locally, draft the review as a file the user approves, then post it with gh as one atomic review. With `stack`, review a stack's line from the trunk to that PR as a whole and draft a review per layer. Invoke manually with a PR number; do not auto-trigger.
argument-hint: "[pr-number] [stack]"
arguments: [pr, mode]
disable-model-invocation: true
compatibility: Requires git, an authenticated gh CLI, and network access. Modules may need more (Linear MCP tools, pnpm).
allowed-tools:
  - Bash(gh pr view *)
  - Bash(gh pr checks *)
  - Bash(gh pr diff *)
  - Bash(gh pr checkout *)
  - Bash(gh issue view *)
  - Bash(owl pr stack *)
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

If `$mode` is `stack`, #$pr is the tip of a stack and the review covers its whole line: read "Stack mode" below before Step 1, and apply it at every step.

## Golden rule: the user posts, not you

The user is the driver on every review. The workflow ALWAYS reaches Step 4 (present a draft file) and STOPS there until the user explicitly approves the exact draft on screen. This holds regardless of:

- verdict (yes, even a clean `approve` with no inline comments — draft first)
- prior approvals earlier in the session or on a different PR
- how "obvious" the review looks
- how many rounds of drafting have already happened

Blanket phrases like "just do the reviews" or "go ahead" apply to running the workflow, not to skipping Step 4. Each individual review needs its own explicit approval of its own draft file. If the user edits the draft and says "post", re-read the file (Step 5.1) and post that — never a version they haven't seen.

## Step 0: Project configuration

This skill is the review *process*; what is specific to a codebase comes from two optional files in the repository. Read them now if they exist — at every invocation, even when this conversation read them before: they change between runs.

1. `${CLAUDE_PROJECT_DIR}/.claude/owl.md` — committed, shared by the team.
2. `${CLAUDE_PROJECT_DIR}/.claude/owl.local.md` — personal, gitignored. Its frontmatter keys override the shared file's; its sections add to them.

Both have the same shape: YAML frontmatter, then markdown sections named after the step they extend.

```markdown
---
modules: [linear, pnpm-turbo]      # shipped modules to enable (see below)
ticket_pattern: 'PROJ-\d+'         # read by the tracker modules
brief: file                        # the design brief of Step 2: file (default), page (an HTML page next to it), or artifact (that page published)
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

## Stack mode

A stack is open pull requests built on each other: each one's base is the head branch of the one below, and the bottom's base is the trunk. A line is the path from the trunk to one tip, and it is exactly the code the tip would merge. `owl pr open <N> --stack` opens a worktree detached at the tip's head, so its tree holds every layer. Each step below still applies; this section says what changes.

**The line.** Run `owl pr stack $pr --json`. `prs` is the line, bottom first, in gh's field names; `notes` is the notes directory. **The tip is the last PR in `prs`**: #$pr when the review was opened, or a PR pushed on top of it since, which the line now runs to. Everything below that says the tip means that PR, never `$pr`. If `tips` is not empty, the stack forked above the line: name the tips and ask which line to review, then stop.

**Notes.** Read every file in the notes directory before Step 1. `pr-<N>.md` is what a session reviewing PR N alone wrote, and `stack-<…>.md` is what a stack session wrote. Take what they found and what they posted as context, never as findings of your own: verify a finding before you repeat it. Write your own notes to `<notes>/<workspace>.md`: the worktree directory's name when owl opened it (`stack-…`), else `stack-<tip>`. Write them after Step 2 and again after Step 5, and never write another session's file. A layer's base at review is the head of the layer below, and for the bottom the trunk's commit; full shas, since a re-review diffs against them:

```markdown
# <workspace>, line #<bottom> → #<tip>

| PR | base at review | head at review | verdict | state |
|---|---|---|---|---|
| #<N> | <sha> | <sha> | comment | drafted · posted <review url> · not reviewed (draft PR) |

## Findings
- #<N> <path>:<line> · blocking · <the defect, one sentence> · drafted / posted / dropped (<why>)
```

**Step 1, per layer.** Run the metadata call and `gh pr checks` for every layer. A draft layer is context: read it for the overview, and give it no review of its own unless the user asks. Do not stop for it.

**Step 2, the overview first.** Before any finding, print the line bottom up: for each layer, two or three sentences on what it does, and what the layers above it rely on from it (file:symbol). This overview is printed on every stack review, whatever else the user asked for. A structural line gets one design brief for the whole line, named `stack-<tip>-brief.md`. Then read each layer's diff (`gh pr diff <N>`) with the lenses, in the tip's tree.

**Each finding belongs to one layer:** the one whose diff holds the line it cites. GitHub rejects a review whose comment sits on a line outside the PR's diff, and the layer that introduced a defect is the one where it is fixed. A defect that only shows in the combination, such as an upper layer relying on something a lower one does not guarantee, goes on the upper layer's line that relies on it, and its text names the lower layer.

**Step 3 runs once, at the tip.** In the `stack-…` worktree owl opened, skip Step 3's setup: the worktree holds the tip's head as of the first open. Compare `git rev-parse HEAD` with the tip's `headRefOid`; when they differ and `git status --porcelain` is empty, fetch `pull/<tip>/head` from the remote and `git checkout --detach FETCH_HEAD`. Anywhere else, never move a checkout owl did not open: fetch `pull/<tip>/head`, run `git worktree add --detach <path> FETCH_HEAD`, check there, and remove it afterwards as Step 3 says. A failing check belongs to the layer that introduced the failing code.

**Step 4, a draft per layer.** Write one draft file per layer that has findings or a verdict to post: `stack-<tip>-pr-<N>-review-draft.md`, a name no review of PR N alone writes, each verified cold as Step 4.3 says. Present a table first, one row per layer with its verdict, its number of findings and its draft's path, then each draft in full. Posting is optional per layer, and the user picks which to post. The golden rule holds per file: each draft needs its own explicit approval.

**Step 5, one review per layer.** Post each approved draft as its own atomic review on its own PR. Before each one, re-read the notes and run `gh pr view <N> --json reviews,headRefOid`. When a review by your account (`gh api user -q .login`) has `commit.oid` equal to `headRefOid` and a body that is not empty, that head already has your review, from another session or by hand: show its URL and ask before posting a second. A reply in a review thread is recorded as a review of its own, with an empty body, on the head of the moment; it is no review of the code. Use `reviews`, not `latestReviews`, whose `commit.oid` gh leaves empty.

**Re-review.** A rebase of one layer moves the head of every layer above it, so a new head says nothing about whether a layer changed. For each layer, compare `git diff -U0 <base at review>...<head at review> | git patch-id --stable` from the notes with the same over its current base and head. The same id means the layer moved and none of its code changed. Re-read only the layers whose id differs, and say which. Diff against the base recorded at review, never the base branch as it is now: when a layer below was rewritten, the old head's diff against the new base also holds the old commits of the layer below, and the ids differ for nothing. Without notes, the old head is the `commit.oid` of your newest review with a body, and `git range-diff <current base> <old head> <current head>` names the layer's changed commits; the commits of a rewritten layer below show there as removed, and are not this layer's.

**A single PR in a stack.** Without `stack`, run `owl pr stack $pr --json` in Step 1 when the `owl` command exists. When the PR is a layer (`prs` holds more than one PR, or `tips` is not empty), read the notes directory as above, say in the summary where the PR sits in the line, write your notes to `<notes>/pr-$pr.md`, and before posting in Step 5 run the same check for a review already on the head.

## Step 1: Gather context

Before reviewing code, gather the context that should inform the review:

1. **PR metadata**: run one JSON call and consult the parsed object downstream — one round-trip, richer than the default view:

   ```sh
   gh pr view $pr --json \
     title,body,author,baseRefName,headRefName,headRefOid,\
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

1. Every claim about the code names the file and symbol it comes from, or the file and line where the line is the point. A claim that can't is not written. A claim taken from the ticket or the description says so instead, as rule 3 allows. The coordinates sit beside the sentence, not inside it: in a table row's label cell, after the label, or on a `where:` line after the paragraph. A sentence names an identifier only when the reader needs it to find something.
2. What the repository doesn't show — consumers outside it, traffic, intent — is written as *not determined*, never inferred. A number the description or the ticket gives is theirs, not an inference, and appears as rule 3 says.
3. The PR description and the ticket are input for the first two sections only — what the change is for, and where description and code part. A number only they hold, such as a production count, may appear elsewhere when the sentence says whose number it is.
4. Observations, not verdicts: "the facets query reads the collection directly; every other caller goes through the messaging layer", with `ReportService.facets` on its `where:` line, is allowed; "this is the wrong approach" is not.
5. Written for a reader who has seen neither the ticket nor the code. Each domain term is explained in one plain sentence the first time it appears. Short sentences, one idea each: cause, then effect; a sentence over about 30 words is two. A number only when it is the point.
6. Tables over prose. No sentence that restates the diff.
7. The brief names the head it was drawn at: Step 1's `headRefOid`, for a line the tip's. A later session redraws it at the current head and never reuses one drawn at another, unless Re-review's patch-id shows the code unchanged between the two.

Seven sections, in this order:

```markdown
# Design brief — #<pr> <title>
drawn at <head sha>, <date>

## What it is for, and where it sits
for a reader who has not seen the ticket, in three sentences: the capability in the product's own terms — the problem it solves, for whom — from the ticket, the description and the repository's docs, and what it replaces, extends or is the first of. Then the terms it rests on, one line each. The term the change turns on, when it is easier shown than said, gets one real instance from the repository, such as a rule file or a config row, with its file, and never a secret, a credential or an internal address. For a PR reviewed alone that is a layer of a stack: what it takes from the layers below and what the layers above take from it, read from their code; a line's brief leaves that to Stack mode's overview. Intent the repository cannot show: *not determined*
the flow it sits in, one numbered step per line, in plain words: what triggers it, what comes before and after, what it reads and writes
where: the entry point and the stores, file:symbol

## Description versus code
matches / diverges — per divergence: what the description says, what the code does, and what follows from the difference; where: file:line

## Shape, before → after
| area, and its file | before | after |
only the rows that change: components and flow, endpoints or contracts, data models, package dependencies

## Compatibility
| change | additive / breaking | consumers in this repository |
only what something outside the changed package can see: contracts, schemas, stored documents, types another package imports, and what people type or set — commands, flags, config keys, environment variables. breaking: removed or renamed, optional made required, narrowed enum, changed error shape; additive in the new version but rejected by a replica still on the old one: say so; consumers outside the repository: not determined

## Migration and reversibility
migration or backfill: present (file) / none · old and new coexist during rollout: yes / no · undo: a revert is enough / a revert leaves <what stays: a dropped column's data, rewritten or deleted documents>, and what restoring it takes

## Companions
| tests for the new shape | observability | docs | deprecation of what it replaces |
present (file) / missing / n/a (why) — no advice

## Questions of approach
Q1 to Q4: the three or four questions the decision hinges on, phrased to be put to the author as they stand, each ending in a question mark. Under each: what depends on the answer; what was checked, and what its premise leaves not determined; an earlier review or comment that raised it, and whether it was answered, read with `gh pr view $pr --json reviews,comments` (Step 1's `latestReviews` keeps only each reviewer's newest review), and since inline review threads are not in that read, "none found" says what was read; a where: line. A row elsewhere that backs a question ends with its number, as (Q1)
```

The questions stay last in the file, beside the ask that follows. Then stop and ask: discuss the approach first, or go on to the findings? Concerns about the shape — the approach, the boundaries, the model — become findings only from what the user decides here; the lenses below cover correctness, contracts, tests, security, performance and maintainability, not the shape.

With `brief: page` in the project file, also write the brief as an HTML page next to the file, under the file's name with `.html` (`pr-$pr-brief.html`, or `stack-<tip>-brief.html` for a line), and print its `file://` path for the user to open in a browser; with `brief: artifact` and the Artifact tool available, publish that page as well and print the link. A page written as a local file opens with `<!doctype html>`, `<meta charset="utf-8">` and the viewport line; a publish adds all three itself, so the published page leaves them out. The file stays the source. The page holds the same content in two layers: the plain text is always visible, and each claim's coordinates fold into a collapsed `Where in the code` block beneath it. It opens with the questions of approach, since a page is read from the top, and a term a question uses is explained there, where it first appears (rule 5). The flow's numbered steps are the diagram's numbered nodes. Load the `artifact-design` skill for the page when it is available, and draw the whole flow as inline SVG, marking what the PR changes (the `artifact-diagramming` skill): it renders in a browser and on the artifact host alike, and its colours follow the page's theme through `currentColor`. Not mermaid — only the host renders it, and a `%%{init}` directive overrides the host's per-theme palette and leaves labels unreadable in the other theme.

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
- **Failure scenario** — concrete inputs/state → wrong output/behavior. Example: *"when `order.originalCurrency` is null (existing prod data), `resolveRate()` throws `TypeError` instead of falling back to the account's currency"*.

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

If the working directory is already a worktree for this PR — `owl pr open` starts you in one, a directory named `pr-$pr` or `pr-$pr-…` — skip the setup. Otherwise create one first (`git worktree add <path>` then `gh pr checkout $pr` inside it).

Then, from inside the worktree, run the project's checks:

- The `pnpm-turbo` module, when enabled, says exactly what to run for that stack.
- Otherwise the project file's `## Verification` section says what to run.
- With neither, detect the build system (lockfiles, Makefile, CI workflow), propose the install / build / lint / test commands, and confirm them with the user before running anything.

Run build → lint → tests sequentially (build first — lint and tests usually depend on compiled output). If your harness supports subagents, delegate the run to a single subagent so the output doesn't clutter the main context, and pass it the exact commands and any command constraints verbatim.

Then:

1. **Summary**: for each check (build, lint, tests) print pass/fail; on fail, the key error lines. Include lint warnings even if lint passes. If everything's green, one line saying so.
2. **Cleanup**: if you created the worktree yourself, remove it with `git worktree remove <path>`. If the caller provided the worktree (`owl pr open`), leave it — the launcher owns its lifecycle.

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

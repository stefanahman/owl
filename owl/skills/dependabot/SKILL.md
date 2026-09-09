---
name: dependabot
description: Take a Dependabot pull request from opened to mergeable - read the bump and what changed upstream between the two versions, check it against how this repository uses the package, verify locally, fix what the bump broke in small commits on the bot's branch, then draft the review as a file the user approves before anything is pushed or posted. Invoke manually with a PR number; do not auto-trigger.
argument-hint: "[pr-number]"
arguments: [pr]
disable-model-invocation: true
compatibility: Requires git, an authenticated gh CLI and network access. The stack module (pnpm-turbo) says how to verify; the tracker modules are not needed.
allowed-tools:
  - Bash(gh pr view *)
  - Bash(gh pr checks *)
  - Bash(gh pr diff *)
  - Bash(gh pr checkout *)
  - Bash(gh release view *)
  - Bash(gh release list *)
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

# Dependabot Workflow

Take Dependabot pull request #$pr from opened to mergeable: read the bump, read what changed upstream, check it against this repository, verify, repair what the bump broke, draft the review, get approval, push and post. If `$pr` is empty, ask for the PR number and stop.

A review judges a change; this workflow judges it and then makes it mergeable. It writes to the bot's branch, which `review` never does, and it keeps to what the bump caused, which `feature` need not.

## Golden rule: the user pushes and posts, not you

Nothing leaves this machine without the user's say-so. The workflow ALWAYS reaches Step 6 (present a draft file) and STOPS there until the user explicitly approves the exact draft on screen. This holds regardless of:

- verdict (a clean patch bump with nothing to say still gets a draft first)
- prior approvals earlier in the session or on another Dependabot PR
- how many rounds of drafting have already happened

Two things make it weigh more here than on a review. Where the repository auto-merges Dependabot pull requests, **an approval is a merge**: the workflow merges the moment the approval lands and the PR is mergeable. And commits pushed to the bot's branch are the user's name on a bot's work. Blanket phrases like "just handle the Dependabot PRs" apply to running the workflow, not to skipping Step 6. If the user edits the draft and says "post", re-read the file (Step 7.1) and post that.

## Step 0: Project configuration and the repository's Dependabot policy

Read the project files if they exist - at every invocation, they change between runs:

1. `${CLAUDE_PROJECT_DIR}/.claude/owl.md` - committed, shared by the team.
2. `${CLAUDE_PROJECT_DIR}/.claude/owl.local.md` - personal, gitignored; frontmatter keys override, sections add.

Sections this skill uses: `## Verification` (Step 4), `## Extra lenses` and `## Conventions` (Step 5), and `## Dependencies` when present - the repository's own dependency rules: packages pinned and why, versions known to break, subtrees with a lockfile of their own. Modules come from `${CLAUDE_SKILL_DIR}/../review/modules/<name>.md`; `pnpm-turbo` applies at Step 4, `mongodb` and `service-boundaries` as constraints at Step 5.

Then read the repository's Dependabot policy, which is code, not prose:

- `.github/dependabot.yml`: the ecosystems and directories, `ignore` entries and the comments beside them (a maintainer wrote down why a version is skipped - that reasoning applies to neighbouring bumps too), `groups`, `versioning-strategy`, `open-pull-requests-limit`.
- Workflows that mention Dependabot (Grep `.github/workflows` for `dependabot`): whether approvals trigger a merge, for which update types, and which paths or ecosystems are excluded. Note the exact conditions; Step 6 states what an approval will do.

## Step 1: Read the bump

One JSON call, then read the body:

```sh
gh pr view $pr --json \
  title,body,author,labels,headRefName,baseRefName,commits,files,\
  isDraft,mergeStateStatus,autoMergeRequest,reviewDecision,latestReviews
```

- `author.login` is `dependabot[bot]` (or `app/dependabot`). Anything else: this is not a Dependabot PR - say so and stop.
- **Single or grouped.** A single bump's body starts `Bumps [pkg](url) from A to B.`; a grouped one starts `Bumps the <group> group … with N updates` and lists one `Updates \`pkg\` from a to b` line per package. Work per package from here on.
- **Ecosystem and directory** from the branch (`dependabot/npm_and_yarn/<dir>/<pkg>-<version>`, `dependabot/github_actions/…`), the labels (`dependencies`, the ecosystem, `major`/`minor`/`patch` where the repository sets them) and the title's scope (`deps` production, `deps-dev` development).
- **Semver class** per package, from the two versions: patch, minor, major, or a pre-1.0 minor that the ecosystem treats as breaking.
- **Security or version update.** A security update names an advisory (GHSA id, CVSS) in the body; it may carry a compatibility score - the share of public CI runs that passed on this exact bump - which is a signal, not a verdict.
- **The body's own sections**: `Release notes`, `Changelog`, `Commits` (collapsed `<details>` blocks), `Maintainer changes` (the package changed hands - read Step 2 with more care), and `Dependabot commands and options`, which lists the comment commands this PR accepts.
- **Commits on the branch.** More than Dependabot's own means someone already worked here: read those commits before anything else, and expect Dependabot to have stopped rebasing this PR.
- **Repository policy for this PR**: from Step 0, whether an approval merges it (update type, paths), or whether it needs a hand.
- `gh pr checks $pr`: what CI says today, and which job fails.

## Step 2: Read what changed upstream

Between the two versions, for each package:

1. The PR body's release notes and changelog first - Dependabot pastes them from the package's repository.
2. Then the source: the package's GitHub releases between the versions (`gh release list` / `gh release view` on the upstream repository the body links), its `CHANGELOG.md` at the new tag, or the compare view (`…/compare/vA...vB`) with WebFetch when the notes are thin. For a GitHub Actions bump: the action's releases, and whether the workflow pins by tag or by commit SHA.
3. Collect, with the version they arrived in: breaking changes, removed or renamed APIs, deprecations that now warn or throw, changed defaults, new peer or engine requirements (Node, TypeScript, a framework major), build or config format changes, and security fixes that the repository relies on.

A major with no notes is not a green light; it is a reason to read the compare view.

## Step 3: Check it against this repository

The changelog says what changed; the repository says what matters.

- Grep the package's imports and the APIs named in Step 2: every call site of a removed, renamed or behaviour-changed symbol; config files the package reads (`next.config.*`, ESLint configs, `tsconfig`, CI workflows for an action).
- Peer and engine requirements against `package.json` (workspace catalogs included), the pinned runtime, and the other packages that depend on this one - a peer the bump now wants at a major the repository does not have is a blocker, not a fix.
- The lockfile: is it updated consistently with the manifest, and only for this bump? A subtree with a lockfile of its own that Dependabot did not regenerate (see `## Dependencies` and Step 0's policy) fails a frozen install until someone runs the install there.
- `.github/dependabot.yml`'s `ignore` reasoning: a bump that reintroduces a chain a maintainer pinned against is a request-changes with the pin's reason, not a fix.

Decide the size of the work before touching anything:

- **Nothing to change**: the bump is compatible as it stands. Go to Step 4.
- **Adaptations**: a handful of call sites, a config key, a lockfile to regenerate. Go to Step 4, then Step 5.
- **A migration**: a major that changes an API used across the codebase, a runtime the repository does not run, a peer major it does not have. **Stop and ask.** Print the plan and its size - files, call sites, the peer chain - and the options as they stand: the migration as a feature of its own, waiting on the peer, or `@dependabot ignore this major version` (the user's comment, never yours). The shape is the user's call.

## Step 4: Verify in the worktree

If the working directory is already a worktree for this PR - `owl pr open` starts you in one, a directory named `pr-$pr` or `pr-$pr-…` - skip the setup. Otherwise `gh pr checkout $pr` into a worktree (`git worktree add`) and work there.

Run the project's checks, build then lint then tests, scoped to the packages the bump touches when the stack module can scope them:

- The `pnpm-turbo` module, when enabled, says exactly what to run.
- Otherwise `## Verification` says what to run.
- With neither, detect the build system and confirm the commands with the user before running anything.
- A subtree with its own toolchain runs its own install and checks the way CI does (the frozen install, the subtree's test command).

If your harness supports subagents, delegate the run and read back a summary. Print pass/fail per check with the key lines on failure, and reconcile with `gh pr checks`: a failure only in CI is still the bump's failure until shown otherwise.

## Step 5: Fix what the bump broke

Only when Step 3 found adaptations or Step 4 found failures, and only what the bump caused.

- **Small commits, each about the bump**: the call sites for one removed API, the config key, the regenerated lockfile - conventional commits in the prefix the PR's own title uses (`build(deps): …`, `fix(deps): …`, or the project's `## Conventions`), no `Co-Authored-By` trailers for an AI.
- **Lockfiles are regenerated with the repository's own tool at the pinned version** (`pnpm install`, `bun install` in the subtree), never edited by hand, and committed on their own.
- **House rules stay rules**: `## Extra lenses` and the constraint modules apply to your commits as they would to anyone's.
- **Unrelated breakage is reported, not fixed**: a test that was already red on the base branch, a deprecation warning from another package. Say so in the draft.
- Re-run Step 4 until green.

What your commits change about this PR, and the draft must say:

- Dependabot stops rebasing a pull request once extra commits have been pushed to it. The branch will not follow the base branch on its own anymore.
- Never ask for `@dependabot recreate`: it recreates the PR and overwrites every edit made to it, your commits included. `@dependabot rebase` is only safe before any human commit exists.
- Dependabot's metadata action yields nothing for a PR that is not purely Dependabot's commits, and auto-merge workflows built on it will not arm. A PR you fixed is merged by a person.

## Step 6: Draft the review as a file

What gets posted must exist verbatim as a file the user can read and edit.

1. **Decide the verdict.**
   - `approve` when Step 3 found nothing that touches the repository or Step 5's fixes are in and Step 4 is green. Where the repository auto-merges this update type on approval and no human commit exists, the approval is the merge: say so in the body's last line.
   - `request-changes` when the bump reintroduces something the repository pins against, wants a peer or runtime it does not have, or needs the migration of Step 3 - with the reason and the option, in the body.
   - `comment` when the bump is fine but a person must act: a subtree lockfile to reconcile, a major to merge by hand, a human commit that switched auto-merge off.
   - **Prior reviews exist**: read them first; a bot reviewer's findings are verified like anyone's, not repeated.
2. **Write the complete review to a draft file** outside the repo (your harness's scratchpad/temp dir), named `pr-$pr-dependabot.md`, in the review skill's format:

   ```markdown
   verdict: approve

   ## Body

   <the bump in one line: package, A → B, class, security or version update>
   <what changed upstream that matters here, or "nothing between A and B touches this repository", with the call sites or config that were checked>
   <verification: build/lint/tests, scoped how, result; CI's state>
   <fixes pushed or to push, one line each with the commit subject; or none>
   <what merging takes here: auto-merge arms on approval / merged by hand because … / the subtree lockfile first>

   ## <path>:<line>

   <an inline comment only where a line of the diff itself needs one - rare on a bot's diff>
   ```

   For a grouped PR, one line per package under the first heading.
3. **Verify every claim cold**: the versions, the changelog items, the call sites, the check results, the merge consequence. The body carries the user's name.
4. **Present it in chat**: (a) one sentence of verdict rationale, (b) the draft file's absolute path, (c) the file's full contents in one fenced block, (d) the commits waiting to be pushed, if any, (e) the exact question: approve and post, redirect, or edit the file and say post.
5. **Stop here and wait.** No exceptions - see the golden rule.

## Step 7: Push and post from the draft file

Pushing and posting are deliberately not among this skill's pre-approved tools: `git push` and the `gh api` call always go through the user's permission prompt, a mechanical second gate behind approving the draft.

1. **Re-read the draft file first** - the user may have edited it after you printed it. The file is the single source of truth.
2. `git push` the fix commits, when there are any, to the PR's branch (`origin` and the head ref from Step 1). Wait for the checks to run when the verdict depends on them.
3. Post everything in one `gh api` call to `repos/{owner}/{repo}/pulls/$pr/reviews` so the verdict and any comments land as one atomic review; map `approve` → `APPROVE`, `request-changes` → `REQUEST_CHANGES`, `comment` → `COMMENT`; `--input -` with a heredoc for the JSON. Print the review's URL.
4. When the user decides against the bump, the Dependabot commands are theirs to comment: `@dependabot ignore this major version` (or `minor`, `patch`, `this dependency`; for a grouped PR, `@dependabot ignore <name> major version`), which closes the PR and stops those updates until the version is reached another way; `@dependabot show <name> ignore conditions` lists what is already ignored. Name the right one in the draft; never post it.

## Writing style

The body follows `${CLAUDE_SKILL_DIR}/../review/writing-style.md`, then the project file's `## Writing style`. Versions and package names exact; no restating the changelog.

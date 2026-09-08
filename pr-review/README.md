# pr-review — a Claude Code plugin

One skill, `/pr-review:pr-review <number>`, that reviews a pull request
the way a careful senior engineer would and never posts without you:

1. **Context** — `gh pr view` metadata (draft? auto-merge armed? prior
   reviews? size? external contributor?), CI status, the linked issues.
2. **Careful reading** — the diff plus the whole files around it and
   their callers; every finding needs a concrete failure scenario and
   survives an attempt to refute it. Vague suspicions are dropped.
3. **A design brief, when the shape changes** — endpoints or contracts,
   data models, services or package edges: a one-screen map drawn from
   the code (before → after, compatibility, reversibility, companions,
   questions), every row with its file, unknowns marked, no verdict.
   The skill stops here so you can judge the approach: the shape is
   yours, not the agent's.
4. **Local verification** — build, lint and tests in a worktree for the
   PR (the one `pr-owl open` puts you in, or one the skill creates).
5. **A draft file, verified cold** — every finding re-checked at its
   file and line (by a fresh subagent where the harness has them) and
   dropped unless confirmed, then the exact body and inline comments
   written to a file you can edit. The skill stops here, every time.
6. **One atomic post** — after your explicit go, `gh api` posts verdict
   and comments as a single review and prints the URL.

The skill is the process. Everything specific to a codebase — tracker,
build commands, house rules, review voice — comes from a file in the
repository, so the same plugin serves a Go CLI and a TypeScript
monorepo.

## Install

```
/plugin marketplace add stefanahman/pr-owl
/plugin install pr-review@pr-owl
```

Requires git and an authenticated `gh`. The skill pre-approves what it
needs to read the PR and check it out — `gh pr view/checks/diff/checkout`,
`gh issue view`, `git worktree add/list/remove`, `git log/show/diff/blame/status`,
`Read`, `Grep`, `Glob`, `WebFetch` — and only for its own turn. Posting the review
(`gh api`) is not pre-approved on purpose: it goes through your
permission prompt every time, a second gate behind approving the
draft. The build and test commands of Step 3 run under your normal
permission settings too; the `pnpm-turbo` module keeps to commands a
`Bash(pnpm:*)` rule can safely auto-allow and never uses `pnpm exec`,
`pnpm dlx` or `npx`.

## Configure a repository

Two optional files, same format, read in order:

- `.claude/pr-review.md` — committed, shared by the team.
- `.claude/pr-review.local.md` — yours; add `.claude/*.local.md` to
  `.gitignore`. Frontmatter keys override the shared file's, sections
  add to them.

```markdown
---
modules: [linear, pnpm-turbo, mongodb, service-boundaries]
ticket_pattern: 'PROJ-\d+'
brief: artifact
---

## Verification
Integration tests need `docker compose up -d db` first.

## Design brief
Routes live in `apps/*/src/routes/`, RPC contracts in `packages/contracts/`, collection schemas in `packages/db/src/schemas/`, migrations in `packages/db/migrations/`.

## Extra lenses
- Tenant-scoped collections are keyed on `tenantId`; every query filters by it.
- Soft deletes: filter `deletedAt: null` unless the code says why not.
- RPC contracts (Zod) live in `packages/contracts/`, never in the consuming service.

## Writing style
Prefer `suggestion` blocks over prose for one-line fixes.
```

- `modules` — shipped add-ons to enable (below).
- `ticket_pattern` — how issue ids look in titles, bodies and branches; read by the tracker modules.
- `brief` — `file` (default) or `artifact`: with the Artifact tool available, the design brief is also published as a page, with the flow diagram.
- `## Verification` — how to build, lint and test this repo (Step 3). With a stack module enabled, this adds to it.
- `## Design brief` — where the shape lives in this repository: routes, contracts, schemas, migrations. Tells the brief where to look.
- `## Extra lenses` — what to look for in this codebase, applied alongside the built-in lenses (Step 2). This is also where the modules read their project specifics.
- `## Writing style` — additions to the shipped [writing-style.md](skills/pr-review/writing-style.md).

Without any file the core workflow runs on its own; Step 3 then detects
the build system and confirms the commands with you before running
them.

## Modules

| Module | Step | What it adds |
|---|---|---|
| `linear` | 1 | Linked Linear issues and documents via the Linear MCP tools |
| `github-issues` | 1 | Linked GitHub issues via `gh issue view` |
| `pnpm-turbo` | 3 | `pnpm install`, scoped `turbo build/lint/test`, and the command constraint that keeps `Bash(pnpm:*)` safe to auto-allow |
| `mongodb` | 2 | Index coverage, tenant scoping, soft deletes, schema migrations, aggregation and write hazards |
| `service-boundaries` | 2 | Services talk only through the messaging layer; contracts and shared packages are public API |

Each module is one markdown file under
[skills/pr-review/modules/](skills/pr-review/modules/); the first line
says which step it plugs into. A project-specific rule that doesn't fit
a module goes in your project file's sections — no fork needed.

## Hacking

```sh
claude --plugin-dir ./pr-review        # from the pr-owl checkout
claude plugin validate ./pr-review --strict
```

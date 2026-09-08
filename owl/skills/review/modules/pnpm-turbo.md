Applies to: Step 3 (local verification) — pnpm workspaces built with Turborepo.

From inside the worktree:

1. **Install**: `pnpm install`.
2. **Scope**: derive the affected packages from `gh pr view <pr> --json files --jq '.files[].path'` and map the paths to workspace packages. Fall back to the full workspace only if the scope is unclear.
3. **Build → lint → tests, sequentially** (build first — lint and tests depend on compiled output):
   - **Build**: `pnpm turbo build --filter <package>...` (the trailing `...` includes upstream dependencies). If it fails, report the error and skip tests (lint can still run).
   - **Lint**: `pnpm turbo lint --filter <package>...`. Report errors AND warnings.
   - **Tests**: `pnpm test` from the affected package directory, or `pnpm turbo test --filter <package>...` for broader scope.
4. **Command constraint** — include this verbatim when delegating to a subagent: only `pnpm install`, `pnpm run <script>` / `pnpm -C <dir> run <script>`, and `pnpm turbo <task> --filter <pkg>`. Never `pnpm exec`, `pnpm dlx`, or `npx` — they execute an arbitrary binary named in their arguments, so a `Bash(pnpm:*)` permission rule can't safely auto-allow them. For per-file lint or test detail, parse the full task output (the turbo cache replays it for free) instead of re-invoking the tool per file.

The project file's `## Verification` section can name package-specific scripts or extra checks on top of this.

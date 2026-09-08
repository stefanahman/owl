Applies to: Step 2 (review lenses) — a monorepo of services that talk through a shared messaging layer.

Add to the contracts & compatibility lens:

- **One way to talk** — services communicate through the project's RPC / messaging layer only. Direct reads or writes into another service's database or collections are a blocking finding, however convenient.
- **Contracts live in one place** — request/response schemas belong to the shared contracts package, not to the consuming service. A contract defined inline in a consumer, or duplicated, is a finding; a changed contract needs every consumer traced (search the workspace for the schema's name) and a compatibility story for the deploy window when old and new versions run side by side.
- **Shared packages are public API** — a change to a shared package's exports is a breaking change for every consumer unless it is additive. Look for stealth breaks: narrowed types, changed defaults, removed exports, reordered parameters.
- **Version alignment** — shared SDK and client versions stay aligned across services; a bump in one service alone is a finding unless the PR says why.

The project file's `## Extra lenses` section names the specifics: the messaging mechanism, the path of the contracts package, and the shared packages that count as public API.

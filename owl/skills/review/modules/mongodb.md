Applies to: Step 2 (review lenses) — MongoDB data access.

Add to the performance & reliability and correctness lenses:

- **Index coverage** — every new or changed query, `find` filter, `$match` stage, sort and `$lookup` must be served by a leading index on that collection. Check the collection's index definitions (schema files, migrations, `createIndex` calls); a query that only matches an index's trailing fields scans. Flag as blocking on hot paths and large collections.
- **Tenant scoping** — in multi-tenant collections, every query filters by the tenant key; a missing tenant filter is a data-leak bug, not a performance nit.
- **Soft deletes** — collections that soft-delete must filter deleted documents out on read unless the code says why not.
- **Schema changes** — new required fields, renamed fields and type changes need a migration or backfill and must tolerate documents written by the previous version while both run.
- **Aggregations** — `$lookup` and `$unwind` on unbounded arrays, `$group` without a preceding `$match`, and pipelines that can't use an index in their first stage.
- **Writes** — `updateMany`/`deleteMany` filters (an empty filter is catastrophic), upsert races, and multi-document updates that assume atomicity without a transaction.

The project file's `## Extra lenses` section names the specifics: the tenant key, the soft-delete field, which databases hold multi-tenant data, and any convention this module should hold the PR to.

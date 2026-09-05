Applies to: Step 1 (gather context) — Linear as the issue tracker.

Needs the Linear MCP tools (`get_issue`, `get_document`). If they aren't available in this session, skip this module silently.

1. Extract issue identifiers from the PR title, body, branch name and commit messages. Match the project file's `ticket_pattern` (for example `PROJ-\d+`), the bracketed form `[PROJ-123]`, and Linear issue URLs.
2. For each identifier, `get_issue` and read the description, acceptance criteria and comments. If the issue references a parent issue or a project, fetch those too.
3. For documents linked from the PR body or the issues (Linear docs, design docs, RFCs), `get_document` — or WebFetch for external links.

Carry the acceptance criteria into Step 2's spec cross-check: does the PR do what the ticket asked, and nothing it didn't?

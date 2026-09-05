Applies to: Step 1 (gather context) — GitHub Issues as the issue tracker.

1. Collect the linked issues: `closingIssuesReferences` from the Step 1 metadata, plus `#123` / `owner/repo#123` references and issue URLs in the PR title, body, branch name and commit messages. The project file's `ticket_pattern` can add another form.
2. For each, `gh issue view <number> --json title,body,labels,comments` (add `--repo` for cross-repo references) and read the description, any acceptance criteria and the discussion.
3. Follow links from the issue or PR body to design docs and RFCs with WebFetch.

Carry the acceptance criteria into Step 2's spec cross-check: does the PR do what the issue asked, and nothing it didn't?

# Keys

| key | on a PR | on an issue | on a project |
|---|---|---|---|
| `↓`/`j` `↑`/`k` `g` `G` `pgup` `pgdn` | move; section headers are skipped | the same | the same |
| `n` | next PR that needs you: todo, or Claude blocked or done | next issue whose Claude needs you | next project whose Claude needs you |
| `tab` `shift+tab` | the other pane: to review, or your own | with several Linear workspaces, the next or the previous one; the list shows one at a time | the same |
| `↵` | open (or focus) the review workspace, then `on_open` | open (or focus) the feature workspace | open (or focus) the project's conversation |
| `s` | start the workspace and stay in the list — no `on_open`, no `after_open`; press it on one row after another | the same | the same |
| `f` | send the check-feedback prompt to the PR's Claude and stay; only on a PR with a conversation | — | — |
| `o` | open the PR in the browser | open the issue in Linear | open the project in Linear |
| `y` | copy the PR URL | copy the issue key | copy the project name, what `/` and a search take |
| `c` | close the workspace: worktree, branch and window; refused while tracked files have uncommitted changes (`owl pr close --force <N>` discards them) | the same, for the feature | worktree and window; the session id is kept, so the next `↵` resumes the conversation |
| `/` | filter by number; `esc` clears | filter by key, title or project | filter by name |
| `r` `?` `q` | refresh, help with the full legend, quit | the same | the same |

In the mine pane the same keys act on a PR of your own, so three of
them read differently. `↵` opens **its branch's** workspace — the
feature's, when the branch carries an issue key — and starts a fresh
conversation on `mine.prompt` rather than the review skill. `f` is
`bindings.mine`'s: the review you were **given**, checked rather than
complied with. And `c` refuses when the workspace is a feature's,
naming `owl issue close <KEY>` instead: that branch holds commits that
exist nowhere else, and only `issue close` checks for them first.

`↵`, `s`, `f` and `c` run in the background: the list stays usable
while the child works, the row shows a spinner in the worktree slot, a
second press on the same row is refused until it reports, and a
failure shows in the action row — or, once a popup has closed, as a
notification from the multiplexer. Every key is rebindable (`keys`),
and `bindings` add your own to the PR and issue lists; a project row
takes none until it has more than one workspace to aim at.

## Prompts and links on a key

```yaml
bindings:
  pr:
    - key: d
      name: Dependabot
      prompt: "/owl:dependabot {pr}"
    - key: l
      name: Linear
      pattern: 'PROJ-\d+'        # {id} is the first match in title, body and branch
      url: https://linear.app/my-org/issue/{id}
  issue:
    - key: p
      name: Continue
      prompt: "Continue {key}: pick up where you left off"
      when: conversation         # only on a row whose agent has a conversation
```

A binding is a key, a `name` for the help view and exactly one of
`prompt` and `url`. A prompt goes to the row's agent the way `s`
starts one — `owl pr start <N> --prompt …` — so it starts a workspace
where there is none and resumes the conversation where there is one.
A URL opens with `open_cmd`. Placeholders: `{pr}` (or `{key}` on the
issue list), `{repo}`, `{branch}`, `{url}` and `{id}`, the first match
of `pattern`; a binding with a pattern does nothing on a row it does
not match. `when: conversation` keeps the key to rows whose agent
already has one, which is how the shipped `f` behaves; listing `f`
again replaces it. Keys are checked per list, so `l` may mean one thing
on PRs and another on issues. Prefer a skill of your own? Bind it:
`/team:review {pr}`.

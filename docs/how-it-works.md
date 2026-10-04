# How it works

| layer | what | owner |
|---|---|---|
| git worktree | `<repo>/.worktrees.local/<name>` on a branch of the same name: `pr-<N>-<slug>` fetched from `pull/N/head`, or the issue's branch. A project's `proj-<slug>` is detached at the remote's default branch instead: it has no branch of its own, and its agent reads rather than commits | `open` creates, `close` removes |
| window | same name, in the multiplexer, cwd the worktree, running `agent.cmd` | `open` creates, `close` kills |
| conversation | Claude's transcript for that directory | survives `close`; `open` resumes it with `-c`, or for a project with `--resume` and the id owl keeps in `$XDG_STATE_HOME/owl/projects.json`, when that conversation's own transcript is there |

A prompt (`f`, a binding, `open --prompt`) is typed into the window as
one line of keystrokes. If the agent has exited — the window is back at
a shell — it is started again with `-c` and the prompt; while Claude is
blocked on a question or a permission, the prompt is refused. owl works
on one repo at a time — the working directory's, or `default_repo` —
and window names carry the PR number, the issue key or the project
slug, not the repo, so keep one repo per session.

The multiplexers differ in how a window is made, how the state is
read and what happens after Enter: [multiplexers.md](multiplexers.md)
has the table, the per-multiplexer notes and the one environment rule
that matters (start the multiplexer from a clean shell, not from
inside Claude).
